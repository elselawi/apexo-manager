// Package secret encrypts integration credentials and management passwords at
// rest using AES-256-GCM. The key lives outside the SQLite database so a copied
// database alone does not disclose stored secrets.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	keySize   = 32
	prefix    = "v1:"
	keyEnvVar = "PBMANAGER_MASTER_KEY"
)

// Box provides authenticated encryption with a stable associated data label.
type Box struct {
	aead cipher.AEAD
}

// Open loads an existing key or creates a new 32-byte key with 0600
// permissions. It never follows a symlink for an existing key file.
func Open(dataDir string) (*Box, error) {
	if raw := strings.TrimSpace(os.Getenv(keyEnvVar)); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != keySize {
			return nil, fmt.Errorf("%s must be a base64-encoded 32-byte key", keyEnvVar)
		}
		return newBox(key)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create secret directory: %w", err)
	}
	path := filepath.Join(dataDir, "secret.key")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("secret key %s must not be a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("secret key %s is not a regular file", path)
		}
		if info.Size() != keySize {
			return nil, fmt.Errorf("secret key %s has an invalid size", path)
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read secret key: %w", err)
		}
		return newBox(key)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect secret key: %w", err)
	}

	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, ".secret-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create secret key: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(name, path); err != nil {
		return nil, fmt.Errorf("install secret key: %w", err)
	}
	return newBox(key)
}

func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext and binds it to purpose. The returned value is safe
// to store in a text database column. Empty plaintext yields an empty string.
func (b *Box) Seal(purpose, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if strings.TrimSpace(purpose) == "" {
		return "", errors.New("secret purpose is required")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nil, nonce, []byte(plaintext), []byte(purpose))
	out := append(nonce, ct...)
	return prefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal. A different purpose fails
// authentication, preventing cross-field ciphertext substitution.
func (b *Box) Open(purpose, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, prefix) {
		return "", errors.New("unsupported secret encoding")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, prefix))
	if err != nil {
		return "", errors.New("invalid secret encoding")
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+16 {
		return "", errors.New("truncated secret")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], []byte(purpose))
	if err != nil {
		return "", errors.New("secret authentication failed")
	}
	return string(plain), nil
}

// CheckKeyPermissions returns an error if the key file is group/world
// accessible. Used by diagnostics.
func CheckKeyPermissions(dataDir string) error {
	path := filepath.Join(dataDir, "secret.key")
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("secret key permissions are too broad: %o", info.Mode().Perm())
	}
	return nil
}
