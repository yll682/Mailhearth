// Package db opens the SQLite database and applies embedded migrations.
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

	"mailhearth/internal/secrets"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps *sql.DB with helpers.
type DB struct {
	*sql.DB
}

// MigrationInputs carries the old installation inputs needed by version 2.
type MigrationInputs struct {
	PurelymailAPIURL          string
	IMAPAddr                  string
	IMAPTLS                   string
	SMTPAddr                  string
	SMTPTLS                   string
	SieveAddr                 string
	SieveTLS                  string
	CredentialsBox            *secrets.Box
	AllowDevelopmentPlaintext bool
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string, inputs ...MigrationInputs) (*DB, error) {
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
	d := &DB{sqldb}
	var in MigrationInputs
	if len(inputs) > 0 {
		in = inputs[0]
	}
	if err := d.migrate(context.Background(), in); err != nil {
		sqldb.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context, inputs MigrationInputs) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if name == "0002_multi_provider.sql" {
			if err := d.migrateV2(ctx, string(body), inputs); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			continue
		}
		if name == "0009_restrict_management_references.sql" || name == "0013_forwarding_observations.sql" {
			if err := d.migrateStructural(ctx, name, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if name == "0005_association_scopes.sql" {
			if err := validateAssociationScopes(ctx, tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if name == "0006_resource_ownership.sql" {
			var kind, id, related string
			err := tx.QueryRowContext(ctx, `SELECT object_type,object_id,COALESCE(related_id,0) FROM additional_scope_errors ORDER BY object_type,object_id LIMIT 1`).Scan(&kind, &id, &related)
			if err != nil && !IsNotFound(err) {
				tx.Rollback()
				return err
			}
			if err == nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: association_scope_conflict: %s id=%s relatedId=%s", name, kind, id, related)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, name, Now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// migrateV2 runs the structural migration on a dedicated connection, with
// foreign keys disabled outside the transaction and checked before commit.
func (d *DB) migrateV2(ctx context.Context, body string, inputs MigrationInputs) error {
	conn, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}
	defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateLegacyInputs(ctx, tx, inputs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		tx.Rollback()
		return err
	}
	if inputs.PurelymailAPIURL != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE mail_connections SET api_base_url=? WHERE provider_kind='purelymail'`, inputs.PurelymailAPIURL); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := convertLegacyProtocols(ctx, tx, inputs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES ('0002_multi_provider.sql', ?)`, Now()); err != nil {
		return err
	}
	var violations []string
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for rows.Next() {
		var table string
		var rowid int64
		var parent string
		var parentID int64
		if err := rows.Scan(&table, &rowid, &parent, &parentID); err != nil {
			rows.Close()
			tx.Rollback()
			return err
		}
		violations = append(violations, fmt.Sprintf("%s row %d parent %s %d", table, rowid, parent, parentID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		tx.Rollback()
		return err
	}
	rows.Close()
	if len(violations) > 0 {
		tx.Rollback()
		return fmt.Errorf("foreign key check failed: %s", strings.Join(violations, "; "))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
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
