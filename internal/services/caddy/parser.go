// Package caddy safely manages managed Caddyfile snippets without rewriting
// unrelated configuration.
package caddy

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

// Site is a parsed Caddy site block.
type Site struct {
	Addresses []string `json:"addresses"`
	Upstreams []string `json:"upstreams"`
	Managed   bool     `json:"managed"`
	Line      int      `json:"line"`
}

// ParsedConfig is a read-only view of a Caddyfile.
type ParsedConfig struct {
	Sites   []Site
	Imports []string
}

// Parse extracts top-level site blocks and reverse_proxy upstreams. It is
// intentionally conservative: it never needs to understand every directive,
// only enough to display and detect conflicts safely.
func Parse(content string) ParsedConfig {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	clean := make([]string, len(lines))
	rawManaged := make([]bool, len(lines))
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "managed by pbmanager") {
			rawManaged[i] = true
		}
		clean[i] = strings.TrimSpace(stripComment(line))
	}

	var out ParsedConfig
	var current *Site
	var body []string
	depth := 0
	var pendingLabels string
	inGlobal := false

	finish := func() {
		if current == nil {
			return
		}
		bodyText := strings.Join(body, "\n")
		current.Addresses = normalizeAddresses(current.Addresses)
		current.Upstreams = extractUpstreams(bodyText)
		if len(current.Addresses) > 0 {
			out.Sites = append(out.Sites, *current)
		}
		current = nil
		body = nil
	}

	for i, line := range clean {
		if line == "" {
			continue
		}
		if depth == 0 {
			if strings.HasPrefix(line, "import ") {
				fields := fieldsOutsideQuotes(line)
				if len(fields) >= 2 {
					out.Imports = append(out.Imports, strings.Trim(fields[1], `"`))
				}
				pendingLabels = ""
				continue
			}
			open := indexUnquoted(line, '{')
			if open < 0 {
				if strings.HasSuffix(line, ",") || (pendingLabels != "" && !strings.Contains(line, " ")) {
					pendingLabels = strings.TrimSpace(pendingLabels + " " + line)
				}
				continue
			}
			prefix := strings.TrimSpace(pendingLabels + " " + line[:open])
			pendingLabels = ""
			if prefix == "" {
				inGlobal = true
				depth = braceDelta(line[open:])
				continue
			}
			if strings.HasPrefix(prefix, "(") {
				// Top-level snippet definition; not a site.
				current = nil
				inGlobal = true
				depth = braceDelta(line[open:])
				continue
			}
			current = &Site{Addresses: splitAddresses(prefix), Line: i + 1, Managed: managedMarkerBefore(clean, rawManaged, i)}
			body = append(body, line[open+1:])
			depth = braceDelta(line[open:])
			if depth <= 0 {
				finish()
				depth = 0
			}
			continue
		}
		// Inside a block (site, global, or snippet).
		if current != nil {
			body = append(body, line)
		}
		depth += braceDelta(line)
		if depth <= 0 {
			depth = 0
			if current != nil {
				finish()
			}
			inGlobal = false
		}
	}
	if current != nil {
		finish()
	}
	_ = inGlobal
	return out
}

func managedMarkerBefore(clean []string, rawManaged []bool, siteIndex int) bool {
	for i := siteIndex - 1; i >= 0; i-- {
		if i < len(rawManaged) && rawManaged[i] {
			return true
		}
		if strings.TrimSpace(clean[i]) != "" {
			return false
		}
	}
	return false
}

func stripComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			quote = r
			continue
		}
		if r == '#' {
			return line[:i]
		}
	}
	return line
}

func indexUnquoted(s string, want rune) int {
	var quote rune
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			quote = r
			continue
		}
		if r == want {
			return i
		}
	}
	return -1
}

func braceDelta(s string) int {
	var quote rune
	delta := 0
	for _, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			quote = r
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta
}

func fieldsOutsideQuotes(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch {
		case r == '"' || r == '\'' || r == '`':
			quote = r
			b.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func splitAddresses(prefix string) []string {
	var out []string
	for _, part := range strings.Split(prefix, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeAddresses(in []string) []string {
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(strings.Trim(a, `"'`))
		if a == "" {
			continue
		}
		// Keep URLs for matching but strip path.
		if strings.Contains(a, "://") {
			if u, err := url.Parse(a); err == nil && u.Host != "" {
				a = u.Host
			}
		}
		a = strings.TrimSuffix(a, "/")
		if strings.HasPrefix(a, ":") || strings.Contains(a, "{") {
			continue
		}
		if host, _, err := net.SplitHostPort(a); err == nil {
			a = host
		}
		a = strings.TrimPrefix(strings.ToLower(a), "www.")
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}

func extractUpstreams(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(body, "\n") {
		fields := fieldsOutsideQuotes(strings.TrimSpace(stripComment(raw)))
		if len(fields) < 2 {
			continue
		}
		if fields[0] != "reverse_proxy" && fields[0] != "to" {
			continue
		}
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "{") || f == "{" {
				break
			}
			f = strings.Trim(f, `"'`)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// ImportLine returns the marker import directive for a snippet directory.
func ImportLine(snippetDir string) string {
	return `import "` + strings.ReplaceAll(snippetDir, `"`, "") + `/*.caddy"`
}

// HasImport reports whether the Caddyfile imports the managed directory.
func HasImport(content, snippetDir string) bool {
	needle := strings.ReplaceAll(snippetDir, `"`, "")
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if !strings.HasPrefix(line, "import ") {
			continue
		}
		line = strings.ReplaceAll(line, `"`, "")
		line = strings.ReplaceAll(line, " ", "")
		if strings.Contains(line, needle+"/*") {
			return true
		}
	}
	return false
}

// AddImport appends a clearly marked import line if it is not already present.
func AddImport(content, snippetDir string) (string, bool) {
	if HasImport(content, snippetDir) {
		return content, false
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n# BEGIN pbmanager managed sites (do not remove)\n" + ImportLine(snippetDir) + "\n# END pbmanager managed sites\n", true
}

// ReplaceImportDir rewrites an existing managed import line to point at a
// different directory. It is used to validate staged snippets safely.
func ReplaceImportDir(content, oldDir, newDir string) string {
	needle := strings.ReplaceAll(oldDir, `"`, "")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if !strings.HasPrefix(trimmed, "import ") {
			continue
		}
		normalized := strings.ReplaceAll(trimmed, `"`, "")
		normalized = strings.ReplaceAll(normalized, " ", "")
		if !strings.Contains(normalized, needle+"/*") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + ImportLine(newDir)
	}
	return strings.Join(lines, "\n")
}

// Snippet renders a managed route.
func Snippet(instanceID, instanceName, domain string, port int) string {
	var b strings.Builder
	b.WriteString("# managed by pbmanager - do not edit by hand\n")
	b.WriteString("# instance: " + instanceID + "\n")
	b.WriteString("# instance-name: " + instanceName + "\n")
	b.WriteString("# domain: " + domain + "\n")
	b.WriteString(domain + " {\n")
	b.WriteString(fmt.Sprintf("\treverse_proxy 127.0.0.1:%d\n", port))
	b.WriteString("\tencode zstd gzip\n")
	b.WriteString("}\n")
	return b.String()
}

// ValidDomain validates a DNS hostname suitable for a managed site.
func ValidDomain(s string) bool {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if len(s) < 3 || len(s) > 253 || strings.ContainsAny(s, `/\:*?"<>|@ `) {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) < 1 || len(l) > 63 || strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
