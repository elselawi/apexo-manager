// Package discovery inventories existing PocketBase installations using
// read-only OS inspection.
package discovery

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Found is one discovered installation candidate.
type Found struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Hostname    string   `json:"hostname"`
	BaseDir     string   `json:"baseDir"`
	BinaryPath  string   `json:"binaryPath"`
	DataDir     string   `json:"dataDir"`
	Port        int      `json:"port"`
	ServiceName string   `json:"serviceName"`
	ServiceFile string   `json:"serviceFile"`
	RunAsUser   string   `json:"runAsUser,omitempty"`
	Version     string   `json:"version,omitempty"`
	Running     bool     `json:"running"`
	Domains     []string `json:"domains,omitempty"`
	Evidence    []string `json:"evidence"`
	Warnings    []string `json:"warnings,omitempty"`
	Tracked     bool     `json:"tracked"`
	Managed     bool     `json:"managed"`
}

// Service discovers installations. It performs no writes.
type Service struct {
	Store *store.Store
	Cfg   func() settings.Config
}

// Discover scans systemd units, processes, listening sockets, known roots, and
// Caddy configuration. Errors in optional sources become warnings rather than
// aborting the entire inventory.
func (s *Service) Discover(ctx context.Context) ([]Found, []string, error) {
	cfg := s.Cfg()
	warnings := []string{}
	byKey := map[string]*Found{}

	units, unitWarnings := scanUnits(ctx, cfg)
	warnings = append(warnings, unitWarnings...)
	for _, u := range units {
		f := mergeCandidate(byKey, keyFor(u.ServiceFile, u.DataDir, u.BinaryPath), &u)
		merge(f, u)
		f.Evidence = appendUnique(f.Evidence, "systemd unit "+u.ServiceFile)
	}

	procs, procWarnings := scanProcesses(ctx)
	warnings = append(warnings, procWarnings...)
	for _, p := range procs {
		f := mergeCandidate(byKey, keyFor(p.ServiceFile, p.DataDir, p.BinaryPath), &p)
		merge(f, p)
		f.Evidence = appendUnique(f.Evidence, "running PocketBase process")
	}

	files, fileWarnings := scanFilesystem(ctx, cfg.DiscoveryRoots)
	warnings = append(warnings, fileWarnings...)
	for _, fnd := range files {
		f := mergeCandidate(byKey, keyFor(fnd.ServiceFile, fnd.DataDir, fnd.BinaryPath), &fnd)
		merge(f, fnd)
		f.Evidence = appendUnique(f.Evidence, "filesystem artifact")
	}

	domains, caddyWarnings := scanCaddy(ctx, cfg)
	warnings = append(warnings, caddyWarnings...)
	for _, f := range byKey {
		if f.Port > 0 {
			for _, d := range domains {
				if d.port == f.Port {
					f.Domains = appendUnique(f.Domains, d.hostname)
				}
			}
		}
	}

	existing, err := s.Store.ListInstances(ctx)
	if err != nil {
		return nil, warnings, err
	}
	for _, f := range byKey {
		for _, inst := range existing {
			if (inst.ServiceFile != "" && samePath(inst.ServiceFile, f.ServiceFile)) ||
				(inst.DataDir != "" && samePath(inst.DataDir, f.DataDir)) ||
				(inst.BinaryPath != "" && samePath(inst.BinaryPath, f.BinaryPath)) {
				f.Tracked = true
				f.Managed = inst.Managed
			}
		}
		if f.ServiceName != "" {
			if _, err := os.Stat(f.ServiceFile); err != nil {
				f.Warnings = appendUnique(f.Warnings, "systemd unit file not found")
			}
		}
		if f.BinaryPath == "" {
			f.Warnings = appendUnique(f.Warnings, "PocketBase executable path could not be determined")
		}
		if f.DataDir == "" {
			f.Warnings = appendUnique(f.Warnings, "data directory could not be determined")
		}
		if f.Port == 0 {
			f.Warnings = appendUnique(f.Warnings, "listening port could not be determined")
		}
	}

	out := make([]Found, 0, len(byKey))
	names := map[string]bool{}
	for _, f := range byKey {
		if f.Name == "" {
			f.Name = inferName(*f)
		}
		f.Name = uniqueName(f.Name, names)
		f.Hostname = f.Name
		sort.Strings(f.Domains)
		sort.Strings(f.Evidence)
		sort.Strings(f.Warnings)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}

func mergeFound(m map[string]*Found, key string) *Found {
	if f, ok := m[key]; ok {
		return f
	}
	f := &Found{Key: key}
	m[key] = f
	return f
}

// mergeCandidate consolidates artifacts that describe the same installation
// even when discovered through different evidence (unit vs. data directory).
func mergeCandidate(m map[string]*Found, key string, probe *Found) *Found {
	if f, ok := m[key]; ok {
		return f
	}
	for _, f := range m {
		if probe.DataDir != "" && f.DataDir != "" && samePath(f.DataDir, probe.DataDir) {
			return f
		}
	}
	return mergeFound(m, key)
}

func merge(dst *Found, src Found) {
	if src.Name != "" && dst.Name == "" {
		dst.Name = src.Name
	}
	if src.BaseDir != "" && dst.BaseDir == "" {
		dst.BaseDir = src.BaseDir
	}
	if src.BinaryPath != "" && dst.BinaryPath == "" {
		dst.BinaryPath = src.BinaryPath
	}
	if src.DataDir != "" && dst.DataDir == "" {
		dst.DataDir = src.DataDir
	}
	if src.Port != 0 && dst.Port == 0 {
		dst.Port = src.Port
	}
	if src.ServiceName != "" && dst.ServiceName == "" {
		dst.ServiceName = src.ServiceName
	}
	if src.ServiceFile != "" && dst.ServiceFile == "" {
		dst.ServiceFile = src.ServiceFile
	}
	if src.RunAsUser != "" && dst.RunAsUser == "" {
		dst.RunAsUser = src.RunAsUser
	}
	if src.Version != "" && dst.Version == "" {
		dst.Version = src.Version
	}
	dst.Running = dst.Running || src.Running
	for _, d := range src.Domains {
		dst.Domains = appendUnique(dst.Domains, d)
	}
	for _, e := range src.Evidence {
		dst.Evidence = appendUnique(dst.Evidence, e)
	}
	for _, w := range src.Warnings {
		dst.Warnings = appendUnique(dst.Warnings, w)
	}
}

func keyFor(serviceFile, dataDir, binary string) string {
	if serviceFile != "" {
		return "unit:" + serviceFile
	}
	if dataDir != "" {
		return "data:" + dataDir
	}
	return "bin:" + binary
}

func scanUnits(ctx context.Context, cfg settings.Config) ([]Found, []string) {
	dirs := []string{cfg.ServiceDir, "/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"}
	seenDirs := map[string]bool{}
	var out []Found
	var warnings []string
	for _, dir := range dirs {
		if dir == "" || seenDirs[dir] {
			continue
		}
		seenDirs[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, "read "+dir+": "+err.Error())
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".service") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			raw, err := platform.ReadFileLimited(path, 512<<10)
			if err != nil {
				continue
			}
			unit := parseUnit(e.Name(), path, string(raw))
			if unit == nil {
				continue
			}
			out = append(out, *unit)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceName < out[j].ServiceName })
	return out, warnings
}

