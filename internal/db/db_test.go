package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dustincorder/ai-lb/internal/config"
)

func TestOpenStartupMigrations(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	if d.Path != filepath.Join(dir, "ai-lb.db") {
		t.Errorf("unexpected db path: %s", d.Path)
	}
	var version int
	if err := d.Conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != schemaVersion() {
		t.Errorf("schema version %d, want %d", version, schemaVersion())
	}
	var fk int
	if err := d.Conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys should be on, got %d, err %v", fk, err)
	}
}

func TestJournalModeIsWAL(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	var mode string
	if err := d.Conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal mode = %q, want wal", mode)
	}
}

func TestReopenDoesNotReapplyMigrations(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := config.Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 8421,
		GatewayHost: "127.0.0.1",
		GatewayPort: 8422,
	}
	if err := d.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	d2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer d2.Close()
	var version int
	if err := d2.Conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != schemaVersion() {
		t.Errorf("version after reopen = %d, want %d", version, schemaVersion())
	}
	got, err := d2.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got != want {
		t.Errorf("data lost across reopen: got %+v want %+v", got, want)
	}
}

// TestMigrationFromOlderVersion simulates a v1 database and expects Open
// to progress it to the current version without touching v1 objects.
func TestMigrationFromOlderVersion(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("ailb_sqlite", "file:"+filepath.Join(dir, "ai-lb.db"))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(schemaMigrations[0]); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("stamp v1: %v", err)
	}
	if _, err := raw.Exec(
		"INSERT INTO settings(key, value) VALUES('control_port', '8431')"); err != nil {
		t.Fatalf("seed v1 data: %v", err)
	}
	raw.Close()

	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open should migrate v1 forward: %v", err)
	}
	defer d.Close()
	var version int
	if err := d.Conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != schemaVersion() {
		t.Errorf("migrated version = %d, want %d", version, schemaVersion())
	}
	got, err := d.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got.ControlPort != 8431 {
		t.Errorf("v1 data lost during migration: %+v", got)
	}
}

// TestFutureSchemaFailsClosed expects a clear error when the database is
// newer than the binary instead of any write attempt.
func TestFutureSchemaFailsClosed(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("ailb_sqlite", "file:"+filepath.Join(dir, "ai-lb.db"))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(schemaMigrations[0]); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatalf("stamp future: %v", err)
	}
	raw.Close()

	if _, err := Open(dir); err == nil {
		t.Fatal("Open should fail closed on a newer-than-binary schema")
	} else if !strings.Contains(err.Error(), "newer than this binary") {
		t.Errorf("error should explain the version mismatch, got: %v", err)
	}
}

func TestSettingsPersistence(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	got, err := d.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got != config.Defaults() {
		t.Errorf("fresh db should seed defaults, got %+v", got)
	}

	want := config.Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 8401,
		GatewayHost: "127.0.0.1",
		GatewayPort: 8402,
	}
	if err := d.SaveSettings(want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	d2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer d2.Close()
	got2, err := d2.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings after reopen: %v", err)
	}
	if got2 != want {
		t.Errorf("settings not persisted across restart: got %+v want %+v", got2, want)
	}
}

func TestSaveSettingsRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	bad := config.Defaults()
	bad.ControlPort = 0
	if err := d.SaveSettings(bad); err == nil {
		t.Error("SaveSettings should reject port 0")
	}
	bad = config.Defaults()
	bad.GatewayHost = "0.0.0.0"
	if err := d.SaveSettings(bad); err == nil {
		t.Error("SaveSettings should reject non-loopback host")
	}
}
