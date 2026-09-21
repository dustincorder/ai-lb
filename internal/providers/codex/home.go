package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

// managedConfigTOML is the entire non-secret configuration of a managed
// home. Keyring-only credential persistence is mandatory: managed
// logins fail closed without an OS keyring instead of falling back to
// plaintext auth.json. Update checks are disabled so managed processes
// never self-modify outside ai-lb releases.
const managedConfigTOML = `cli_auth_credentials_store = "keyring"
check_for_update_on_startup = false
`

// authFileName is checked for existence only. ai-lb never reads it.
const authFileName = "auth.json"

// ManagedHome resolves and prepares the isolated CODEX_HOME for one
// account: <dataDir>/providers/codex/accounts/<accountID>/codex-home,
// created with 0700. The account ID is validated to a safe charset so
// browser-supplied IDs can never escape the directory (no path
// traversal); paths are never accepted from API clients.
func ManagedHome(dataDir, accountID string) (string, error) {
	if !safeAccountID(accountID) {
		return "", fmt.Errorf("%w: unsafe account id", ErrCodexProtocol)
	}
	home := filepath.Join(dataDir, "providers", "codex", "accounts", accountID, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create managed home: %w", err)
	}
	// Enforce 0700 even if the directory already existed.
	if err := os.Chmod(home, 0o700); err != nil {
		return "", fmt.Errorf("secure managed home: %w", err)
	}
	// The managed home belongs to ai-lb: enforce the exact non-secret
	// config before any spawn, so a tampered config.toml can never flip
	// the home into file-based credential storage unnoticed.
	if err := enforceManagedConfig(home); err != nil {
		return "", err
	}
	return home, nil
}

func safeAccountID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 128 {
		return false
	}
	for _, r := range id {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

// enforceManagedConfig guarantees the exact managed config.toml:
// keyring-only credential storage plus no self-update. An existing file
// with identical content is left alone; any drift (including a switch
// to file-based auth storage) is repaired atomically before Codex can
// start, so a login can never run in file-storage mode. config.toml
// holds no secrets.
func enforceManagedConfig(home string) error {
	path := filepath.Join(home, "config.toml")
	if data, err := os.ReadFile(path); err == nil && string(data) == managedConfigTOML {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect managed config: %w", err)
	}
	tmp, err := os.CreateTemp(home, "config.toml.*")
	if err != nil {
		return fmt.Errorf("stage managed config: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(managedConfigTOML); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write managed config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("secure managed config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write managed config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("install managed config: %w", err)
	}
	return nil
}

// PlaintextAuthPresent reports whether auth.json exists in a managed
// home. Existence only — contents are never read. A present file means
// Codex persisted credentials outside the keyring, and the account must
// be treated as unsafely stored.
func PlaintextAuthPresent(codexHome string) bool {
	if codexHome == "" {
		return false
	}
	clean := filepath.Clean(codexHome)
	info, err := os.Stat(filepath.Join(clean, authFileName))
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
