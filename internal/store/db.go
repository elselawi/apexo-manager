package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var ErrNotFound = errors.New("not found")

// Store is the single persistence layer shared by the REST API and the CLI.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the SQLite database, enables WAL/foreign keys, and applies
// embedded migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	u := &url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "trusted_schema(0)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("secure database permissions: %w", err)
	}
	return &Store{db: db, path: path}, nil
}

// Close releases the database handles. WAL checkpointing is handled by SQLite.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for narrow, internal operations only.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	type mig struct {
		version int
		name    string
	}
	var list []mig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		numPart := strings.SplitN(e.Name(), "_", 2)[0]
		v, err := strconv.Atoi(numPart)
		if err != nil {
			return fmt.Errorf("invalid migration filename %q", e.Name())
		}
		list = append(list, mig{version: v, name: e.Name()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	for _, m := range list {
		var exists int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, m.version, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}
	return nil
}

// NewID returns a 128-bit random identifier.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Now returns the canonical database timestamp.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// FormatTime converts a time to the canonical DB format.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseTime parses a database timestamp, returning zero for empty values.
func ParseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// MigrateStatus reports applied migration versions for diagnostics.
func (s *Store) MigrateStatus(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// QuickCheck runs SQLite's integrity check. It returns a descriptive error when
// the database reports corruption.
func (s *Store) QuickCheck(ctx context.Context) (string, error) {
	var result string
	if err := s.db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return "", err
	}
	if !strings.EqualFold(result, "ok") {
		return result, fmt.Errorf("sqlite integrity check failed: %s", result)
	}
	return result, nil
}

// Snapshot writes a consistent copy of the manager database using VACUUM INTO.
func (s *Store) Snapshot(ctx context.Context, dest string) error {
	if !filepath.IsAbs(dest) {
		return errors.New("snapshot destination must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	_ = os.Remove(dest)
	quoted := strings.ReplaceAll(dest, "'", "''")
	_, err := s.db.ExecContext(ctx, `VACUUM INTO '`+quoted+`'`)
	if err != nil {
		return fmt.Errorf("sqlite snapshot: %w", err)
	}
	return os.Chmod(dest, 0o600)
}
