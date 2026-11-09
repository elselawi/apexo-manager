package systemd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/apexo/pbmanager/internal/platform"
)

// UnitSpec is the validated, fixed input for a generated service unit.
type UnitSpec struct {
	InstanceID string
	Name       string
	Binary     string
	BaseDir    string
	DataDir    string
	Port       int
	User       string
	Version    string
}

// RenderUnit produces a hardened PocketBase unit. All values are validated;
// paths containing control characters, quotes, or systemd specifiers are
// rejected rather than escaped ambiguously.
func RenderUnit(spec UnitSpec) ([]byte, error) {
	if !platform.ValidSafeName(spec.Name) {
		return nil, fmt.Errorf("invalid instance name")
	}
	for name, p := range map[string]string{"binary": spec.Binary, "base_dir": spec.BaseDir, "data_dir": spec.DataDir} {
		if err := platform.ValidateAbsolutePath(p, false); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if strings.ContainsAny(p, "\"%\n\r") {
			return nil, fmt.Errorf("%s contains unsupported characters", name)
		}
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return nil, fmt.Errorf("invalid port")
	}
	if spec.User != "" && !platform.ValidSafeName(spec.User) {
		return nil, fmt.Errorf("invalid run-as user")
	}
	protectHome := true
	if platform.IsWithin("/home", spec.BaseDir) || platform.IsWithin("/root", spec.BaseDir) {
		protectHome = false
	}
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=PocketBase instance " + spec.Name + "\n")
	b.WriteString("Documentation=https://pocketbase.io/docs/\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("StartLimitIntervalSec=60\n")
	b.WriteString("StartLimitBurst=5\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if spec.User != "" {
		b.WriteString("User=" + spec.User + "\n")
		b.WriteString("Group=" + spec.User + "\n")
	}
	b.WriteString("WorkingDirectory=" + quoteUnitPath(spec.BaseDir) + "\n")
	b.WriteString("ExecStart=" + quoteUnitPath(spec.Binary) + " serve --http=127.0.0.1:" + fmt.Sprint(spec.Port) + " --dir=" + quoteUnitPath(spec.DataDir) + "\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("TimeoutStopSec=20\n")
	b.WriteString("KillSignal=SIGTERM\n")
	b.WriteString("UMask=0077\n")
	b.WriteString("LimitNOFILE=8192\n")
	b.WriteString("NoNewPrivileges=true\n")
	b.WriteString("PrivateTmp=true\n")
	b.WriteString("ProtectSystem=strict\n")
	if protectHome {
		b.WriteString("ProtectHome=true\n")
	}
	b.WriteString("ProtectKernelTunables=true\n")
	b.WriteString("ProtectKernelModules=true\n")
	b.WriteString("ProtectControlGroups=true\n")
	b.WriteString("RestrictSUIDSGID=true\n")
	b.WriteString("LockPersonality=true\n")
	b.WriteString("RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX\n")
	rw := []string{spec.BaseDir}
	if !platform.IsWithin(spec.BaseDir, spec.DataDir) {
		rw = append(rw, spec.DataDir)
	}
	var quoted []string
	for _, p := range rw {
		quoted = append(quoted, quoteUnitPath(p))
	}
	b.WriteString("ReadWritePaths=" + strings.Join(quoted, " ") + "\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	b.WriteString("\n")
	b.WriteString(UnitMarker(spec.InstanceID) + "\n")
	b.WriteString("# version: " + spec.Version + "\n")
	return []byte(b.String()), nil
}

func quoteUnitPath(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

// SnippetName returns a stable snippet filename for a domain.
func SnippetName(domain string) string {
	return strings.ReplaceAll(strings.ToLower(domain), "*", "_wildcard") + ".caddy"
}

// UnitDirectory returns the directory containing a unit file.
func UnitDirectory(path string) string { return filepath.Dir(path) }