func parseUnit(filename, path, content string) *Found {
	lines := joinContinuations(content)
	var execLine, workdir, user string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") && execLine == "" {
			execLine = strings.TrimPrefix(trimmed, "ExecStart=")
		}
		if strings.HasPrefix(trimmed, "WorkingDirectory=") {
			workdir = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "WorkingDirectory=")), `"`)
		}
		if strings.HasPrefix(trimmed, "User=") {
			user = strings.TrimSpace(strings.TrimPrefix(trimmed, "User="))
		}
	}
	if execLine == "" {
		return nil
	}
	tokens := splitCommand(execLine)
	if len(tokens) == 0 {
		return nil
	}
	binary := strings.Trim(tokens[0], `"'`)
	if !looksLikePocketBase(binary) && !looksLikePocketBase(execLine) {
		return nil
	}
	args := tokens[1:]
	dir, port := parsePBArgs(args)
	if !filepath.IsAbs(binary) {
		if workdir != "" {
			binary = filepath.Join(workdir, binary)
		}
	}
	if dir != "" && !filepath.IsAbs(dir) && workdir != "" {
		dir = filepath.Join(workdir, dir)
	}
	base := filepath.Dir(binary)
	if dir == "" {
		candidate := filepath.Join(base, "pb_data")
		if _, err := os.Stat(candidate); err == nil {
			dir = candidate
		}
	}
	name := strings.TrimSuffix(filename, ".service")
	return &Found{
		Name:        sanitizeName(name),
		BaseDir:     base,
		BinaryPath:  binary,
		DataDir:     dir,
		Port:        port,
		ServiceName: strings.TrimSuffix(filename, ".service"),
		ServiceFile: path,
		RunAsUser:   user,
	}
}

