package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "correct-horse-battery-staple")
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	ok, _ = VerifyPassword(hash, "wrong")
	if ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password accepted: %v", err)
	}
}

func TestUsernameAndRoleValidation(t *testing.T) {
	if err := ValidateUsername("valid.user-1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"a", "has space", "semi;colon", "new\nline"} {
		if err := ValidateUsername(bad); err == nil {
			t.Fatalf("accepted invalid username %q", bad)
		}
	}
	if !ValidRole("owner") || ValidRole("root") {
		t.Fatal("role validation failed")
	}
}

func TestLoginLimiterBlocksAfterFailures(t *testing.T) {
	l := NewLoginLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < maxFailures; i++ {
		if allowed, _ := l.Allow("1.2.3.4"); !allowed {
			t.Fatal("blocked too early")
		}
		l.Failure("1.2.3.4")
	}
	if allowed, retry := l.Allow("1.2.3.4"); allowed || retry <= 0 {
		t.Fatalf("expected block, got allowed=%v retry=%v", allowed, retry)
	}
	now = now.Add(blockFor + time.Second)
	if allowed, _ := l.Allow("1.2.3.4"); !allowed {
		t.Fatal("expected block to expire")
	}
}

func testAuthService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st, time.Hour), st
}

func TestLoginAndSessions(t *testing.T) {
	ctx := context.Background()
	svc, st := testAuthService(t)
	a, err := svc.CreateAdmin(ctx, "owner", "Owner", "correct-horse-battery-staple", "owner")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.Login(ctx, "owner", "correct-horse-battery-staple", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.CSRF == "" || result.Token == "" {
		t.Fatal("missing tokens")
	}
	sess, admin, err := svc.Authenticate(ctx, result.Token)
	if err != nil || admin.ID != a.ID || sess.AdminID != a.ID {
		t.Fatalf("authenticate: %v %#v", err, admin)
	}
	if _, _, err := svc.Authenticate(ctx, "bogus"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("bogus token: %v", err)
	}
	if err := svc.ResetPassword(ctx, a.ID, "new-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "owner", "new-password-123", "127.0.0.1", "test"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	_ = st.SetAdminDisabled(ctx, a.ID, true)
	if _, err := svc.Login(ctx, "owner", "new-password-123", "127.0.0.1", "test"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
}

func TestLastOwnerProtection(t *testing.T) {
	ctx := context.Background()
	svc, _ := testAuthService(t)
	owner, err := svc.CreateAdmin(ctx, "owner", "", "correct-horse-battery-staple", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateAdmin(ctx, owner.ID, "", "admin", false); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner protection, got %v", err)
	}
	if err := svc.DeleteAdmin(ctx, owner.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("expected last owner delete protection, got %v", err)
	}
	second, err := svc.CreateAdmin(ctx, "owner2", "", "correct-horse-battery-staple", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateAdmin(ctx, second.ID, "", "admin", false); err != nil {
		t.Fatalf("demote with another owner: %v", err)
	}
}
