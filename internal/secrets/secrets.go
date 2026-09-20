// Package secrets will hold the SecretStore abstraction over OS
// credential storage (macOS Keychain, Windows Credential Manager, Linux
// Secret Service) plus short-lived in-memory credential sessions for the
// gateway. No secret handling exists yet; token material must never land
// in SQLite. See docs/RFC-0001-v0.1.md §10.
package secrets
