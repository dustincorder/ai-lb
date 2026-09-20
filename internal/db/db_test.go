package db

import (
	"path/filepath"
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
	if version != len(schemaMigrations) {
		t.Errorf("schema version %d, want %d", version, len(schemaMigrations))
	}
	var fk int
	if err := d.Conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys should be on, got %d, err %v", fk, err)
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
