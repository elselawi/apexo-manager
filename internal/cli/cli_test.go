package cli

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/apexo/pbmanager/internal/config"
)

func TestParseArgsAllowsFlagsAnywhere(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	version := fs.String("version", "", "")
	force := fs.Bool("force", false, "")
	args := parseArgs(fs, []string{"clinic-a", "--version", "0.23.6", "--force", "extra"})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if *version != "0.23.6" || !*force {
		t.Fatalf("flags not parsed: version=%q force=%v args=%v", *version, *force, fs.Args())
	}
	if len(fs.Args()) != 2 || fs.Arg(0) != "clinic-a" || fs.Arg(1) != "extra" {
		t.Fatalf("positionals: %#v", fs.Args())
	}
}

func TestParseArgsEqualsForm(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	name := fs.String("name", "", "")
	if err := fs.Parse(parseArgs(fs, []string{"pos", "--name=value"})); err != nil {
		t.Fatal(err)
	}
	if *name != "value" || fs.Arg(0) != "pos" {
		t.Fatalf("name=%q args=%v", *name, fs.Args())
	}
}

func TestRootEnforcementUsesEffectiveUID(t *testing.T) {
	old := geteuid
	defer func() { geteuid = old }()
	geteuid = func() int { return 1000 }
	if err := requireRoot(); err == nil {
		t.Fatal("non-root uid accepted")
	}
	geteuid = func() int { return 0 }
	if err := requireRoot(); err != nil {
		t.Fatalf("root uid rejected: %v", err)
	}
}

func TestConfigSetUpdatesBootstrap(t *testing.T) {
	oldEU, oldPath := geteuid, configPath
	defer func() { geteuid, configPath = oldEU, oldPath }()
	geteuid = func() int { return 0 }
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "manager.db")
	cfg.PublicURL = "http://127.0.0.1:8787"
	if err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	configPath = path
	if code := cmdConfig([]string{"set", "--public-url", "https://manager.example.com", "--secure-cookies=true", "--listen", "127.0.0.1:8788"}); code != 0 {
		t.Fatalf("config set exit %d", code)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicURL != "https://manager.example.com" || !got.SecureCookies || got.ListenAddr != "127.0.0.1:8788" {
		t.Fatalf("config not updated: %#v", got)
	}
	if got.DataDir != dir || got.DBPath != filepath.Join(dir, "manager.db") {
		t.Fatalf("bootstrap paths changed: %#v", got)
	}
}
