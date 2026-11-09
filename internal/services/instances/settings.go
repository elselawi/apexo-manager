package instances

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	smtpsvc "github.com/apexo/pbmanager/internal/services/smtp"
	"github.com/apexo/pbmanager/internal/services/storage"
	"github.com/apexo/pbmanager/internal/store"
)

// CredentialInput links a PocketBase superuser to the manager.
type CredentialInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// SetCredentials verifies superuser credentials before storing them encrypted.
func (s *Service) SetCredentials(ctx context.Context, id string, in CredentialInput) error {
	inst, err := s.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("invalid email address")
	}
	if in.Password == "" {
		return errors.New("password is required")
	}
	client := pocketbase.New(inst.Port)
	if _, err := client.TestAuth(ctx, email, in.Password); err != nil {
		_ = s.Store.SetPBAuthState(ctx, inst.ID, email, "", "error", store.Now(), truncate(err.Error(), 400))
		if errors.Is(err, pocketbase.ErrUnauthorized) {
			return errors.New("PocketBase rejected those superuser credentials")
		}
		return fmt.Errorf("could not reach PocketBase authentication: %w", err)
	}
	enc, err := s.Secrets.Seal("pb_superuser:"+inst.ID, in.Password)
	if err != nil {
		return err
	}
	if err := s.Store.SetPBAuthState(ctx, inst.ID, email, enc, "linked", store.Now(), ""); err != nil {
		return err
	}
	// Operator-supplied credentials are not auto-healed by the manager.
	_ = s.Store.SetPBUnmanaged(ctx, inst.ID)
	return nil
}

// ClearCredentials unlinks PocketBase application credentials.
func (s *Service) ClearCredentials(ctx context.Context, id string) error {
	inst, err := s.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	if inst.PBSuperuserManaged {
		return errors.New("managed credentials must be removed with the managed-credential action")
	}
	return s.Store.ClearManagedPBAuth(ctx, inst.ID)
}

// TestCredentials verifies the stored management credential.
func (s *Service) TestCredentials(ctx context.Context, id string) error {
	inst, err := s.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	_, _, err = s.pbAuth(ctx, inst)
	return err
}

func (s *Service) pbAuth(ctx context.Context, inst *store.Instance) (*pocketbase.Client, string, error) {
	if inst.PBSuperuserEmail == "" || inst.PBSuperuserSecret == "" {
		return nil, "", errors.New("PocketBase management credentials are not linked for this instance")
	}
	password, err := s.Secrets.Open("pb_superuser:"+inst.ID, inst.PBSuperuserSecret)
	if err != nil {
		return nil, "", errors.New("stored PocketBase credential could not be decrypted")
	}
	client := pocketbase.New(inst.Port)
	token, err := client.TestAuth(ctx, inst.PBSuperuserEmail, password)
	if err != nil {
		_ = s.Store.SetPBAuthState(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, "error", store.Now(), truncate(err.Error(), 400))
		if errors.Is(err, pocketbase.ErrUnauthorized) {
			return nil, "", errors.New("PocketBase credentials are invalid or expired; update them in the instance settings")
		}
		return nil, "", err
	}
	_ = s.Store.SetPBAuthState(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, "linked", store.Now(), "")
	return client, token, nil
}

