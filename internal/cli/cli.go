// Package cli implements the root-owned pbmanager command line interface.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/apexo/pbmanager/internal/api"
	appcore "github.com/apexo/pbmanager/internal/app"
	"github.com/apexo/pbmanager/internal/auth"
	"github.com/apexo/pbmanager/internal/config"
	"github.com/apexo/pbmanager/internal/cron"
	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/instances"
	"github.com/apexo/pbmanager/internal/store"
)

// Version is set at build time.
var Version = "dev"

var geteuid = os.Geteuid

var configPath = config.Path("")

// Run executes the CLI and returns a process exit code.
func Run(args []string) int {
	args = stripGlobalConfig(args)
	if len(args) == 0 {
		printHelp()
		return 0
	}
	cmd := args[0]
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
		return 0
	case "version", "--version":
		fmt.Printf("pbmanager %s\n", Version)
		return 0
	case "serve":
		return cmdServe(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "instances":
		return cmdInstances(args[1:])
	case "admin":
		return cmdAdmin(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "jobs":
		return cmdJobs(args[1:])
	case "backup":
		return cmdBackup(args[1:])
	case "caddy":
		return cmdCaddy(args[1:])
	case "diagnose":
		return cmdDiagnose(args[1:])
	case "recover":
		return cmdRecover(args[1:])
	case "db":
		return cmdDB(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printHelp()
		return 2
	}
}

func stripGlobalConfig(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			}
		case "--config=":
			// ignore malformed
		default:
			if strings.HasPrefix(args[i], "--config=") {
				configPath = strings.TrimPrefix(args[i], "--config=")
				continue
			}
			out = append(out, args[i])
		}
	}
	return out
}

func printHelp() {
	fmt.Print(`pbmanager - PocketBase infrastructure manager

Usage:
  pbmanager [--config PATH] <command> [options]

Commands:
  serve                 Run the management API and embedded dashboard
  status                Show manager and instance status
  instances list        List managed instances
  instances inspect ID  Inspect one instance
  instances discover    Discover existing installations (read-only)
  instances import      Import an existing installation without modifying it
  instances create      Provision a new PocketBase installation
  instances start|stop|restart ID
  instances update ID --version VERSION
  instances logs ID
  instances set-credentials ID
  instances delete ID [--purge-data]
  admin list            List dashboard administrators
  admin create          Create a dashboard administrator
  admin reset-password  Reset an administrator password
  admin disable|enable|delete|revoke-sessions
  config show           Show bootstrap and server configuration
  config set            Update public URL, listen address, or session policy
  config validate       Validate configuration and filesystem permissions
  config init           Create a bootstrap configuration file
  jobs list|inspect|retry|cancel
  backup create ID      Create a verified backup
  backup list [ID]
  backup verify BACKUP_ID
  backup restore BACKUP_ID
  diagnose              Read-only environment diagnostics
  recover               Safe administrative recovery workflow
  db check|snapshot|restore
  version               Print version

Root is verified using the effective UID, never a username argument. Passwords
are prompted on a terminal and are never accepted as command-line arguments.
`)
}

func requireRoot() error {
	if geteuid() != 0 {
		return errors.New("this command must be run as root (or via sudo)")
	}
	return nil
}

func loadConfig() (config.Runtime, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, fmt.Errorf("configuration %s does not exist; run 'pbmanager config init' as root or install with scripts/install.sh", configPath)
		}
		return cfg, err
	}
	cfg.FromEnv()
	return cfg, cfg.Normalize()
}

func openApp(ctx context.Context) (*appcore.App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return appcore.Open(ctx, cfg, log)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}

