package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagedHomeLayout(t *testing.T) {
	dir := t.TempDir()
	home, err := ManagedHome(dir, "acc-123_ABC")
	if err != nil {
		t.Fatalf("ManagedHome: %v", err)
	}
	want := filepath.Join(dir, "providers", "codex", "accounts", "acc-123_ABC", "codex-home")
	if home != want {
		t.Errorf("home = %q, want %q", home, want)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("managed config missing: %v", err)
	}
	cfg := string(data)
	for _, want := range []string{`cli_auth_credentials_store = "keyring"`, `check_for_update_on_startup = false`} {
		if !containsLine(cfg, want) {
			t.Errorf("managed config must contain %q, got:\n%s", want, cfg)
		}
	}
}

func containsLine(s, sub string) bool {
	for _, line := range splitLines(s) {
		if line == sub {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func TestManagedHomePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not honor Unix mode bits; managed homes live under the user profile, which is ACL-isolated per user")
	}
	dir := t.TempDir()
	home, err := ManagedHome(dir, "acc-1")
	if err != nil {
		t.Fatalf("ManagedHome: %v", err)
	}
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("home mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestManagedHomeRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", ".", "..", "../evil", "a/b", "a\\b", "a b", "a:b", ".hidden", "üser"} {
		if _, err := ManagedHome(dir, bad); err == nil {
			t.Errorf("account id %q must be rejected", bad)
		}
	}
	// Nothing escaped the accounts directory.
	entries, _ := os.ReadDir(filepath.Join(dir, "providers", "codex", "accounts"))
	if len(entries) != 0 {
		t.Errorf("rejected ids must not create directories: %v", entries)
	}
}

func TestAuthJSONDetection(t *testing.T) {
	dir := t.TempDir()
	home, err := ManagedHome(dir, "acc-1")
	if err != nil {
		t.Fatalf("ManagedHome: %v", err)
	}
	if PlaintextAuthPresent(home) {
		t.Error("fresh managed home must not report auth.json")
	}
	// Contents are irrelevant; only existence is checked.
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"anything":1}`), 0o600); err != nil {
		t.Fatalf("plant auth.json: %v", err)
	}
	if !PlaintextAuthPresent(home) {
		t.Error("planted auth.json must be detected")
	}
	if PlaintextAuthPresent("") {
		t.Error("empty home must not report auth.json")
	}
}
