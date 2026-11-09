package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func nowUTC() time.Time { return time.Now().UTC() }

// TarEntry describes one file in a manager backup.
type TarEntry struct {
	Name   string
	Source string
	Data   []byte
	Mode   os.FileMode
}

// WriteTarGz writes files atomically in a gzipped tar archive.
func WriteTarGz(dest string, entries []TarEntry) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".backup-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		name := path.Clean(strings.ReplaceAll(e.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			tw.Close()
			gz.Close()
			tmp.Close()
			return fmt.Errorf("invalid archive entry %q", e.Name)
		}
		var size int64
		var source *os.File
		if e.Source != "" {
			info, err := os.Lstat(e.Source)
			if err != nil {
				tw.Close()
				gz.Close()
				tmp.Close()
				return err
			}
			if !info.Mode().IsRegular() {
				tw.Close()
				gz.Close()
				tmp.Close()
				return fmt.Errorf("%s is not a regular file", e.Source)
			}
			size = info.Size()
			if e.Mode == 0 {
				e.Mode = info.Mode().Perm()
			}
			source, err = os.Open(e.Source)
			if err != nil {
				tw.Close()
				gz.Close()
				tmp.Close()
				return err
			}
		} else {
			size = int64(len(e.Data))
		}
		if e.Mode == 0 {
			e.Mode = 0o600
		}
		hdr := &tar.Header{Name: name, Mode: int64(e.Mode.Perm()), Size: size, Typeflag: tar.TypeReg, ModTime: nowUTC()}
		if err := tw.WriteHeader(hdr); err != nil {
			if source != nil {
				source.Close()
			}
			tw.Close()
			gz.Close()
			tmp.Close()
			return err
		}
		if source != nil {
			if _, err := io.CopyN(tw, source, size); err != nil {
				source.Close()
				tw.Close()
				gz.Close()
				tmp.Close()
				return err
			}
			source.Close()
		} else if _, err := tw.Write(e.Data); err != nil {
			tw.Close()
			gz.Close()
			tmp.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		tmp.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmpName, dest)
}

// SafeExtractTarGz extracts files below dest. Symlinks, devices, and traversal
// paths are rejected. If stripPrefix is non-empty, every entry must begin with
// it and the prefix is removed.
func SafeExtractTarGz(src, dest, stripPrefix string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.ReplaceAll(hdr.Name, "\\", "/"))
		if stripPrefix != "" {
			prefix := strings.TrimSuffix(path.Clean(stripPrefix), "/") + "/"
			if !strings.HasPrefix(name, prefix) {
				return fmt.Errorf("archive entry %q is outside %s", hdr.Name, stripPrefix)
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "." || name == "" {
			continue
		}
		if path.IsAbs(name) || strings.HasPrefix(name, "../") || name == ".." {
			return fmt.Errorf("unsafe archive entry %q", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe archive entry %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > 8<<30 {
				return fmt.Errorf("archive entry %q has invalid size", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o600
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(f, tr, hdr.Size); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry type for %q", hdr.Name)
		}
	}
	return nil
}

// ZipInfo describes an official PocketBase backup archive.
type ZipInfo struct {
	RootPrefix string
	DataDBNext string
	Files      int
	Size       int64
}

// InspectPocketBaseZip verifies that an archive looks like a PocketBase backup
// and determines its root prefix (older versions may wrap files in a folder).
func InspectPocketBaseZip(src string) (ZipInfo, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return ZipInfo{}, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	info := ZipInfo{}
	var names []string
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." {
			continue
		}
		if path.IsAbs(name) || strings.HasPrefix(name, "../") {
			return info, fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return info, fmt.Errorf("zip contains symlink %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		info.Files++
		info.Size += int64(f.UncompressedSize64)
		if info.Size > 8<<30 {
			return info, errors.New("backup archive is too large")
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return info, errors.New("empty backup archive")
	}
	prefix := commonPrefix(names)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	// If there is a single top-level directory, only strip it when it directly
	// contains data.db. Otherwise older PocketBase archives may be flat.
	if prefix != "" {
		found := false
		for _, f := range zr.File {
			if path.Clean(strings.ReplaceAll(f.Name, "\\", "/")) == prefix+"data.db" {
				found = true
				break
			}
		}
		if !found {
			prefix = ""
		}
	}
	for _, n := range names {
		trimmed := strings.TrimPrefix(n, prefix)
		if trimmed == "data.db" {
			info.RootPrefix = prefix
			info.DataDBNext = trimmed
			return info, nil
		}
	}
	return info, errors.New("archive does not contain data.db at its expected root")
}

func commonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	first := strings.Split(names[0], "/")[0]
	if first == names[0] {
		return ""
	}
	for _, n := range names[1:] {
		parts := strings.Split(n, "/")
		if len(parts) < 2 || parts[0] != first {
			return ""
		}
	}
	return first
}

// ExtractPocketBaseZip extracts a validated PocketBase backup into dest.
func ExtractPocketBaseZip(src, dest string) error {
	info, err := InspectPocketBaseZip(src)
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if info.RootPrefix != "" {
			if !strings.HasPrefix(name, info.RootPrefix) {
				continue
			}
			name = strings.TrimPrefix(name, info.RootPrefix)
		}
		if name == "" || name == "." {
			continue
		}
		if path.IsAbs(name) || strings.HasPrefix(name, "../") {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(target), cleanDest) {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip contains symlink %q", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(rc, 8<<30)); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSQLite opens a database read-only and runs integrity checks.
func ValidateSQLite(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("SQLite database is empty")
	}
	return nil
}

// ValidateTarBackup verifies a manager archive end-to-end and checks its DB.
func ValidateTarBackup(ctx context.Context, src string) error {
	tmp, err := os.MkdirTemp("", "pbmanager-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := SafeExtractTarGz(src, tmp, ""); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(tmp, "manifest.json")); err != nil {
		return errors.New("backup manifest is missing")
	}
	if err := ValidateSQLite(ctx, filepath.Join(tmp, "data", "data.db")); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	return nil
}

// ValidateZipBackup verifies an official PocketBase backup.
func ValidateZipBackup(ctx context.Context, src string) error {
	info, err := InspectPocketBaseZip(src)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "pbmanager-verify-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := ExtractPocketBaseZip(src, tmp); err != nil {
		return err
	}
	if err := ValidateSQLite(ctx, filepath.Join(tmp, info.DataDBNext)); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	return nil
}

// SHA256File returns the hex digest and size of a regular file.
func SHA256File(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("backup path is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 16<<30))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
