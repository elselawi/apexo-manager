// Package monitoring samples system and PocketBase health at a deliberately
// low frequency and stores compact history in SQLite.
package monitor

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Service samples server and instance health.
type Service struct {
	Store   *store.Store
	Cfg     func() settings.Config
	Systemd *systemd.Manager
	Caddy   *caddy.Manager
	DataDir string
	Log     *slog.Logger

	mu        sync.Mutex
	lastStats cpuStats
	haveStats bool
	diskCache map[string]diskSample
}

type cpuStats struct {
	total, idle uint64
}

type diskSample struct {
	bytes int64
	at    time.Time
}

// ServerInfo describes the Linux host.
type ServerInfo struct {
	Hostname     string  `json:"hostname"`
	OS           string  `json:"os"`
	Kernel       string  `json:"kernel"`
	Arch         string  `json:"arch"`
	CPUs         int     `json:"cpus"`
	UptimeSecs   int64   `json:"uptimeSeconds"`
	Load1        float64 `json:"load1"`
	MemTotal     int64   `json:"memTotal"`
	MemAvailable int64   `json:"memAvailable"`
	DiskTotal    int64   `json:"diskTotal"`
	DiskFree     int64   `json:"diskFree"`
	DataDir      string  `json:"dataDir"`
}

// Start runs the sampler until ctx is cancelled. Domain/TLS checks run less
// frequently than basic health checks.
func (s *Service) Start(ctx context.Context) {
	s.runOnce(ctx)
	interval := time.Duration(s.Cfg().MonitorIntervalSecs) * time.Second
	if interval < 15*time.Second {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	domainTicker := time.NewTicker(5 * time.Minute)
	defer domainTicker.Stop()
	maintenance := time.NewTicker(6 * time.Hour)
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		case <-domainTicker.C:
			s.checkDomains(ctx)
		case <-maintenance.C:
			s.prune(ctx)
		}
	}
}

func (s *Service) runOnce(ctx context.Context) {
	if _, err := s.SampleServer(ctx); err != nil {
		s.log().Warn("server sample failed", "error", err)
	}
	instances, err := s.Store.ListInstances(ctx)
	if err != nil {
		s.log().Warn("list instances for monitoring", "error", err)
		return
	}
	for i := range instances {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := s.SampleInstance(ctx, &instances[i]); err != nil {
			s.log().Warn("instance sample failed", "instance", instances[i].Name, "error", err)
		}
	}
}

// SampleServer records one server metrics point.
func (s *Service) SampleServer(ctx context.Context) (store.ServerMetrics, error) {
	cpu, err := s.cpuPercent(ctx)
	if err != nil {
		s.log().Warn("cpu sample failed", "error", err)
	}
	memTotal, memAvail, _ := memInfo()
	diskTotal, diskUsed := diskUsage(s.DataDir)
	load1, uptime := loadAndUptime()
	m := store.ServerMetrics{
		CapturedAt: store.Now(), CPUPercent: cpu, MemUsed: memTotal - memAvail, MemTotal: memTotal,
		DiskUsed: diskUsed, DiskTotal: diskTotal, Load1: load1, UptimeSeconds: uptime,
	}
	if err := s.Store.InsertServerMetrics(ctx, &m); err != nil {
		return m, err
	}
	return m, nil
}

// SampleInstance records one instance point and updates runtime state.
func (s *Service) SampleInstance(ctx context.Context, inst *store.Instance) (store.InstanceMetrics, error) {
	m := store.InstanceMetrics{InstanceID: inst.ID, CapturedAt: store.Now()}
	status, health := "unknown", "unknown"
	if inst.ServiceName != "" {
		st, err := s.Systemd.Status(ctx, inst.ServiceName)
		if err != nil {
			m.Error = "systemd: " + err.Error()
		} else if !st.Exists {
			status = "not-found"
			health = "unknown"
		} else if st.Running {
			status = "running"
			m.MemBytes = st.MemoryBytes
		} else if st.ActiveState == "failed" {
			status = "failed"
			health = "unhealthy"
		} else {
			status = "stopped"
			health = "down"
		}
	} else {
		m.Error = "no systemd service configured"
	}
	if status == "running" {
		client := pocketbase.New(inst.Port)
		hc, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := client.Health(hc)
		cancel()
		if err == nil {
			health = "healthy"
		} else {
			health = "unhealthy"
			m.Error = "health: " + err.Error()
		}
	}
	m.PortListening = netutil.PortListening(ctx, inst.Port)
	m.Status = status
	m.Health = health
	m.DiskBytes = s.instanceDisk(inst.DataDir)
	if m.MemBytes == 0 && status == "running" {
		m.MemBytes = processMemoryFromSystemd(ctx, s.Systemd, inst.ServiceName)
	}
	if err := s.Store.InsertInstanceMetrics(ctx, &m); err != nil {
		return m, err
	}
	lastSeen := inst.LastSeenAt
	if status == "running" {
		lastSeen = m.CapturedAt
	}
	if err := s.Store.UpdateInstanceRuntime(ctx, inst.ID, status, health, m.CapturedAt, m.Error, lastSeen); err != nil {
		return m, err
	}
	inst.Status = status
	inst.Health = health
	inst.LastHealthAt = m.CapturedAt
	inst.LastHealthError = m.Error
	return m, nil
}

