// Package launcher runs the official Codex CLI against one connected,
// isolated ai-lb account without changing the user's default Codex home.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/providers"
	"github.com/dustincorder/ai-lb/internal/providers/codex"
)

var (
	ErrUsage               = errors.New("usage: ai-lb codex --account <account-id> [-- codex args...]")
	ErrAccountNotFound     = errors.New("account not found")
	ErrAccountLookup       = errors.New("account could not be loaded")
	ErrWrongProvider       = errors.New("account is not a Codex account")
	ErrAccountDisconnected = errors.New("account is not connected")
	ErrCodexUnavailable    = errors.New("Codex CLI is not installed")
	ErrUnsafeManagedHome   = errors.New("managed Codex account storage is unavailable")
)

// ExitError preserves the official Codex process exit code for the parent
// command without exposing subprocess diagnostics in the CLI contract.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("Codex exited with status %d", e.Code) }

type Options struct {
	AccountID string
	DataDir   string
	Args      []string
}

func ParseArgs(args []string) (Options, error) {
	var out Options
	separator := len(args)
	for i, arg := range args {
		if arg == "--" {
			separator = i
			out.Args = append([]string(nil), args[i+1:]...)
			break
		}
	}
	for i := 0; i < separator; i++ {
		switch args[i] {
		case "--account":
			if i+1 >= separator || args[i+1] == "" {
				return Options{}, ErrUsage
			}
			out.AccountID = args[i+1]
			i++
		case "--data-dir":
			if i+1 >= separator || args[i+1] == "" {
				return Options{}, ErrUsage
			}
			out.DataDir = args[i+1]
			i++
		case "-h", "--help":
			return Options{}, ErrUsage
		default:
			return Options{}, ErrUsage
		}
	}
	if out.AccountID == "" {
		return Options{}, ErrUsage
	}
	return out, nil
}

type AccountStore interface {
	Get(context.Context, string) (accounts.Account, error)
}

type RunConfig struct {
	Binary   string
	DataDir  string
	Accounts AccountStore
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Signals  <-chan os.Signal
}

// Run validates the selected profile, prepares its existing managed home,
// and launches codex directly with inherited stdio. It never reads or
// copies credentials and never mutates the caller's environment.
func Run(ctx context.Context, opts Options, cfg RunConfig) error {
	a, err := cfg.Accounts.Get(ctx, opts.AccountID)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			return ErrAccountNotFound
		}
		return ErrAccountLookup
	}
	if a.Provider != providers.Codex {
		return ErrWrongProvider
	}
	if !a.Connected() {
		return ErrAccountDisconnected
	}
	if cfg.Binary == "" {
		return ErrCodexUnavailable
	}
	home, err := codex.ManagedHome(cfg.DataDir, a.ID)
	if err != nil || codex.PlaintextAuthPresent(home) {
		return ErrUnsafeManagedHome
	}

	cmd := exec.Command(cfg.Binary, opts.Args...)
	cmd.Env = codex.InteractiveEnvForLauncher(home)
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	if err := cmd.Start(); err != nil {
		return errors.New("Codex CLI could not be started")
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return processResult(err)
	case sig := <-cfg.Signals:
		if err := stopChild(cmd.Process, sig); err != nil {
			_ = cmd.Process.Kill()
		}
		return waitForChild(done, cmd.Process)
	case <-ctx.Done():
		if err := stopChild(cmd.Process, os.Interrupt); err != nil {
			// Windows does not implement os.Interrupt for arbitrary child
			// processes; kill immediately rather than leaving the launcher
			// blocked until the orphan-prevention timeout.
			_ = cmd.Process.Kill()
		}
		return waitForChild(done, cmd.Process)
	}
}

func waitForChild(done <-chan error, process *os.Process) error {
	select {
	case err := <-done:
		return processResult(err)
	case <-time.After(5 * time.Second):
		_ = process.Kill()
		return processResult(<-done)
	}
}

func processResult(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := processStateExitCode(exitErr.ProcessState)
		return &ExitError{Code: code}
	}
	return errors.New("Codex process failed")
}

// SignalContext gives the launcher both cancellation and the actual OS signal
// so Unix can preserve SIGINT versus SIGTERM when forwarding to Codex.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc, <-chan os.Signal) {
	ctx, cancel := context.WithCancel(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	return ctx, func() {
		signal.Stop(signals)
		cancel()
	}, signals
}
