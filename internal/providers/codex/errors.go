package codex

import (
	"errors"
	"fmt"
	"strings"
)

// Typed integration errors. Handlers translate these into stable API
// error codes; raw subprocess output never reaches clients.
var (
	// ErrCodexNotInstalled means no codex binary was found on PATH.
	ErrCodexNotInstalled = errors.New("codex CLI not installed")
	// ErrCodexIncompatible means the binary cannot serve the stable
	// account API (no app-server or failed initialize handshake).
	ErrCodexIncompatible = errors.New("codex CLI incompatible")
	// ErrCodexProcessFailed covers spawn, I/O, timeout, and exit
	// failures of a managed app-server process.
	ErrCodexProcessFailed = errors.New("codex process failed")
	// ErrCodexProtocol means the wire conversation broke (bad framing,
	// undecodable payload, missing required field).
	ErrCodexProtocol = errors.New("codex protocol error")
	// ErrCodexNotConnected means the managed home has no ChatGPT login.
	ErrCodexNotConnected = errors.New("codex account not connected")
	// ErrCredentialStoreUnavailable means the managed home cannot
	// persist credentials in the OS keyring (or fell back to plaintext
	// auth.json, which ai-lb refuses).
	ErrCredentialStoreUnavailable = errors.New("codex credential storage unavailable")
	// ErrLoginInProgress means a login session already runs for the
	// account; cancel it before starting another.
	ErrLoginInProgress = errors.New("login already in progress")
)

// classifyRPC translates an upstream JSON-RPC error into a typed error
// without forwarding raw upstream text to API clients.
func classifyRPC(rpcErr *rpcError) error {
	if rpcErr == nil {
		return nil
	}
	if rpcErr.Code == -32601 {
		return fmt.Errorf("%w: method not found", ErrCodexIncompatible)
	}
	if strings.Contains(strings.ToLower(rpcErr.Message), "authentication required") {
		return fmt.Errorf("%w: %s", ErrCodexNotConnected, sanitizeError(rpcErr.Message))
	}
	return fmt.Errorf("%w: upstream error %d", ErrCodexProcessFailed, rpcErr.Code)
}
