package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Detection describes one Codex CLI probe.
type Detection struct {
	Installed   bool   `json:"installed"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	AppServerOK bool   `json:"app_server_compatible"`
	Error       string `json:"error,omitempty"`
}

// Detect locates the codex binary and probes `--version` with a short
// timeout. Compatibility is NOT decided by version-string parsing:
// the binary proves itself by starting app-server and completing the
// initialize handshake (see Compatible).
func Detect(ctx context.Context) Detection {
	path, err := exec.LookPath("codex")
	if err != nil {
		return Detection{Installed: false, Error: ErrCodexNotInstalled.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return Detection{Installed: true, Path: path, Error: fmt.Sprintf("%v: version probe failed", ErrCodexProcessFailed)}
	}
	return Detection{Installed: true, Path: path, Version: strings.TrimSpace(string(out))}
}

// Compatible starts a throwaway app-server against an isolated home and
// runs the initialize handshake. Success proves the binary can serve
// the stable account API ai-lb needs. The home must already exist.
func Compatible(ctx context.Context, binary, codexHome, clientVersion string, extraEnv ...string) error {
	c := NewClient(binary, codexHome, clientVersion, nil)
	c.ExtraEnv = extraEnv
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return fmt.Errorf("%w: %v", ErrCodexIncompatible, err)
	}
	_ = c.Close()
	return nil
}
