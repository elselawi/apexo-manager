// Package platform contains safe operating-system primitives shared by all
// infrastructure integrations: argument-only command execution, atomic file
// writes, path validation, and small filesystem helpers.
package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Runner abstracts external command execution so tests can avoid touching the
// host. No shell is ever involved.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner executes binaries directly.
type ExecRunner struct {
	// Env optionally replaces the environment. A nil slice inherits.
	Env []string
	// MaxOutput caps captured stdout and stderr per stream.
	MaxOutput int
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, nil, errors.New("empty command name")
	}
	if strings.ContainsAny(name, "\x00\n\r") {
		return nil, nil, errors.New("invalid command name")
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return nil, nil, errors.New("invalid NUL in command argument")
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if r.Env != nil {
		cmd.Env = append([]string{}, r.Env...)
	}
	var out, errb bytes.Buffer
	max := r.MaxOutput
	if max <= 0 {
		max = 1 << 20
	}
	cmd.Stdout = &limitedBuffer{buf: &out, max: max}
	cmd.Stderr = &limitedBuffer{buf: &errb, max: max}
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			return out.Bytes(), errb.Bytes(), &CommandError{Name: name, Args: redactArgs(args), Code: code, Stderr: strings.TrimSpace(errb.String())}
		}
		return out.Bytes(), errb.Bytes(), fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return out.Bytes(), errb.Bytes(), nil
}

// CommandError is a failed external command.
type CommandError struct {
	Name   string
	Args   []string
	Code   int
	Stderr string
}

func (e *CommandError) Error() string {
	msg := fmt.Sprintf("%s exited with code %d", filepath.Base(e.Name), e.Code)
	if e.Stderr != "" {
		msg += ": " + oneLine(e.Stderr, 800)
	}
	return msg
}

func redactArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		if strings.Contains(strings.ToLower(out[i]), "password=") || strings.Contains(strings.ToLower(out[i]), "token=") {
			out[i] = "[redacted]"
		}
	}
	return out
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	return b.buf.Write(p)
}

var safeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// ValidSafeName accepts a conservative identifier useful for service and unit
// names. It cannot express path separators or shell metacharacters.
func ValidSafeName(s string) bool { return safeNameRE.MatchString(s) }

// ValidateAbsolutePath validates a path used for filesystem operations.
func ValidateAbsolutePath(p string, allowRoot bool) error {
	if p == "" {
		return errors.New("path is required")
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return errors.New("path contains invalid characters")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path %q must be absolute", p)
	}
	clean := filepath.Clean(p)
	if clean == "/" && !allowRoot {
		return errors.New("filesystem root is not an acceptable path")
	}
	if clean != p {
		return fmt.Errorf("path %q must be normalized (use %q)", p, clean)
	}
	return nil
}

// IsWithin reports whether target is inside base (or equal to it).
func IsWithin(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// EnsureDir creates a directory and verifies it is not a symlink. Intermediate
// directories are explicitly set to 0755 so a restrictive process umask (the
// manager uses 0077) cannot make an installation parent untraversable by its
// service user.
func EnsureDir(path string, mode os.FileMode) error {
	if err := ValidateAbsolutePath(path, false); err != nil {
		return err
	}
	var missing []string
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s must not be a symlink", p)
			}
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", p)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, p)
		if p == filepath.Dir(p) {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		p := missing[i]
		createMode := os.FileMode(0o755)
		if p == path {
			createMode = mode
		}
		if err := os.Mkdir(p, createMode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := os.Chmod(p, createMode); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return os.Chmod(path, mode)
}

// AtomicWrite writes a file via a same-directory temporary file and rename.
// If mode is zero, an existing file's mode is preserved, otherwise 0644 is used.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := ValidateAbsolutePath(path, false); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if mode == 0 {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		} else {
			mode = 0o644
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return nil
}

// BackupFile copies path to <backupDir>/<prefix>-<timestamp><ext>, preserving
// mode and returning the backup path.
func BackupFile(path, backupDir, prefix string) (string, error) {
	if err := ValidateAbsolutePath(path, false); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if err := EnsureDir(backupDir, 0o700); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	name := fmt.Sprintf("%s-%s%s", sanitizeFilename(prefix), stamp, filepath.Ext(path))
	dest := filepath.Join(backupDir, name)
	if err := CopyFile(path, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// CopyFile copies a regular file, rejecting symlinks and non-regular sources.
func CopyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, io.LimitReader(in, 1<<40)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}

// ReadFileLimited reads a regular file up to max bytes.
func ReadFileLimited(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s is too large (%d bytes)", path, info.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "backup"
	}
	return b.String()
}

// ShellQuote quotes a string for display only. Commands are never run through
// a shell by this application.
func ShellQuote(s string) string { return strconv.Quote(s) }
