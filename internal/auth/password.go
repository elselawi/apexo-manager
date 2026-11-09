// Package auth implements dashboard administrator authentication and sessions.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrAccountDisabled    = errors.New("administrator account is disabled")
	ErrSessionExpired     = errors.New("session expired or revoked")
	ErrWeakPassword       = errors.New("password must be between 12 and 256 characters")
	ErrInvalidUsername    = errors.New("username must be 3-64 characters and contain only letters, digits, . _ @ -")
)

var dummyHash = func() string {
	h, _ := hashPassword("this-is-only-a-timing-equalizer-password")
	return h
}()

// HashPassword produces an Argon2id PHC hash.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return hashPassword(password)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC hash in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("unsupported password hash")
	}
	var mem, iter uint64
	var par uint64
	for _, pair := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return false, errors.New("invalid password hash parameters")
		}
		v, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return false, errors.New("invalid password hash parameters")
		}
		switch kv[0] {
		case "m":
			mem = v
		case "t":
			iter = v
		case "p":
			par = v
		}
	}
	if mem == 0 || iter == 0 || par == 0 || mem > 1024*1024 || iter > 20 || par > 64 {
		return false, errors.New("invalid password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("invalid password salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("invalid password hash")
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iter), uint32(mem), uint8(par), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ValidatePassword enforces the dashboard password policy.
func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 256 {
		return ErrWeakPassword
	}
	return nil
}

// ValidateUsername enforces a conservative username character set.
func ValidateUsername(username string) error {
	if len(username) < 3 || len(username) > 64 {
		return ErrInvalidUsername
	}
	for _, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '@', r == '-':
		default:
			return ErrInvalidUsername
		}
	}
	return nil
}

// ValidRole checks administrator roles.
func ValidRole(role string) bool {
	return role == "owner" || role == "admin" || role == "viewer"
}
