package caddy

import (
	"strings"
	"testing"
)

func TestParseSitesAndUpstreams(t *testing.T) {
	config := `{
	email admin@example.com
}
# existing site
clinic-a.example.com, www.clinic-a.example.com {
	encode gzip
	reverse_proxy 127.0.0.1:8090
}
https://clinic-b.example.com {
	handle /api/* {
		reverse_proxy localhost:8091
	}
}
import /etc/caddy/pbmanager.d/*.caddy
`
	parsed := Parse(config)
	if len(parsed.Sites) != 2 {
		t.Fatalf("sites = %#v", parsed.Sites)
	}
	first := parsed.Sites[0]
	if len(first.Addresses) != 2 || first.Addresses[0] != "clinic-a.example.com" {
		t.Fatalf("addresses = %#v", first.Addresses)
	}
	if len(first.Upstreams) != 1 || first.Upstreams[0] != "127.0.0.1:8090" {
		t.Fatalf("upstreams = %#v", first.Upstreams)
	}
	second := parsed.Sites[1]
	if len(second.Upstreams) != 1 || second.Upstreams[0] != "localhost:8091" {
		t.Fatalf("nested upstreams = %#v", second.Upstreams)
	}
	if len(parsed.Imports) != 1 {
		t.Fatalf("imports = %#v", parsed.Imports)
	}
}

func TestParseManagedMarkerAndOneLine(t *testing.T) {
	config := `example.com { reverse_proxy 127.0.0.1:8090 }
# managed by pbmanager
managed.example.com {
	reverse_proxy 127.0.0.1:8091
}
`
	parsed := Parse(config)
	if len(parsed.Sites) != 2 {
		t.Fatalf("sites = %#v", parsed.Sites)
	}
	if parsed.Sites[1].Managed != true {
		t.Fatalf("managed marker not detected: %#v", parsed.Sites[1])
	}
}

func TestImportHelpers(t *testing.T) {
	base := "{\n}\n\nexample.com {\n}\n"
	dir := "/etc/caddy/pbmanager.d"
	if HasImport(base, dir) {
		t.Fatal("unexpected import")
	}
	updated, added := AddImport(base, dir)
	if !added || !HasImport(updated, dir) {
		t.Fatalf("import not added: %q", updated)
	}
	if again, addedAgain := AddImport(updated, dir); addedAgain || again != updated {
		t.Fatal("import added twice")
	}
	replaced := ReplaceImportDir(updated, dir, "/tmp/staging")
	if strings.Contains(replaced, dir) || !strings.Contains(replaced, "/tmp/staging") {
		t.Fatalf("replacement failed: %q", replaced)
	}
}

func TestSnippetAndDomainValidation(t *testing.T) {
	snippet := Snippet("inst-1", "clinic-a", "clinic-a.example.com", 8090)
	if !strings.Contains(snippet, "managed by pbmanager") || !strings.Contains(snippet, "reverse_proxy 127.0.0.1:8090") {
		t.Fatalf("snippet = %q", snippet)
	}
	for _, valid := range []string{"example.com", "clinic-a.example.com"} {
		if !ValidDomain(valid) {
			t.Fatalf("rejected valid domain %q", valid)
		}
	}
	for _, invalid := range []string{"", "localhost", "bad domain.com", "http://example.com", "../example.com", "-bad.com"} {
		if ValidDomain(invalid) {
			t.Fatalf("accepted invalid domain %q", invalid)
		}
	}
}