var pbBinaryRE = regexp.MustCompile(`(?i)pocketbase`)

func looksLikePocketBase(s string) bool { return pbBinaryRE.MatchString(s) }

func joinContinuations(content string) []string {
	var out []string
	var cur string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmedRight, "\\") {
			cur += strings.TrimSuffix(trimmedRight, "\\") + " "
			continue
		}
		cur += line
		out = append(out, cur)
		cur = ""
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func splitCommand(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return out
}

func parsePBArgs(args []string) (string, int) {
	var dir string
	port := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := ""
		switch {
		case strings.HasPrefix(a, "--dir="):
			dir = strings.TrimPrefix(a, "--dir=")
		case a == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "--http="):
			val = strings.TrimPrefix(a, "--http=")
		case a == "--http" && i+1 < len(args):
			val = args[i+1]
			i++
		}
		if val != "" {
			_, p, err := netSplit(val)
			if err == nil && p > 0 {
				port = p
			}
		}
	}
	return strings.Trim(dir, `"'`), port
}

func netSplit(addr string) (string, int, error) {
	addr = strings.Trim(addr, `"'`)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		addr = strings.SplitN(addr, "://", 2)[1]
	}
	if i := strings.Index(addr, "/"); i >= 0 {
		addr = addr[:i]
	}
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, 0, nil
	}
	port, err := strconv.Atoi(addr[idx+1:])
	if err != nil {
		return "", 0, err
	}
	return addr[:idx], port, nil
}

func scanProcesses(ctx context.Context) ([]Found, []string) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, []string{"read /proc: " + err.Error()}
	}
	sockets := listeningSockets()
	var out []Found
	var warnings []string
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return out, warnings
		default:
		}
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		proc := filepath.Join("/proc", e.Name())
		raw, err := os.ReadFile(filepath.Join(proc, "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(parts) == 0 {
			continue
		}
		binary := parts[0]
		if !looksLikePocketBase(filepath.Base(binary)) {
			continue
		}
		exe, err := os.Readlink(filepath.Join(proc, "exe"))
		if err == nil {
			binary = strings.TrimSuffix(exe, " (deleted)")
		}
		cwd, _ := os.Readlink(filepath.Join(proc, "cwd"))
		dir, port := parsePBArgs(parts[1:])
		if port == 0 {
			for _, p := range sockets[pid] {
				port = p
				break
			}
		}
		if dir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		if dir == "" && cwd != "" {
			candidate := filepath.Join(cwd, "pb_data")
			if _, err := os.Stat(candidate); err == nil {
				dir = candidate
			}
		}
		base := filepath.Dir(binary)
		svc := serviceFromCgroup(proc)
		f := Found{
			BaseDir:    base,
			BinaryPath: binary,
			DataDir:    dir,
			Port:       port,
			Running:    true,
		}
		if svc != "" {
			f.ServiceName = svc
			f.ServiceFile = findUnitFile(svc)
		}
		f.Name = inferName(f)
		out = append(out, f)
	}
	return out, warnings
}

