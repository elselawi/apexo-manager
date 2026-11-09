package cli

import (
	"flag"
	"testing"
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
