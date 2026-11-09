package pbadmin

import (
	"testing"
)

func validSettings() Settings {
	return Settings{
		Meta:    AppMeta{AppName: "App", AppURL: "https://example.com"},
		SMTP:    SMTP{Port: 587},
		S3:      S3{},
		Backups: BackupSettings{MaxKeep: 3},
		Logs:    LogSettings{MaxDays: 5},
		Batch:   BatchSettings{MaxRequests: 50, Timeout: 3},
	}
}

func TestValidateSettings(t *testing.T) {
	if err := validateSettings(validSettings()); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Settings)
	}{
		{"smtp without host", func(s *Settings) { s.SMTP.Enabled = true }},
		{"s3 without bucket", func(s *Settings) { s.S3.Enabled = true }},
		{"backup s3 without bucket", func(s *Settings) { s.Backups.S3.Enabled = true }},
		{"bad cron", func(s *Settings) { s.Backups.Cron = "not a cron" }},
		{"bad rate rule", func(s *Settings) { s.RateLimits.Rules = []RateLimitRule{{Label: "x"}} }},
		{"bad retention", func(s *Settings) { s.Backups.MaxKeep = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSettings()
			tc.edit(&s)
			if err := validateSettings(s); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNormalizeUserInput(t *testing.T) {
	create := map[string]interface{}{"email": "user@example.com"}
	if err := normalizeUserInput(create, true); err == nil {
		t.Fatal("create without password should fail")
	}
	data := map[string]interface{}{"email": "user@example.com", "password": "long-enough"}
	if err := normalizeUserInput(data, true); err != nil {
		t.Fatal(err)
	}
	if data["passwordConfirm"] != "long-enough" {
		t.Fatal("passwordConfirm not set")
	}
	update := map[string]interface{}{"email": "user@example.com"}
	if err := normalizeUserInput(update, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := update["password"]; ok {
		t.Fatal("empty password should be removed on update")
	}
	if err := normalizeUserInput(map[string]interface{}{"email": "bad"}, false); err == nil {
		t.Fatal("invalid email should fail")
	}
}