// ApplySMTP applies the assigned SMTP profile through the documented settings API.
func (s *Service) ApplySMTP(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if inst.SMTPProfileID == "" {
		return nil, errors.New("no SMTP profile is assigned to this instance")
	}
	profile, err := s.Store.GetSMTPProfile(ctx, inst.SMTPProfileID)
	if err != nil {
		return nil, err
	}
	password, err := s.Secrets.Open("smtp:"+profile.ID, profile.Password)
	if err != nil {
		return nil, errors.New("SMTP password could not be decrypted")
	}
	if profile.TLSMode == "none" {
		return nil, errors.New("PocketBase supports STARTTLS or implicit TLS only; unencrypted SMTP profiles cannot be applied")
	}
	if err := ex.Step(ctx, "validate-profile", "Test SMTP connectivity and authentication", func(ctx context.Context) (string, error) {
		res, err := smtpsvc.Test(ctx, profile, password)
		if err != nil {
			_ = s.Store.SetSMTPTestState(ctx, profile.ID, "failed", store.Now(), truncate(err.Error(), 500))
			return "", err
		}
		_ = s.Store.SetSMTPTestState(ctx, profile.ID, "validated", store.Now(), "")
		return fmt.Sprintf("Connected to %s:%d in %dms (authenticated=%v)", res.Host, res.Port, res.LatencyMS, res.Auth), nil
	}); err != nil {
		return nil, err
	}
	client, token, err := s.pbAuth(ctx, inst)
	if err != nil {
		_ = s.Store.SetApplyState(ctx, inst.ID, "smtp", "auth_missing", "", err.Error())
		return nil, err
	}
	tlsOn := profile.TLSMode == "tls"
	patch := pocketbase.SMTPPatch(profile.Host, profile.Port, profile.Username, password, profile.AuthMethod, tlsOn, profile.LocalName, profile.FromAddress, profile.FromName)
	if err := ex.Step(ctx, "apply", "Apply SMTP settings via PocketBase's supported settings API", func(ctx context.Context) (string, error) {
		if _, err := client.PatchSettings(ctx, token, patch); err != nil {
			return "", err
		}
		return "PocketBase accepted the SMTP settings", nil
	}); err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "verify", "Read back and verify the applied SMTP configuration", func(ctx context.Context) (string, error) {
		settings, err := client.GetSettings(ctx, token)
		if err != nil {
			return "", err
		}
		smtp := pocketbase.SettingsSMTP(settings)
		if smtp == nil || fmt.Sprint(smtp["host"]) != profile.Host || fmt.Sprint(smtp["port"]) != fmt.Sprint(profile.Port) {
			return "", errors.New("PocketBase settings read-back does not match the applied SMTP profile")
		}
		if enabled, ok := smtp["enabled"].(bool); ok && !enabled {
			return "", errors.New("PocketBase reports SMTP as disabled after applying the profile")
		}
		_ = s.Store.SetApplyState(ctx, inst.ID, "smtp", "applied", store.Now(), "")
		return "SMTP settings verified by read-back", nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"instanceId": inst.ID, "status": "applied", "profileId": profile.ID}, nil
}

// ApplyStorage applies the assigned S3-compatible storage profile.
func (s *Service) ApplyStorage(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if inst.StorageProfileID == "" {
		return nil, errors.New("no storage profile is assigned to this instance")
	}
	profile, err := s.Store.GetStorageProfile(ctx, inst.StorageProfileID)
	if err != nil {
		return nil, err
	}
	secret, err := s.Secrets.Open("storage:"+profile.ID, profile.SecretKey)
	if err != nil {
		return nil, errors.New("storage secret could not be decrypted")
	}
	if err := ex.Step(ctx, "validate-profile", "Validate S3 bucket access and credentials", func(ctx context.Context) (string, error) {
		res, err := storage.Test(ctx, profile, secret)
		if err != nil {
			_ = s.Store.SetStorageTestState(ctx, profile.ID, "failed", store.Now(), truncate(err.Error(), 500))
			return "", err
		}
		_ = s.Store.SetStorageTestState(ctx, profile.ID, "validated", store.Now(), "")
		return fmt.Sprintf("Bucket %s accessible at %s in %dms", res.Bucket, res.Endpoint, res.LatencyMS), nil
	}); err != nil {
		return nil, err
	}
	client, token, err := s.pbAuth(ctx, inst)
	if err != nil {
		_ = s.Store.SetApplyState(ctx, inst.ID, "storage", "auth_missing", "", err.Error())
		return nil, err
	}
	patch := pocketbase.S3Patch(profile.Endpoint, profile.Region, profile.Bucket, profile.AccessKey, secret, profile.ForcePathStyle)
	if err := ex.Step(ctx, "apply", "Apply S3 settings via PocketBase's supported settings API", func(ctx context.Context) (string, error) {
		if _, err := client.PatchSettings(ctx, token, patch); err != nil {
			return "", err
		}
		return "PocketBase accepted the S3 settings", nil
	}); err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "verify", "Read back and verify the applied S3 configuration", func(ctx context.Context) (string, error) {
		settings, err := client.GetSettings(ctx, token)
		if err != nil {
			return "", err
		}
		s3 := pocketbase.SettingsS3(settings)
		if s3 == nil || fmt.Sprint(s3["bucket"]) != profile.Bucket || fmt.Sprint(s3["region"]) != profile.Region {
			return "", errors.New("PocketBase settings read-back does not match the applied storage profile")
		}
		if enabled, ok := s3["enabled"].(bool); ok && !enabled {
			return "", errors.New("PocketBase reports S3 as disabled after applying the profile")
		}
		_ = s.Store.SetApplyState(ctx, inst.ID, "storage", "applied", store.Now(), "")
		return "S3 settings verified by read-back", nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"instanceId": inst.ID, "status": "applied", "profileId": profile.ID}, nil
}