func listeningSockets() map[int][]int {
	inodePort := map[string]int{}
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if i == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			local := fields[1]
			idx := strings.LastIndex(local, ":")
			if idx < 0 {
				continue
			}
			port, err := strconv.ParseInt(local[idx+1:], 16, 32)
			if err != nil {
				continue
			}
			inodePort[fields[9]] = int(port)
		}
	}
	out := map[int][]int{}
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range procs {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if port, ok := inodePort[inode]; ok {
				out[pid] = appendUniqueInt(out[pid], port)
			}
		}
	}
	return out
}

func serviceFromCgroup(proc string) string {
	raw, err := os.ReadFile(filepath.Join(proc, "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		idx := strings.LastIndex(line, "/")
		name := strings.TrimSpace(line[idx+1:])
		if strings.HasSuffix(name, ".service") {
			return strings.TrimSuffix(name, ".service")
		}
	}
	return ""
}

func findUnitFile(service string) string {
	for _, dir := range []string{"/etc/systemd/system", "/lib/systemd/system", "/usr/lib/systemd/system"} {
		path := filepath.Join(dir, service+".service")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func scanFilesystem(ctx context.Context, roots []string) ([]Found, []string) {
	var out []Found
	var warnings []string
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		count := 0
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err != nil {
				return nil
			}
			count++
			if count > 30000 {
				return filepath.SkipAll
			}
			rel, _ := filepath.Rel(root, path)
			depth := len(strings.Split(rel, string(os.PathSeparator)))
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" || strings.Contains(d.Name(), ".pbmanager-") || (depth > 4 && root != path) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			name := d.Name()
			if name == "pocketbase" {
				info, err := d.Info()
				if err != nil || info.Mode()&0o111 == 0 {
					return nil
				}
				base := filepath.Dir(path)
				data := filepath.Join(base, "pb_data")
				if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
					data = ""
				}
				out = append(out, Found{BaseDir: base, BinaryPath: path, DataDir: data, Name: sanitizeName(filepath.Base(base))})
			}
			if name == "data.db" {
				dir := filepath.Dir(path)
				base := dir
				if filepath.Base(dir) == "pb_data" {
					base = filepath.Dir(dir)
				}
				bin := filepath.Join(base, "pocketbase")
				if _, err := os.Stat(bin); err != nil {
					bin = ""
				}
				out = append(out, Found{BaseDir: base, BinaryPath: bin, DataDir: dir, Name: sanitizeName(filepath.Base(base))})
			}
			return nil
		})
	}
	return out, warnings
}

type domainMap struct {
	hostname string
	port     int
}

func scanCaddy(ctx context.Context, cfg settings.Config) ([]domainMap, []string) {
	var out []domainMap
	var warnings []string
	read := func(path string) {
		raw, err := platform.ReadFileLimited(path, 2<<20)
		if err != nil {
			return
		}
		parsed := caddy.Parse(string(raw))
		for _, site := range parsed.Sites {
			for _, upstream := range site.Upstreams {
				_, port, err := netSplit(upstream)
				if err == nil && port > 0 {
					for _, host := range site.Addresses {
						out = append(out, domainMap{hostname: host, port: port})
					}
				}
			}
		}
	}
	read(cfg.CaddyfilePath)
	entries, _ := os.ReadDir(cfg.CaddySnippetDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".caddy") {
			read(filepath.Join(cfg.CaddySnippetDir, e.Name()))
		}
	}
	return out, warnings
}

func inferName(f Found) string {
	if f.ServiceName != "" {
		return sanitizeName(f.ServiceName)
	}
	if f.BaseDir != "" {
		return sanitizeName(filepath.Base(f.BaseDir))
	}
	if f.DataDir != "" {
		return sanitizeName(filepath.Base(filepath.Dir(f.DataDir)))
	}
	return "pocketbase"
}

var nameCleanRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nameCleanRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		s = "pocketbase"
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func uniqueName(name string, seen map[string]bool) string {
	candidate := name
	for i := 2; seen[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	seen[candidate] = true
	return candidate
}

func appendUnique(in []string, v string) []string {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

func appendUniqueInt(in []int, v int) []int {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
