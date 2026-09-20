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

	"modernc.org/sqlite"

	"github.com/dustincorder/ai-lb/internal/config"
)

func init() {
	// Register driver name explicitly for clarity in sql.Open.
	sql.Register("ailb_sqlite", &sqlite.Driver{})
}

// schemaMigrations are applied in order. user_version tracks the level.
var schemaMigrations = []string{
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS app_metadata (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
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
// database file, enables foreign keys, runs migrations, and validates
// the result. Only settings/app_metadata schema exists at this stage.
func Open(dir string) (*DB, error) {
	if dir == "" {
		var err error
		dir, err = DefaultDir()
		if err != nil {
			return nil, fmt.Errorf("resolve data dir: %w", err)
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

func (d *DB) migrate() error {
	tx, err := d.Conn.Begin()
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	for i, stmt := range schemaMigrations {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(schemaMigrations))); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return tx.Commit()
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
	for _, table := range []string{"settings", "app_metadata"} {
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
	if version != len(schemaMigrations) {
		return fmt.Errorf("schema version %d, expected %d", version, len(schemaMigrations))
	}
	return nil
}

// seedDefaults inserts default settings for missing keys only.
func (d *DB) seedDefaults() error {
	defaults := config.Defaults()
	seed := map[string]string{
		"control_host": defaults.ControlHost,
		"control_port": itoa(defaults.ControlPort),
		"gateway_host": defaults.GatewayHost,
		"gateway_port": itoa(defaults.GatewayPort),
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

// LoadSettings reads settings from the database, falling back to defaults
// for missing keys.
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
		s.ControlPort = atoi(v, s.ControlPort)
	}
	if v, ok := values["gateway_host"]; ok {
		s.GatewayHost = v
	}
	if v, ok := values["gateway_port"]; ok {
		s.GatewayPort = atoi(v, s.GatewayPort)
	}
	if err := config.Validate(s); err != nil {
		return s, fmt.Errorf("stored settings invalid: %w", err)
	}
	return s, nil
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
		"control_host": s.ControlHost,
		"control_port": itoa(s.ControlPort),
		"gateway_host": s.GatewayHost,
		"gateway_port": itoa(s.GatewayPort),
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
	return fmt.Sprintf("%d", n)
}

func atoi(s string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return fallback
	}
	return n
}
