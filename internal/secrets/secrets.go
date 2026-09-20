// Package secrets defines the SecretStore contract: all token material
// lives behind this interface, never in SQLite and never in API payloads.
//
// Production currently uses UnavailableStore (fail-closed): OS keychain
// backends arrive with the first real provider integration. Tests use
// MemoryStore. No file/env/plaintext fallback exists by design.
package secrets

import (
	"context"
	"errors"
)

// Typed errors.
var (
	// ErrNotFound means no secret exists under the reference.
	ErrNotFound = errors.New("secret not found")
	// ErrUnavailable means secret storage itself is unavailable.
	ErrUnavailable = errors.New("secret storage unavailable")
)

// Store persists opaque secret bytes under application-chosen refs.
// Implementations must copy input on Put and output on Get so callers
// can never alias stored material.
type Store interface {
	Put(ctx context.Context, ref string, secret []byte) error
	Get(ctx context.Context, ref string) ([]byte, error)
	Delete(ctx context.Context, ref string) error
}