func (s *Service) checkDomains(ctx context.Context) {
	domains, err := s.Store.ListDomains(ctx)
	if err != nil {
		return
	}
	for i := range domains {
		d := &domains[i]
		select {
		case <-ctx.Done():
			return
		default:
		}
		dnsStatus, caddyStatus, tlsStatus, expiry, errText := "error", "unknown", "unknown", "", ""
		if res, err := netutil.CheckPublicDNS(ctx, d.Hostname); err == nil && len(res.Addresses) > 0 {
			dnsStatus = "ok"
		} else if err != nil {
			errText = "dns: " + err.Error()
		}
		if d.CaddyManaged {
			if d.SnippetPath != "" {
				if _, err := os.Stat(d.SnippetPath); err == nil {
					caddyStatus = "ok"
				} else {
					caddyStatus = "missing"
				}
			}
		}
		tls := netutil.CheckTLS(ctx, d.Hostname)
		if tls.Error == "" {
			tlsStatus = "valid"
			if tls.DaysLeft < 14 {
				tlsStatus = "expiring"
			}
			expiry = tls.NotAfter
		} else {
			tlsStatus = "error"
			if errText == "" {
				errText = "tls: " + tls.Error
			}
		}
		if err := s.Store.UpdateDomainChecks(ctx, d.ID, dnsStatus, caddyStatus, tlsStatus, expiry, store.Now(), errText); err != nil {
			s.log().Warn("update domain checks", "domain", d.Hostname, "error", err)
		}
	}
}

func (s *Service) prune(ctx context.Context) {
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	if err := s.Store.PruneMetrics(ctx, cutoff); err != nil {
		s.log().Warn("prune metrics", "error", err)
	}
}

func (s *Service) cpuPercent(ctx context.Context) (float64, error) {
	read := func() (cpuStats, error) {
		f, err := os.Open("/proc/stat")
		if err != nil {
			return cpuStats{}, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		if !sc.Scan() {
			return cpuStats{}, fmt.Errorf("empty /proc/stat")
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			return cpuStats{}, fmt.Errorf("invalid /proc/stat")
		}
		var total, idle uint64
		for i := 1; i < len(fields); i++ {
			v, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return cpuStats{}, err
			}
			total += v
			if i == 4 || i == 5 {
				idle += v
			}
		}
		return cpuStats{total: total, idle: idle}, nil
	}
	s.mu.Lock()
	prev, had := s.lastStats, s.haveStats
	s.mu.Unlock()
	if !had {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		st, err := read()
		if err != nil {
			return 0, err
		}
		s.mu.Lock()
		s.lastStats = st
		s.haveStats = true
		s.mu.Unlock()
		return 0, nil
	}
	cur, err := read()
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.lastStats = cur
	s.mu.Unlock()
	dt := cur.total - prev.total
	di := cur.idle - prev.idle
	if dt == 0 {
		return 0, nil
	}
	return 100 * float64(dt-di) / float64(dt), nil
}

func (s *Service) instanceDisk(dir string) int64 {
	if dir == "" {
		return 0
	}
	s.mu.Lock()
	if s.diskCache == nil {
		s.diskCache = map[string]diskSample{}
	}
	cached, ok := s.diskCache[dir]
	s.mu.Unlock()
	if ok && time.Since(cached.at) < 10*time.Minute {
		return cached.bytes
	}
	bytes := directorySize(dir)
	s.mu.Lock()
	s.diskCache[dir] = diskSample{bytes: bytes, at: time.Now()}
	s.mu.Unlock()
	return bytes
}

func directorySize(root string) int64 {
	var total int64
	count := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		count++
		if count > 200000 {
			return filepath.SkipAll
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ServerInfo gathers static and current host information.
func ServerInfoFrom(cfg settings.Config, dataDir string) ServerInfo {
	hostname, _ := os.Hostname()
	osName := readOSName()
	total, avail, _ := memInfo()
	diskTotal, diskUsed := diskUsage(dataDir)
	load1, uptime := loadAndUptime()
	return ServerInfo{
		Hostname: hostname, OS: osName, Kernel: readTrimmed("/proc/sys/kernel/osrelease"),
		Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), UptimeSecs: uptime, Load1: load1,
		MemTotal: total, MemAvailable: avail, DiskTotal: diskTotal, DiskFree: diskTotal - diskUsed,
		DataDir: dataDir,
	}
}

func memInfo() (total, available int64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			total = v * 1024
		case "MemAvailable:":
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			available = v * 1024
		}
	}
	return total, available, sc.Err()
}

func diskUsage(path string) (total, used int64) {
	if path == "" {
		path = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	total = int64(st.Blocks) * int64(st.Bsize)
	free := int64(st.Bavail) * int64(st.Bsize)
	return total, total - free
}

func loadAndUptime() (float64, int64) {
	load := 0.0
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 0 {
			load, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	uptime := int64(0)
	if raw, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 0 {
			v, _ := strconv.ParseFloat(fields[0], 64)
			uptime = int64(v)
		}
	}
	return load, uptime
}

func readOSName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(sc.Text(), "PRETTY_NAME="), `"`)
		}
	}
	return runtime.GOOS
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func processMemoryFromSystemd(ctx context.Context, mgr *systemd.Manager, service string) int64 {
	st, err := mgr.Status(ctx, service)
	if err != nil {
		return 0
	}
	return st.MemoryBytes
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