func auditCLI(ctx context.Context, app *appcore.App, action, targetType, targetID string, detail any) {
	raw, _ := json.Marshal(detail)
	_ = app.Store.CreateAudit(ctx, &store.AuditEntry{
		ActorType: "cli-root", ActorName: currentUser(), Action: action,
		TargetType: targetType, TargetID: targetID, Detail: string(raw),
	})
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.StringVar(&configPath, "config", configPath, "bootstrap config path")
	fs.Bool("allow-nonroot", false, "development only: allow serving without root")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if !boolFlag(fs, "allow-nonroot") {
		if err := requireRoot(); err != nil {
			return fail(err)
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	app, err := appcore.Open(ctx, cfg, log)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	if err := app.Start(runCtx); err != nil {
		return fail(err)
	}
	apiHandler := newHTTPHandler(app, log)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           apiHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("pbmanager listening", "addr", cfg.ListenAddr, "public_url", cfg.PublicURL, "version", Version)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		_ = server.Shutdown(shutdownCtx)
		cancel()
		cancelRun()
		app.Stop()
		app.Wait()
		return 0
	case err := <-errCh:
		cancelRun()
		app.Stop()
		app.Wait()
		if !errors.Is(err, http.ErrServerClosed) {
			return fail(err)
		}
		return 0
	}
}

func boolFlag(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	v, _ := strconv.ParseBool(f.Value.String())
	return v
}

// parseArgs allows flags before or after positional arguments. The standard
// flag package stops at the first positional; a professional CLI should accept
// both `instances update NAME --version X` and `... --version X NAME`.
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positional = append(positional, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			flags = append(flags, arg)
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			flags = append(flags, arg)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		boolValue, isBool := f.Value.(interface{ IsBoolFlag() bool })
		flags = append(flags, arg)
		if isBool && boolValue.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

func newHTTPHandler(app *appcore.App, log *slog.Logger) http.Handler {
	return api.New(app, log, Version).Handler()
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	instancesList, _ := app.Store.ListInstances(ctx)
	counts, _ := app.Store.InstanceCounts(ctx)
	adminCount, _ := app.Store.CountAdmins(ctx)
	jobCounts, _ := app.Store.CountJobsByStatus(ctx)
	diag := app.Diagnose(ctx)
	if *jsonOut {
		printJSON(map[string]any{"version": Version, "instances": instancesList, "instanceCounts": counts, "jobs": jobCounts, "admins": adminCount, "diagnostics": diag})
		return 0
	}
	fmt.Printf("pbmanager %s\n", Version)
	fmt.Printf("config:  %s\n"+"", configPath)
	fmt.Printf("data:    %s\n", app.Config.DataDir)
	fmt.Printf("database: %s\n", app.Config.DBPath)
	fmt.Printf("admins:  %d\n", adminCount)
	fmt.Printf("instances: %d (running %d, stopped %d, failed %d)\n", len(instancesList), counts["running"], counts["stopped"], counts["failed"])
	fmt.Printf("jobs:    queued %d, running %d, failed %d\n", jobCounts["queued"], jobCounts["running"], jobCounts["failed"])
	if len(diag.Errors) > 0 {
		fmt.Println("diagnostic errors:")
		for _, e := range diag.Errors {
			fmt.Println("  -", e)
		}
	}
	if len(instancesList) > 0 {
		fmt.Println("\nInstances:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATUS\tHEALTH\tPORT\tVERSION\tSERVICE")
		for _, i := range instancesList {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", i.Name, i.Status, i.Health, i.Port, i.PBVersion, i.ServiceName)
		}
		tw.Flush()
	}
	return 0
}

func cmdInstances(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager instances <list|inspect|discover|import|create|start|stop|restart|update|logs|set-credentials|delete>")
		return 2
	}
	switch args[0] {
	case "list":
		return instancesList(args[1:])
	case "inspect":
		return instancesInspect(args[1:])
	case "discover":
		return instancesDiscover(args[1:])
	case "import":
		return instancesImport(args[1:])
	case "create":
		return instancesCreate(args[1:])
	case "start", "stop", "restart":
		return instancesAction(args[0], args[1:])
	case "update":
		return instancesUpdate(args[1:])
	case "logs":
		return instancesLogs(args[1:])
	case "set-credentials":
		return instancesCredentials(args[1:])
	case "delete":
		return instancesDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown instances subcommand %q\n", args[0])
		return 2
	}
}

func instancesList(args []string) int {
	fs := flag.NewFlagSet("instances list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	list, err := app.Store.ListInstances(ctx)
	if err != nil {
		return fail(err)
	}
	if *jsonOut {
		printJSON(list)
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOSTNAME\tSTATUS\tHEALTH\tPORT\tVERSION\tMANAGED\tSERVICE")
	for _, i := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%v\t%s\n", i.Name, i.Hostname, i.Status, i.Health, i.Port, i.PBVersion, i.Managed, i.ServiceName)
	}
	tw.Flush()
	return 0
}

func instancesInspect(args []string) int {
	fs := flag.NewFlagSet("instances inspect", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" {
		return fail(errors.New("instance name or ID is required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	domains, _ := app.Store.ListDomainsByInstance(ctx, inst.ID)
	backups, _ := app.Store.ListBackups(ctx, inst.ID, 10)
	if *jsonOut {
		printJSON(map[string]any{"instance": inst, "domains": domains, "backups": backups})
		return 0
	}
	fmt.Printf("Instance:    %s (%s)\n", inst.Name, inst.ID)
	fmt.Printf("Hostname:    %s\n", inst.Hostname)
	fmt.Printf("Status:      %s / %s\n", inst.Status, inst.Health)
	fmt.Printf("Version:     %s\n", inst.PBVersion)
	fmt.Printf("Port:        %d\n", inst.Port)
	fmt.Printf("Base dir:    %s\n", inst.BaseDir)
	fmt.Printf("Binary:      %s\n", inst.BinaryPath)
	fmt.Printf("Data dir:    %s\n", inst.DataDir)
	fmt.Printf("Service:     %s (%s)\n", inst.ServiceName, inst.ServiceFile)
	fmt.Printf("Managed:     %v (source %s)\n", inst.Managed, inst.Source)
	fmt.Printf("PB auth:     %s\n", inst.PBAuthStatus)
	fmt.Printf("Backup:      %v cron=%s retention=%d next=%s\n", inst.BackupEnabled, inst.BackupCron, inst.BackupRetention, inst.NextBackupAt)
	if len(domains) > 0 {
		fmt.Println("Domains:")
		for _, d := range domains {
			fmt.Printf("  %s  dns=%s caddy=%s tls=%s\n", d.Hostname, d.DNSStatus, d.CaddyStatus, d.TLSStatus)
		}
	}
	if len(backups) > 0 {
		fmt.Println("Recent backups:")
		for _, b := range backups {
			fmt.Printf("  %s  %s  %d bytes  %s\n", b.ID, b.Status, b.Size, b.CreatedAt)
		}
	}
	return 0
}

func findInstance(ctx context.Context, app *appcore.App, name string) (*store.Instance, error) {
	if inst, err := app.Store.GetInstance(ctx, name); err == nil {
		return inst, nil
	}
	inst, err := app.Store.GetInstanceByName(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("instance %q not found", name)
		}
		return nil, err
	}
	return inst, nil
}

func instancesDiscover(args []string) int {
	fs := flag.NewFlagSet("instances discover", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	found, warnings, err := app.Discovery.Discover(ctx)
	if err != nil {
		return fail(err)
	}
	if *jsonOut {
		printJSON(map[string]any{"items": found, "warnings": warnings})
		return 0
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSERVICE\tBINARY\tDATA\tPORT\tRUNNING\tTRACKED\tDOMAINS")
	for _, f := range found {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%v\t%v\t%s\n", f.Name, f.ServiceName, f.BinaryPath, f.DataDir, f.Port, f.Running, f.Tracked, strings.Join(f.Domains, ","))
	}
	tw.Flush()
	for _, f := range found {
		for _, w := range f.Warnings {
			fmt.Fprintf(os.Stderr, "warning %s: %s\n", f.Name, w)
		}
	}
	return 0
}

func prompt(label string, secret bool) (string, error) {
	fmt.Fprint(os.Stderr, label)
	if secret {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	return strings.TrimSpace(line), err
}

func promptPassword(promptLabel string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", errors.New("a terminal or --password-stdin input is required")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	p1, err := prompt(promptLabel, true)
	if err != nil {
		return "", err
	}
	p2, err := prompt("Confirm password: ", true)
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("passwords do not match")
	}
	return p1, nil
}

func runJobCLI(ctx context.Context, app *appcore.App, jobType string, payload any, opts jobs.EnqueueOptions) (*store.Job, error) {
	job, _, err := app.Jobs.Enqueue(ctx, jobType, payload, opts)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "Queued job %s (%s)\n", job.ID, job.Type)
	cancelWorker, workerDone, err := maybeStartLocalWorker(ctx, app)
	if err != nil {
		return nil, err
	}
	cleanup := localWorkerCleanup(app, cancelWorker, workerDone)
	defer cleanup()
	seen := map[string]string{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		current, err := app.Store.GetJob(ctx, job.ID)
		if err != nil {
			continue
		}
		for _, step := range current.Steps {
			if seen[step.Name] != step.Status {
				seen[step.Name] = step.Status
				fmt.Fprintf(os.Stderr, "  [%s] %s", step.Status, step.Name)
				if step.Detail != "" {
					fmt.Fprintf(os.Stderr, " - %s", step.Detail)
				}
				fmt.Fprintln(os.Stderr)
			}
		}
		switch current.Status {
		case "succeeded", "failed", "canceled":
			return current, nil
		}
	}
}

func maybeStartLocalWorker(ctx context.Context, app *appcore.App) (context.CancelFunc, chan struct{}, error) {
	acquired, err := app.TryAcquireWorker()
	if err != nil {
		return nil, nil, err
	}
	if !acquired {
		return nil, nil, nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Jobs.Start(workerCtx)
	}()
	return cancel, done, nil
}

func localWorkerCleanup(app *appcore.App, cancel context.CancelFunc, done chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if cancel != nil {
				cancel()
				<-done
			}
			app.ReleaseWorker()
		})
	}
}

func serviceActive(ctx context.Context, app *appcore.App) bool {
	out, _, err := (platform.ExecRunner{}).Run(ctx, app.Settings.Get().SystemctlBinary, "is-active", "pbmanager")
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func instancesImport(args []string) int {
	fs := flag.NewFlagSet("instances import", flag.ContinueOnError)
	name := fs.String("name", "", "instance identifier")
	hostname := fs.String("hostname", "", "service identifier hostname")
	baseDir := fs.String("base-dir", "", "installation directory")
	binary := fs.String("binary", "", "PocketBase executable path")
	dataDir := fs.String("data-dir", "", "PocketBase data directory")
	port := fs.Int("port", 0, "listening port")
	service := fs.String("service", "", "systemd service name")
	serviceFile := fs.String("service-file", "", "systemd unit file")
	runAs := fs.String("run-as", "", "service user")
	version := fs.String("version", "", "PocketBase version")
	domains := fs.String("domains", "", "comma-separated domains already routed to this instance")
	notes := fs.String("notes", "", "notes")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if *name == "" {
		*name = fs.Arg(0)
	}
	if *name == "" || *binary == "" || *dataDir == "" || *port == 0 {
		return fail(errors.New("--name, --binary, --data-dir, and --port are required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	var domainList []string
	if *domains != "" {
		for _, d := range strings.Split(*domains, ",") {
			if d = strings.TrimSpace(d); d != "" {
				domainList = append(domainList, d)
			}
		}
	}
	inst, err := app.Instances.Import(ctx, instances.ImportInput{
		Name: *name, Hostname: *hostname, BaseDir: *baseDir, BinaryPath: *binary, DataDir: *dataDir,
		Port: *port, ServiceName: *service, ServiceFile: *serviceFile, RunAsUser: *runAs, PBVersion: *version,
		Domains: domainList, Notes: *notes, Source: "cli-import",
	}, "cli:"+currentUser())
	if err != nil {
		return fail(err)
	}
	auditCLI(ctx, app, "instance.imported", "instance", inst.ID, map[string]any{"name": inst.Name, "service": inst.ServiceName})
	fmt.Printf("Imported %s (%s). No files, services, Caddy routes, or DNS records were modified.\n", inst.Name, inst.ID)
	return 0
}

func instancesCreate(args []string) int {
	fs := flag.NewFlagSet("instances create", flag.ContinueOnError)
	name := fs.String("name", "", "instance identifier")
	hostname := fs.String("hostname", "", "hostname/service identifier")
	version := fs.String("version", "", "PocketBase version")
	arch := fs.String("arch", "", "target architecture")
	port := fs.Int("port", 0, "listening port (0 chooses a free port)")
	baseDir := fs.String("base-dir", "", "installation directory")
	dataDir := fs.String("data-dir", "", "data directory")
	serviceFile := fs.String("service-file", "", "systemd unit path")
	serviceName := fs.String("service-name", "", "systemd service name")
	runAs := fs.String("run-as", "", "service user")
	createUser := fs.Bool("create-user", true, "create a dedicated system user")
	allowRoot := fs.Bool("allow-root-user", false, "allow running PocketBase as root")
	domain := fs.String("domain", "", "public domain")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account ID")
	manageCaddy := fs.Bool("manage-caddy", true, "create a managed Caddy route")
	manageDNS := fs.Bool("manage-dns", false, "create a managed Cloudflare DNS record")
	proxied := fs.Bool("proxied", true, "enable Cloudflare proxy")
	smtpProfile := fs.String("smtp-profile", "", "SMTP profile ID")
	storageProfile := fs.String("storage-profile", "", "storage profile ID")
	backup := fs.Bool("backup", true, "enable scheduled backups")
	backupCron := fs.String("backup-cron", "0 3 * * *", "backup cron expression (UTC)")
	retention := fs.Int("backup-retention", 0, "backup retention count")
	checksum := fs.String("checksum", "", "optional expected SHA-256 of the release archive")
	notes := fs.String("notes", "", "notes")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if *name == "" {
		*name = fs.Arg(0)
	}
	if *name == "" {
		return fail(errors.New("instance name is required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	defaults, err := app.Instances.Defaults(ctx, *name)
	if err != nil {
		return fail(err)
	}
	if *hostname == "" {
		*hostname = *name
	}
	if *version == "" {
		*version = defaults.Version
	}
	if *arch == "" {
		*arch = defaults.Arch
	}
	if *baseDir == "" {
		*baseDir = defaults.BaseDir
	}
	if *dataDir == "" {
		*dataDir = defaults.DataDir
	}
	if *port == 0 {
		*port = defaults.Port
	}
	if *serviceName == "" {
		*serviceName = defaults.ServiceName
	}
	if *serviceFile == "" {
		*serviceFile = defaults.ServiceFile
	}
	if *runAs == "" {
		*runAs = defaults.RunAsUser
	}
	if *retention == 0 {
		*retention = defaults.Retention
	}
	in := instances.CreateInput{
		Name: *name, Hostname: *hostname, PBVersion: *version, Arch: *arch, Port: *port,
		BaseDir: *baseDir, DataDir: *dataDir, ServiceName: *serviceName, ServiceFile: *serviceFile,
		RunAsUser: *runAs, CreateSystemUser: createUser, AllowRootUser: *allowRoot,
		Domain: *domain, CloudflareAccountID: *cfAccount, ManageCaddy: *manageCaddy, ManageDNS: *manageDNS,
		Proxied: *proxied, SMTPProfileID: *smtpProfile, StorageProfileID: *storageProfile,
		BackupEnabled: *backup, BackupCron: *backupCron, BackupRetention: *retention, Notes: *notes, Checksum: *checksum,
	}
	inst, job, err := app.Instances.Create(ctx, in, "cli:"+currentUser())
	if err != nil {
		return fail(err)
	}
	auditCLI(ctx, app, "instance.provision_requested", "instance", inst.ID, map[string]any{"name": inst.Name, "jobId": job.ID, "version": inst.PBVersion, "port": inst.Port})
	fmt.Fprintf(os.Stderr, "Provisioning %s (port %d, version %s); job %s\n", inst.Name, inst.Port, inst.PBVersion, job.ID)
	running, err := waitJob(ctx, app, job.ID)
	if err != nil {
		return fail(err)
	}
	if running.Status != "succeeded" {
		return fail(fmt.Errorf("provisioning %s: %s", running.Status, running.LastError))
	}
	var result map[string]any
	_ = json.Unmarshal([]byte(running.Result), &result)
	fmt.Println("Instance provisioned successfully.")
	printJSON(result)
	return 0
}

// waitJob waits for an already-enqueued job, running a local worker only when
// this process can acquire the single-worker lock.
func waitJob(ctx context.Context, app *appcore.App, id string) (*store.Job, error) {
	cancelWorker, workerDone, err := maybeStartLocalWorker(ctx, app)
	if err != nil {
		return nil, err
	}
	cleanup := localWorkerCleanup(app, cancelWorker, workerDone)
	defer cleanup()
	seen := map[string]string{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		job, err := app.Store.GetJob(ctx, id)
		if err != nil {
			continue
		}
		for _, step := range job.Steps {
			if seen[step.Name] != step.Status {
				seen[step.Name] = step.Status
				fmt.Fprintf(os.Stderr, "  [%s] %s", step.Status, step.Name)
				if step.Detail != "" {
					fmt.Fprintf(os.Stderr, " - %s", step.Detail)
				}
				fmt.Fprintln(os.Stderr)
			}
		}
		switch job.Status {
		case "succeeded", "failed", "canceled":
			return job, nil
		}
	}
}

func instancesAction(action string, args []string) int {
	fs := flag.NewFlagSet("instances "+action, flag.ContinueOnError)
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" {
		return fail(errors.New("instance name is required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	job, err := runJobCLI(ctx, app, "instance."+action, instances.ActionPayload{InstanceID: inst.ID}, jobs.EnqueueOptions{
		Priority: 5, MaxAttempts: 3, CreatedBy: "cli:" + currentUser(),
	})
	if err != nil {
		return fail(err)
	}
	if job.Status != "succeeded" {
		return fail(fmt.Errorf("job %s %s: %s", job.ID, job.Status, job.LastError))
	}
	fmt.Printf("Instance %s: %s succeeded\n", inst.Name, action)
	return 0
}

func instancesUpdate(args []string) int {
	fs := flag.NewFlagSet("instances update", flag.ContinueOnError)
	version := fs.String("version", "", "target PocketBase version")
	checksum := fs.String("checksum", "", "optional expected SHA-256")
	yes := fs.Bool("yes", false, "skip interactive confirmation")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" || *version == "" {
		return fail(errors.New("usage: pbmanager instances update NAME --version VERSION"))
	}
	if !*yes && !confirmDanger("Updating will stop the instance briefly and create a safety backup. Continue?") {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	job, err := runJobCLI(ctx, app, "instance.update", instances.UpdatePayload{InstanceID: inst.ID, Version: *version, Checksum: *checksum}, jobs.EnqueueOptions{
		Priority: 10, MaxAttempts: 3, CreatedBy: "cli:" + currentUser(),
	})
	if err != nil {
		return fail(err)
	}
	if job.Status != "succeeded" {
		return fail(fmt.Errorf("update %s: %s", job.Status, job.LastError))
	}
	fmt.Printf("Instance %s updated to %s\n", inst.Name, *version)
	return 0
}

func instancesLogs(args []string) int {
	fs := flag.NewFlagSet("instances logs", flag.ContinueOnError)
	lines := fs.Int("lines", 200, "number of journal lines")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" {
		return fail(errors.New("instance name is required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	logs, err := app.Systemd.Logs(ctx, inst.ServiceName, *lines)
	if err != nil {
		return fail(err)
	}
	fmt.Print(logs)
	return 0
}

func instancesCredentials(args []string) int {
	fs := flag.NewFlagSet("instances set-credentials", flag.ContinueOnError)
	email := fs.String("email", "", "PocketBase superuser email")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" {
		return fail(errors.New("instance name is required"))
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	if *email == "" {
		*email, err = prompt("Superuser email: ", false)
		if err != nil {
			return fail(err)
		}
	}
	password, err := promptPassword("Superuser password: ")
	if err != nil {
		return fail(err)
	}
	if err := app.Instances.SetCredentials(ctx, inst.ID, instances.CredentialInput{Email: *email, Password: password}); err != nil {
		return fail(err)
	}
	fmt.Println("PocketBase management credentials verified and stored encrypted.")
	return 0
}

func instancesDelete(args []string) int {
	fs := flag.NewFlagSet("instances delete", flag.ContinueOnError)
	purge := fs.Bool("purge-data", false, "stop managing and move data to the protected purge archive")
	removeCaddy := fs.Bool("remove-caddy", false, "remove managed Caddy routes")
	removeDNS := fs.Bool("remove-dns", false, "remove managed Cloudflare records")
	yes := fs.Bool("yes", false, "skip interactive confirmation")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if name == "" {
		return fail(errors.New("instance name is required"))
	}
	if !*yes && !confirmDanger("This removes the manager inventory and managed service unit. Data is never recursively deleted. Continue?") {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	inst, err := findInstance(ctx, app, name)
	if err != nil {
		return fail(err)
	}
	if !*yes {
		typed, err := prompt(fmt.Sprintf("Type %q to confirm: ", inst.Name), false)
		if err != nil || typed != inst.Name {
			return fail(errors.New("confirmation mismatch"))
		}
	}
	job, err := runJobCLI(ctx, app, "instance.delete", instances.DeletePayload{
		InstanceID: inst.ID, Confirmation: inst.Name, PurgeData: *purge, RemoveCaddy: *removeCaddy, RemoveDNS: *removeDNS,
	}, jobs.EnqueueOptions{Priority: 20, MaxAttempts: 2, CreatedBy: "cli:" + currentUser()})
	if err != nil {
		return fail(err)
	}
	if job.Status != "succeeded" {
		return fail(fmt.Errorf("delete %s: %s", job.Status, job.LastError))
	}
	fmt.Println("Instance removed from inventory.")
	return 0
}

func confirmDanger(msg string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	answer, err := prompt(msg+" [y/N]: ", false)
	return err == nil && (strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"))
}

func currentUser() string {
	if u := os.Getenv("SUDO_USER"); u != "" {
		return u
	}
	if u, err := os.Hostname(); err == nil {
		_ = u
	}
	return "root"
}

func cmdAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager admin <list|create|reset-password|disable|enable|delete|revoke-sessions>")
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	switch args[0] {
	case "list":
		list, err := app.Auth.ListAdmins(ctx)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "USERNAME\tNAME\tROLE\tDISABLED\tLAST_LOGIN\tID")
		for _, a := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\t%s\n", a.Username, a.DisplayName, a.Role, a.Disabled, a.LastLoginAt, a.ID)
		}
		tw.Flush()
		return 0
	case "create":
		fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
		username := fs.String("username", "", "administrator username")
		display := fs.String("display-name", "", "display name")
		role := fs.String("role", "owner", "role: owner, admin, viewer")
		fromStdin := fs.Bool("password-stdin", false, "read password from stdin")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		if *username == "" {
			var err error
			*username, err = prompt("Username: ", false)
			if err != nil {
				return fail(err)
			}
		}
		var password string
		if *fromStdin {
			password, err = promptPassword("")
		} else {
			password, err = promptPassword("Password: ")
		}
		if err != nil {
			return fail(err)
		}
		a, err := app.Auth.CreateAdmin(ctx, *username, *display, password, *role)
		if err != nil {
			return fail(err)
		}
		auditCLI(ctx, app, "admin.created", "admin", a.ID, map[string]any{"username": a.Username, "role": a.Role})
		fmt.Printf("Created administrator %s (%s, role %s)\n", a.Username, a.ID, a.Role)
		return 0
	case "reset-password":
		fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
		fromStdin := fs.Bool("password-stdin", false, "read password from stdin")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		who := fs.Arg(0)
		if who == "" {
			who, err = prompt("Administrator username: ", false)
			if err != nil {
				return fail(err)
			}
		}
		a, err := app.Auth.AdminByUsername(ctx, who)
		if err != nil {
			return fail(fmt.Errorf("administrator %q not found", who))
		}
		var password string
		if *fromStdin {
			password, err = promptPassword("")
		} else {
			password, err = promptPassword("New password: ")
		}
		if err != nil {
			return fail(err)
		}
		if err := app.Auth.ResetPassword(ctx, a.ID, password); err != nil {
			return fail(err)
		}
		_ = app.Auth.RevokeAdminSessions(ctx, a.ID, "")
		auditCLI(ctx, app, "admin.password_reset", "admin", a.ID, map[string]any{"username": a.Username})
		fmt.Printf("Password reset for %s; all sessions revoked.\n", a.Username)
		return 0
	case "disable", "enable", "delete":
		if len(args) < 2 {
			return fail(errors.New("administrator username is required"))
		}
		a, err := app.Auth.AdminByUsername(ctx, args[1])
		if err != nil {
			return fail(fmt.Errorf("administrator %q not found", args[1]))
		}
		switch args[0] {
		case "disable":
			if err := app.Auth.UpdateAdmin(ctx, a.ID, a.DisplayName, a.Role, true); err != nil {
				return fail(err)
			}
			auditCLI(ctx, app, "admin.disabled", "admin", a.ID, map[string]any{"username": a.Username})
			fmt.Println("Administrator disabled; sessions revoked.")
		case "enable":
			if err := app.Auth.UpdateAdmin(ctx, a.ID, a.DisplayName, a.Role, false); err != nil {
				return fail(err)
			}
			auditCLI(ctx, app, "admin.enabled", "admin", a.ID, map[string]any{"username": a.Username})
			fmt.Println("Administrator enabled.")
		case "delete":
			if err := app.Auth.DeleteAdmin(ctx, a.ID); err != nil {
				return fail(err)
			}
			auditCLI(ctx, app, "admin.deleted", "admin", a.ID, map[string]any{"username": a.Username})
			fmt.Println("Administrator deleted.")
		}
		return 0
	case "revoke-sessions":
		if len(args) < 2 {
			return fail(errors.New("administrator username is required"))
		}
		a, err := app.Auth.AdminByUsername(ctx, args[1])
		if err != nil {
			return fail(fmt.Errorf("administrator %q not found", args[1]))
		}
		if err := app.Auth.RevokeAdminSessions(ctx, a.ID, ""); err != nil {
			return fail(err)
		}
		auditCLI(ctx, app, "admin.sessions_revoked", "admin", a.ID, map[string]any{"username": a.Username})
		fmt.Println("All sessions revoked.")
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown admin subcommand %q\n", args[0])
	return 2
}

func cmdConfig(args []string) int {
	if len(args) == 0 || args[0] == "show" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cfg, err := loadConfig()
		if err != nil {
			return fail(err)
		}
		app, err := appcore.Open(ctx, cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
		if err != nil {
			return fail(err)
		}
		defer app.Close()
		printJSON(map[string]any{"bootstrap": app.Settings.Bootstrap(), "server": app.Settings.View()})
		return 0
	}
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("config init", flag.ContinueOnError)
		path := fs.String("path", configPath, "config path")
		dataDir := fs.String("data-dir", config.DefaultDataDir, "application data directory")
		listen := fs.String("listen", config.DefaultListen, "listen address")
		publicURL := fs.String("public-url", "", "public dashboard URL")
		secure := fs.Bool("secure-cookies", true, "set Secure cookies")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		if err := requireRoot(); err != nil {
			return fail(err)
		}
		if _, err := os.Stat(*path); err == nil {
			return fail(fmt.Errorf("%s already exists; refusing to overwrite", *path))
		}
		cfg := config.Default()
		cfg.DataDir = *dataDir
		cfg.DBPath = filepath.Join(*dataDir, "manager.db")
		cfg.ListenAddr = *listen
		if *publicURL != "" {
			cfg.PublicURL = *publicURL
		}
		cfg.SecureCookies = *secure
		if err := cfg.Normalize(); err != nil {
			return fail(err)
		}
		if err := config.Write(*path, cfg); err != nil {
			return fail(err)
		}
		fmt.Printf("Wrote %s\n", *path)
		return 0
	case "set":
		fs := flag.NewFlagSet("config set", flag.ContinueOnError)
		path := fs.String("path", configPath, "config path")
		publicURL := fs.String("public-url", "", "public dashboard URL")
		secure := fs.String("secure-cookies", "", "true or false")
		listen := fs.String("listen", "", "listen address")
		logLevel := fs.String("log-level", "", "debug, info, warn, error")
		sessionHours := fs.Int("session-hours", 0, "session lifetime in hours (1-168)")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		if err := requireRoot(); err != nil {
			return fail(err)
		}
		cfg, err := config.Load(*path)
		if err != nil {
			return fail(err)
		}
		if *publicURL != "" {
			cfg.PublicURL = *publicURL
		}
		if *secure != "" {
			v, err := strconv.ParseBool(*secure)
			if err != nil {
				return fail(errors.New("--secure-cookies must be true or false"))
			}
			cfg.SecureCookies = v
		}
		if *listen != "" {
			cfg.ListenAddr = *listen
		}
		if *logLevel != "" {
			cfg.LogLevel = *logLevel
		}
		if *sessionHours != 0 {
			cfg.SessionHours = *sessionHours
		}
		if err := config.Write(*path, cfg); err != nil {
			return fail(err)
		}
		reloaded, _ := config.Load(*path)
		printJSON(map[string]any{
			"publicUrl": reloaded.PublicURL, "secureCookies": reloaded.SecureCookies,
			"listenAddr": reloaded.ListenAddr, "restartRequired": true,
		})
		return 0
	case "validate":
		if err := requireRoot(); err != nil {
			return fail(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		app, err := openApp(ctx)
		if err != nil {
			return fail(err)
		}
		defer app.Close()
		diag := app.Diagnose(ctx)
		printJSON(diag)
		if len(diag.Errors) > 0 {
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand %q\n", args[0])
		return 2
	}
}

func cmdJobs(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager jobs <list|inspect|retry|cancel>")
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("jobs list", flag.ContinueOnError)
		status := fs.String("status", "", "filter status")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		list, _, err := app.Store.ListJobs(ctx, *status, "", "", 100, 0)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTYPE\tSTATUS\tPROGRESS\tATTEMPTS\tCREATED\tERROR")
		for _, j := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d%%\t%d/%d\t%s\t%s\n", j.ID, j.Type, j.Status, j.Progress, j.Attempts, j.MaxAttempts, j.CreatedAt, truncate(j.LastError, 40))
		}
		tw.Flush()
		return 0
	case "inspect":
		if len(args) < 2 {
			return fail(errors.New("job ID is required"))
		}
		job, err := app.Store.GetJob(ctx, args[1])
		if err != nil {
			return fail(err)
		}
		printJSON(job)
		return 0
	case "retry":
		if len(args) < 2 {
			return fail(errors.New("job ID is required"))
		}
		if err := app.Jobs.Retry(ctx, args[1]); err != nil {
			return fail(err)
		}
		fmt.Println("Job queued for retry.")
		return 0
	case "cancel":
		if len(args) < 2 {
			return fail(errors.New("job ID is required"))
		}
		if err := app.Jobs.Cancel(ctx, args[1]); err != nil {
			return fail(err)
		}
		fmt.Println("Cancellation requested.")
		return 0
	}
	return 2
}

func cmdBackup(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager backup <create|list|verify|restore>")
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	yes := false
	raw := args
	args = []string{raw[0]}
	for _, arg := range raw[1:] {
		if arg == "--yes" {
			yes = true
			continue
		}
		args = append(args, arg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	switch args[0] {
	case "create":
		if len(args) < 2 {
			return fail(errors.New("instance name is required"))
		}
		inst, err := findInstance(ctx, app, args[1])
		if err != nil {
			return fail(err)
		}
		job, err := runJobCLI(ctx, app, "backup.create", map[string]any{"instanceId": inst.ID, "automatic": false, "note": "CLI backup"}, jobs.EnqueueOptions{Priority: 4, MaxAttempts: 3, CreatedBy: "cli:" + currentUser()})
		if err != nil {
			return fail(err)
		}
		if job.Status != "succeeded" {
			return fail(fmt.Errorf("backup %s: %s", job.Status, job.LastError))
		}
		var result map[string]any
		_ = json.Unmarshal([]byte(job.Result), &result)
		printJSON(result)
		return 0
	case "list":
		instanceID := ""
		if len(args) > 1 {
			inst, err := findInstance(ctx, app, args[1])
			if err != nil {
				return fail(err)
			}
			instanceID = inst.ID
		}
		list, err := app.Store.ListBackups(ctx, instanceID, 100)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tINSTANCE\tMETHOD\tSTATUS\tSIZE\tCREATED\tVERIFIED")
		for _, b := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", b.ID, b.InstanceName, b.Method, b.Status, b.Size, b.CreatedAt, b.VerifiedAt)
		}
		tw.Flush()
		return 0
	case "verify":
		if len(args) < 2 {
			return fail(errors.New("backup ID is required"))
		}
		job, err := runJobCLI(ctx, app, "backup.verify", map[string]string{"backupId": args[1]}, jobs.EnqueueOptions{Priority: 2, MaxAttempts: 2, CreatedBy: "cli:" + currentUser()})
		if err != nil {
			return fail(err)
		}
		if job.Status != "succeeded" {
			return fail(fmt.Errorf("verify %s: %s", job.Status, job.LastError))
		}
		fmt.Println("Backup verified.")
		return 0
	case "restore":
		if len(args) < 2 {
			return fail(errors.New("backup ID is required"))
		}
		b, err := app.Store.GetBackup(ctx, args[1])
		if err != nil {
			return fail(err)
		}
		inst, err := app.Store.GetInstance(ctx, b.InstanceID)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("Restoring %s to %s will stop the instance and create a safety backup.\n", b.ID, inst.Name)
		if !yes && !confirmDanger("Continue with restore?") {
			return 1
		}
		if !yes {
			typed, err := prompt(fmt.Sprintf("Type %q to confirm: ", inst.Name), false)
			if err != nil || typed != inst.Name {
				return fail(errors.New("confirmation mismatch"))
			}
		}
		job, err := runJobCLI(ctx, app, "backup.restore", map[string]string{"backupId": b.ID, "confirmation": inst.Name}, jobs.EnqueueOptions{Priority: 30, MaxAttempts: 1, CreatedBy: "cli:" + currentUser()})
		if err != nil {
			return fail(err)
		}
		if job.Status != "succeeded" {
			return fail(fmt.Errorf("restore %s: %s", job.Status, job.LastError))
		}
		fmt.Println("Restore completed.")
		return 0
	}
	return 2
}

func cmdCaddy(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager caddy <status|validate|ensure-import|route|logs>")
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := openApp(ctx)
	if err != nil {
		return fail(err)
	}
	defer app.Close()
	switch args[0] {
	case "status":
		view, err := app.Caddy.ReadConfig(ctx)
		if err != nil {
			printJSON(app.Caddy.Status(ctx))
			return 1
		}
		printJSON(view)
		return 0
	case "validate":
		if err := app.Caddy.Validate(ctx); err != nil {
			return fail(err)
		}
		fmt.Println("Caddyfile is valid.")
		return 0
	case "ensure-import":
		added, err := app.Caddy.EnsureImport(ctx)
		if err != nil {
			return fail(err)
		}
		if added {
			fmt.Println("Managed import added; Caddy validated and reloaded.")
		} else {
			fmt.Println("Managed import already present.")
		}
		return 0
	case "route":
		fs := flag.NewFlagSet("caddy route", flag.ContinueOnError)
		domain := fs.String("domain", "", "public hostname")
		upstream := fs.String("upstream", "127.0.0.1:8787", "loopback upstream host:port")
		name := fs.String("name", "manager", "managed route name")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		if *domain == "" {
			return fail(errors.New("--domain is required"))
		}
		host, portStr, err := net.SplitHostPort(*upstream)
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			return fail(errors.New("upstream must be a loopback host:port"))
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return fail(errors.New("invalid upstream port"))
		}
		inst := &store.Instance{ID: "caddy-managed-" + *name, Name: *name, Port: port}
		path, err := app.Caddy.ApplyRoute(ctx, inst, *domain)
		if err != nil {
			return fail(err)
		}
		fmt.Println("Managed route written to", path)
		return 0
	case "logs":
		fs := flag.NewFlagSet("caddy logs", flag.ContinueOnError)
		lines := fs.Int("lines", 200, "number of journal lines")
		if err := fs.Parse(parseArgs(fs, args[1:])); err != nil {
			return 2
		}
		logs, err := app.Caddy.Logs(ctx, *lines)
		if err != nil {
			return fail(err)
		}
		fmt.Print(logs)
		return 0
	}
	return 2
}

func cmdDiagnose(args []string) int {
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	app, err := appcore.Open(ctx, cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		fmt.Println("Manager database could not be opened:", err)
		fmt.Println("Use 'pbmanager recover' or 'pbmanager db restore' with a known-good snapshot.")
		return 1
	}
	defer app.Close()
	diag := app.Diagnose(ctx)
	printJSON(diag)
	if len(diag.Errors) > 0 {
		return 1
	}
	return 0
}

func cmdRecover(args []string) int {
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	fixPerms := fs.Bool("fix-permissions", false, "repair restrictive permissions on data, key, and database")
	createAdmin := fs.Bool("create-admin", false, "interactively create an administrator")
	resetUser := fs.String("reset-password", "", "reset a password for this username")
	revoke := fs.String("revoke-sessions", "", "revoke all sessions for this username")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if *fixPerms {
		if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "warning: chmod data dir:", err)
		}
		if err := os.Chmod(filepath.Join(cfg.DataDir, "secret.key"), 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "warning: chmod secret key:", err)
		}
		if err := os.Chmod(cfg.DBPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "warning: chmod database:", err)
		}
		fmt.Println("Permissions repaired.")
	}
	app, err := appcore.Open(ctx, cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		fmt.Println("Manager database could not be opened:", err)
		fmt.Println("If corruption is suspected: stop the service, run 'pbmanager db check', then restore the latest snapshot with 'pbmanager db restore FILE'.")
		return 1
	}
	defer app.Close()
	diag := app.Diagnose(ctx)
	printJSON(diag)
	if *createAdmin || diag.AdminCount == 0 {
		fmt.Println("No enabled administrators exist. Creating a recovery administrator.")
		username, err := prompt("Username: ", false)
		if err != nil {
			return fail(err)
		}
		password, err := promptPassword("Password: ")
		if err != nil {
			return fail(err)
		}
		a, err := app.Auth.CreateAdmin(ctx, username, "Recovery Administrator", password, "owner")
		if err != nil {
			return fail(err)
		}
		auditCLI(ctx, app, "admin.recovery_created", "admin", a.ID, map[string]any{"username": a.Username})
		fmt.Printf("Created owner administrator %s (%s)\n", a.Username, a.ID)
	}
	if *resetUser != "" {
		a, err := app.Auth.AdminByUsername(ctx, *resetUser)
		if err != nil {
			return fail(fmt.Errorf("administrator %q not found", *resetUser))
		}
		password, err := promptPassword("New password: ")
		if err != nil {
			return fail(err)
		}
		if err := app.Auth.ResetPassword(ctx, a.ID, password); err != nil {
			return fail(err)
		}
		_ = app.Auth.RevokeAdminSessions(ctx, a.ID, "")
		auditCLI(ctx, app, "admin.recovery_password_reset", "admin", a.ID, map[string]any{"username": a.Username})
		fmt.Println("Password reset and sessions revoked.")
	}
	if *revoke != "" {
		a, err := app.Auth.AdminByUsername(ctx, *revoke)
		if err != nil {
			return fail(fmt.Errorf("administrator %q not found", *revoke))
		}
		if err := app.Auth.RevokeAdminSessions(ctx, a.ID, ""); err != nil {
			return fail(err)
		}
		fmt.Println("Sessions revoked.")
	}
	fmt.Println("Recovery checks complete. The web service is not required for these operations.")
	return 0
}

func cmdDB(args []string) int {
	if len(args) == 0 {
		fmt.Println("usage: pbmanager db <check|snapshot|restore>")
		return 2
	}
	yes := false
	var positional []string
	for _, arg := range args {
		if arg == "--yes" {
			yes = true
			continue
		}
		positional = append(positional, arg)
	}
	args = positional
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	switch args[0] {
	case "check":
		st, err := store.Open(ctx, cfg.DBPath)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		result, err := st.QuickCheck(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Println("SQLite integrity:", result)
		versions, _ := st.MigrateStatus(ctx)
		fmt.Println("Applied migrations:", versions)
		return 0
	case "snapshot":
		if len(args) < 2 {
			return fail(errors.New("snapshot destination is required"))
		}
		st, err := store.Open(ctx, cfg.DBPath)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		if err := st.Snapshot(ctx, args[1]); err != nil {
			return fail(err)
		}
		fmt.Println("Snapshot written to", args[1])
		return 0
	case "restore":
		if len(args) < 2 {
			return fail(errors.New("snapshot path is required"))
		}
		if !yes && !confirmDanger("Restoring the manager database requires the web service to be stopped. Continue?") {
			return 1
		}
		if out, _, err := (platform.ExecRunner{}).Run(ctx, "systemctl", "is-active", "pbmanager"); err == nil && strings.TrimSpace(string(out)) == "active" {
			return fail(errors.New("pbmanager service is still active; stop it before restoring the database"))
		}
		src := args[1]
		if _, err := os.Stat(src); err != nil {
			return fail(err)
		}
		check, err := os.CreateTemp("", "pbmanager-restore-check-*.db")
		if err != nil {
			return fail(err)
		}
		checkPath := check.Name()
		_ = check.Close()
		defer os.Remove(checkPath)
		if err := platform.CopyFile(src, checkPath); err != nil {
			return fail(err)
		}
		st, err := store.Open(ctx, checkPath)
		if err != nil {
			return fail(fmt.Errorf("snapshot is not a valid manager database: %w", err))
		}
		if _, err := st.QuickCheck(ctx); err != nil {
			st.Close()
			return fail(err)
		}
		st.Close()
		stamp := time.Now().UTC().Format("20060102T150405Z")
		oldBase := cfg.DBPath + ".before-restore-" + stamp
		if err := os.Rename(cfg.DBPath, oldBase); err != nil {
			return fail(err)
		}
		oldSidecars := []string{oldBase + "-wal", oldBase + "-shm"}
		for i, suffix := range []string{"-wal", "-shm"} {
			if err := os.Rename(cfg.DBPath+suffix, oldSidecars[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = os.Rename(oldBase, cfg.DBPath)
				return fail(err)
			}
		}
		if err := platform.CopyFile(src, cfg.DBPath); err != nil {
			_ = os.Rename(oldBase, cfg.DBPath)
			return fail(err)
		}
		_ = os.Chmod(cfg.DBPath, 0o600)
		if restored, err := store.Open(ctx, cfg.DBPath); err == nil {
			_ = restored.CreateAudit(ctx, &store.AuditEntry{
				ActorType: "cli-root", ActorName: currentUser(), Action: "db.restored",
				TargetType: "database", TargetID: "manager", Detail: fmt.Sprintf(`{"snapshot":%q,"previous":%q}`, src, oldBase),
			})
			restored.Close()
		}
		fmt.Println("Database restored. Previous database retained at", oldBase)
		return 0
	}
	return 2
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ = auth.ValidRole
var _ = cron.Validate
var _ = sort.Strings
