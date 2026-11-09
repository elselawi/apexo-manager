package instances

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/services/pocketbase"
)

// ApplicationConfig is a secret-free snapshot of the live PocketBase settings
// plus the native backup inventory. Passwords and storage secrets are never
// returned.
type ApplicationConfig struct {
	SMTP      SMTPConfig              `json:"smtp"`
	S3        S3Config                `json:"s3"`
	Backups   []pocketbase.BackupInfo `json:"backups"`
	FetchedAt string                  `json:"fetchedAt"`
}

type SMTPConfig struct {
	Enabled            bool   `json:"enabled"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	AuthMethod         string `json:"authMethod"`
	TLS                bool   `json:"tls"`
	LocalName          string `json:"localName"`
	FromAddress        string `json:"fromAddress"`
	FromName           string `json:"fromName"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

type S3Config struct {
	Enabled          bool   `json:"enabled"`
	Endpoint         string `json:"endpoint"`
	Bucket           string `json:"bucket"`
	Region           string `json:"region"`
	AccessKey        string `json:"accessKey"`
	ForcePathStyle   bool   `json:"forcePathStyle"`
	SecretConfigured bool   `json:"secretConfigured"`
}

// ApplicationConfig reads the current PocketBase settings with the linked
// superuser. This is read-only discovery; applying a profile remains explicit.
func (s *Service) ApplicationConfig(ctx context.Context, id string) (*ApplicationConfig, error) {
	inst, err := s.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := s.pbAuth(ctx, inst)
	if err != nil {
		return nil, err
	}
	settings, err := client.GetSettings(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("read PocketBase settings: %w", err)
	}
	backups, backupErr := client.ListBackups(ctx, token)
	if backupErr != nil {
		// Settings are still useful if the backup endpoint is unavailable.
		backups = nil
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].Modified > backups[j].Modified })
	if len(backups) > 100 {
		backups = backups[:100]
	}
	out := &ApplicationConfig{
		SMTP:      smtpConfig(pocketbase.SettingsSMTP(settings)),
		S3:        s3Config(pocketbase.SettingsS3(settings)),
		Backups:   backups,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if out.Backups == nil {
		out.Backups = []pocketbase.BackupInfo{}
	}
	return out, nil
}

func smtpConfig(m map[string]any) SMTPConfig {
	return SMTPConfig{
		Enabled:            boolValue(m["enabled"]),
		Host:               stringValue(m["host"]),
		Port:               intValue(m["port"]),
		Username:           stringValue(m["username"]),
		AuthMethod:         stringValue(m["authMethod"]),
		TLS:                boolValue(m["tls"]),
		LocalName:          stringValue(m["localName"]),
		FromAddress:        stringValue(m["fromAddress"]),
		FromName:           stringValue(m["fromName"]),
		PasswordConfigured: stringValue(m["password"]) != "",
	}
}

func s3Config(m map[string]any) S3Config {
	return S3Config{
		Enabled:          boolValue(m["enabled"]),
		Endpoint:         stringValue(m["endpoint"]),
		Bucket:           stringValue(m["bucket"]),
		Region:           stringValue(m["region"]),
		AccessKey:        stringValue(m["accessKey"]),
		ForcePathStyle:   boolValue(m["forcePathStyle"]),
		SecretConfigured: stringValue(m["secret"]) != "",
	}
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func intValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var n int
		_, _ = fmt.Sscan(strings.TrimSpace(x), &n)
		return n
	default:
		return 0
	}
}
