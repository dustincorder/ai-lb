// Package db owns the local SQLite database: file location, schema
// migrations, startup validation, and settings persistence.
//
// The database holds metadata and state only. Secrets must never be
// stored here; they belong in OS credential storage (future SecretStore).
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"modernc.org/sqlite"

	"github.com/dustincorder/ai-lb/internal/config"
)

func init() {
	// Register driver name explicitly for clarity in sql.Open.
	sql.Register("ailb_sqlite", &sqlite.Driver{})
}

// schemaMigrations are applied exactly once each, in order.
// user_version tracks how many have been applied. Add new statements by
// appending; never edit an applied migration in place.
//
// Entries 1–2 are the foundation (settings, app_metadata); entries 3–5
// form the accounts schema change (table plus its two indexes); entries
// 6–7 add the opaque provider account identity plus its partial unique
// index. Later entries keep appending the same way.
var schemaMigrations = []string{
	`CREATE TABLE settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE app_metadata (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	// Migration 3: account profiles. Provider stays an open string on
	// purpose — no CHECK(provider IN (...)): new providers must not
	// require a schema migration. credentials_ref is empty until an
	// auth flow stores a secret; it is never set through the public API.
	`CREATE TABLE accounts (
		id              TEXT PRIMARY KEY,
		provider        TEXT NOT NULL,
		label           TEXT NOT NULL CHECK(length(label) BETWEEN 1 AND 200),
		identity        TEXT NOT NULL DEFAULT '',
		enabled         INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
		credentials_ref TEXT NOT NULL DEFAULT '',
		created_at      TEXT NOT NULL,
		updated_at      TEXT NOT NULL
	)`,
	`CREATE INDEX idx_accounts_provider ON accounts(provider)`,
	`CREATE INDEX idx_accounts_enabled ON accounts(enabled)`,
	// Migration 6–7: opaque provider-owned account identity for
	// cross-profile deduplication. The partial unique index covers
	// non-empty values only, so profiles without a known upstream ID
	// keep working; the same opaque ID under different providers is
	// allowed.
	`ALTER TABLE accounts ADD COLUMN provider_account_id TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX idx_accounts_provider_account ON accounts(provider, provider_account_id) WHERE provider_account_id <> ''`,
}

// schemaVersion is the number of migrations this binary knows.
func schemaVersion() int {
	return len(schemaMigrations)
}

// DB wraps the SQLite connection with the resolved file path.
type DB struct {
	Conn *sql.DB
	Path string
}

// DefaultDir returns the OS-appropriate application data directory.
func DefaultDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "ai-lb"), nil
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(base, "ai-lb"), nil
	default:
		// Linux and other Unix: respect XDG_DATA_HOME.
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "ai-lb"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "ai-lb"), nil
	}
}

// Open creates the data directory if needed, opens (or creates) the
// database file, and prepares it in a fail-closed order: the schema
// version is inspected before any mutation (WAL, migrations, seeding),
// so a database from a newer ai-lb is rejected untouched.
func Open(dir string) (*DB, error) {
	if dir == "" {
		var derr error
		dir, derr = DefaultDir()
		if derr != nil {
			return nil, fmt.Errorf("resolve data dir: %w", derr)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dir, "ai-lb.db")
	conn, err := sql.Open("ailb_sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	d := &DB{Conn: conn, Path: path}
	// Read-only inspection first: no WAL, migration, or seed write may
	// touch a database this binary does not understand.
	version, verr := d.currentVersion()
	if verr != nil {
		conn.Close()
		return nil, verr
	}
	if version > schemaVersion() {
		conn.Close()
		return nil, fmt.Errorf("database schema version %d is newer than this binary supports (%d); refusing to open",
			version, schemaVersion())
	}
	if err := d.enableWAL(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := d.validate(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := d.seedDefaults(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.Conn.Close()
}

// enableWAL switches the database to WAL mode as required by the
// architecture and verifies the mode actually took effect.
func (d *DB) enableWAL() error {
	var mode string
	if err := d.Conn.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal mode is %q, expected wal", mode)
	}
	return nil
}

// currentVersion reads PRAGMA user_version (0 for a fresh database).
func (d *DB) currentVersion() (int, error) {
	var version int
	if err := d.Conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// migrate applies only migrations newer than the stored user_version,
// one transaction per migration with the version bump inside. A database
// newer than this binary fails closed instead of being touched.
func (d *DB) migrate() error {
	version, err := d.currentVersion()
	if err != nil {
		return err
	}
	if version > schemaVersion() {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)",
			version, schemaVersion())
	}
	for i := version; i < schemaVersion(); i++ {
		tx, err := d.Conn.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(schemaMigrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record schema version %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// validate runs startup checks: foreign keys on, expected tables exist,
// schema version matches.
func (d *DB) validate() error {
	var fk int
	if err := d.Conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("check foreign_keys: %w", err)
	}
	if fk != 1 {
		return fmt.Errorf("foreign_keys pragma is off")
	}
	for _, table := range []string{"settings", "app_metadata", "accounts"} {
		var name string
		err := d.Conn.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			return fmt.Errorf("expected table %q missing: %w", table, err)
		}
	}
	var version int
	if err := d.Conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if version != schemaVersion() {
		return fmt.Errorf("schema version %d, expected %d", version, schemaVersion())
	}
	return nil
}

// seedDefaults inserts default settings for missing keys only.
func (d *DB) seedDefaults() error {
	defaults := config.Defaults()
	seed := map[string]string{
		"control_host":   defaults.ControlHost,
		"control_port":   itoa(defaults.ControlPort),
		"gateway_host":   defaults.GatewayHost,
		"gateway_port":   itoa(defaults.GatewayPort),
		"update_channel": defaults.UpdateChannel,
	}
	for k, v := range seed {
		if _, err := d.Conn.Exec(
			"INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING", k, v,
		); err != nil {
			return fmt.Errorf("seed setting %q: %w", k, err)
		}
	}
	return nil
}

// LoadSettings reads settings from the database. Missing keys fall back
// to defaults; present keys must parse strictly (strconv) and the merged
// record must validate — malformed persisted values fail closed instead
// of silently becoming defaults.
func (d *DB) LoadSettings() (config.Settings, error) {
	s := config.Defaults()
	rows, err := d.Conn.Query("SELECT key, value FROM settings")
	if err != nil {
		return s, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return s, fmt.Errorf("scan setting: %w", err)
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return s, fmt.Errorf("read settings: %w", err)
	}
	if v, ok := values["control_host"]; ok {
		s.ControlHost = v
	}
	if v, ok := values["control_port"]; ok {
		n, err := parsePortValue("control_port", v)
		if err != nil {
			return s, err
		}
		s.ControlPort = n
	}
	if v, ok := values["gateway_host"]; ok {
		s.GatewayHost = v
	}
	if v, ok := values["gateway_port"]; ok {
		n, err := parsePortValue("gateway_port", v)
		if err != nil {
			return s, err
		}
		s.GatewayPort = n
	}
	if v, ok := values["update_channel"]; ok {
		s.UpdateChannel = v
	}
	if err := config.Validate(s); err != nil {
		return s, fmt.Errorf("stored settings invalid: %w", err)
	}
	return s, nil
}

// parsePortValue strictly parses a persisted port: full-string decimal
// integer, no fallback. Out-of-range values (including huge ones that
// overflow int) are errors.
func parsePortValue(key, raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("setting %q has malformed port %q", key, raw)
	}
	if err := config.ValidatePort(n); err != nil {
		return 0, fmt.Errorf("setting %q: %w", key, err)
	}
	return n, nil
}

// SaveSettings validates and persists the full settings record.
func (d *DB) SaveSettings(s config.Settings) error {
	if err := config.Validate(s); err != nil {
		return err
	}
	tx, err := d.Conn.Begin()
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback()
	pairs := map[string]string{
		"control_host":   s.ControlHost,
		"control_port":   itoa(s.ControlPort),
		"gateway_host":   s.GatewayHost,
		"gateway_port":   itoa(s.GatewayPort),
		"update_channel": s.UpdateChannel,
	}
	for k, v := range pairs {
		if _, err := tx.Exec(
			"INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
			k, v,
		); err != nil {
			return fmt.Errorf("save setting %q: %w", k, err)
		}
	}
	return tx.Commit()
}

// Status reports the database path for the management API. It never
// returns filesystem data beyond the ai-lb database location itself.
func (d *DB) Status() map[string]string {
	return map[string]string{"path": d.Path, "status": "ok"}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
