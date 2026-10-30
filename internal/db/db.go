// Package db opens the SQLite database and applies the embedded schema.
//
// SQLite is deliberately the only storage engine: Mailhearth targets very
// small servers and must not require a separate database process.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// schemaFiles is the source of schema files; tests substitute it.
var schemaFiles fs.FS = migrationFS

// DB wraps *sql.DB with helpers.
type DB struct {
	*sql.DB
}

// Open opens (creating if needed) the database at path and brings its
// schema up to date.
func Open(path string) (*DB, error) {
	p := filepath.ToSlash(path)
	// 写入事务在开始时取得写入权限，保持候选检查与提交处于同一事务。
	dsn := "file:" + p + "?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-4000)"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A tiny pool: SQLite serialises writers anyway and every open connection
	// costs memory on the constrained hosts we target.
	sqldb.SetMaxOpenConns(4)
	sqldb.SetMaxIdleConns(2)
	sqldb.SetConnMaxIdleTime(5 * time.Minute)
	d := &DB{DB: sqldb}
	if err := d.migrate(context.Background()); err != nil {
		sqldb.Close()
		return nil, err
	}
	return d, nil
}

// migrations returns the embedded schema files in order. The schema version
// stored in PRAGMA user_version is the number of files applied.
func migrations() ([]string, error) {
	entries, err := fs.ReadDir(schemaFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// migrate applies every pending schema file in its own transaction; a
// failure rolls the transaction back and leaves the database untouched.
func (d *DB) migrate(ctx context.Context) error {
	names, err := migrations()
	if err != nil {
		return err
	}
	for {
		done, err := d.migrateNext(ctx, names)
		if err != nil || done {
			return err
		}
	}
}

func (d *DB) migrateNext(ctx context.Context, names []string) (done bool, err error) {
	// _txlock=immediate: the write lock is held from BEGIN, so a second
	// process starting at the same time waits and then sees the new version.
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return false, err
	}
	switch {
	case version > len(names):
		return false, fmt.Errorf("database schema version %d is newer than this build supports (%d)", version, len(names))
	case version == len(names):
		return true, nil
	case version == 0:
		var objects int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
			return false, err
		}
		if objects > 0 {
			return false, errors.New("database was not created by this version of Mailhearth; start with an empty data directory")
		}
	}
	name := names[version]
	body, err := fs.ReadFile(schemaFiles, "migrations/"+name)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return false, fmt.Errorf("schema %s: %w", name, err)
	}
	// PRAGMA does not take bound parameters; version is an int.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, version+1)); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// Now returns the canonical timestamp string used across the schema.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// ParseTime parses a stored timestamp; zero time on failure.
func ParseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// Tx runs fn inside a transaction, committing on nil error.
func (d *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Querier is satisfied by both *sql.DB and *sql.Tx.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// IsNotFound reports whether err is sql.ErrNoRows.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// GetSetting reads a key from the settings table (empty string when absent).
func GetSetting(ctx context.Context, q Querier, key string) (string, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if IsNotFound(err) {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a key in the settings table.
func SetSetting(ctx context.Context, q Querier, key, value string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
