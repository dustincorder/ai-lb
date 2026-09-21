package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"bytes"
	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/launcher"
	"github.com/dustincorder/ai-lb/internal/providers"
)

type launcherAccountStore struct{ account accounts.Account }

func (s launcherAccountStore) Get(context.Context, string) (accounts.Account, error) {
	return s.account, nil
}

func TestRunCodexArgs(t *testing.T) {
	if _, err := launcher.ParseArgs([]string{"--account", "acc-1", "--", "--help", "exec", "hello world"}); err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if err := run([]string{"codex", "--account"}); !errors.Is(err, launcher.ErrUsage) {
		t.Fatalf("missing account error = %v", err)
	}
}

func TestLauncherHelperProcess(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "TestLauncherHelperProcess") {
		return
	}
	if strings.Contains(strings.Join(os.Args, "\x1f"), "\x1fhang") {
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
		<-interrupted
		return
	}
	_, _ = os.Stdout.WriteString("home=" + os.Getenv("CODEX_HOME") + "\n")
	_, _ = os.Stdout.WriteString("args=" + strings.Join(os.Args, "\x1f") + "\n")
	_, _ = os.Stderr.WriteString("stderr-from-codex\n")
	if len(os.Args) > 0 && os.Args[len(os.Args)-1] == "exit-7" {
		os.Exit(7)
	}
}

func TestLauncherPassesHomeArgsAndStreams(t *testing.T) {
	dataDir := t.TempDir()
	defaultHome := t.TempDir()
	t.Setenv("HOME", defaultHome)
	if err := os.Mkdir(filepath.Join(defaultHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	defaultMarker := filepath.Join(defaultHome, ".codex", "marker")
	if err := os.WriteFile(defaultMarker, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"-test.run=TestLauncherHelperProcess", "--", "--help", "exec", "hello world"}
	err := launcher.Run(context.Background(), launcher.Options{AccountID: "acc-1", Args: args}, launcher.RunConfig{
		Binary: os.Args[0], DataDir: dataDir, Accounts: launcherAccountStore{account: accounts.Account{ID: "acc-1", Provider: providers.Codex, CredentialsRef: "managed"}}, Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	managed := filepath.Join(dataDir, "providers", "codex", "accounts", "acc-1", "codex-home")
	if !strings.Contains(stdout.String(), "home="+managed) {
		t.Errorf("stdout missing selected home: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "\x1f--\x1f--help\x1fexec\x1fhello world") {
		t.Errorf("stdout missing exact child args: %q", stdout.String())
	}
	if stderr.String() != "stderr-from-codex\n" {
		t.Errorf("stderr = %q", stderr.String())
	}
	if got, err := os.ReadFile(defaultMarker); err != nil || string(got) != "untouched" {
		t.Errorf("default Codex home changed: %q, %v", got, err)
	}
}

func TestLauncherPropagatesExitCode(t *testing.T) {
	err := launcher.Run(context.Background(), launcher.Options{AccountID: "acc-1", Args: []string{"-test.run=TestLauncherHelperProcess", "exit-7"}}, launcher.RunConfig{
		Binary: os.Args[0], DataDir: t.TempDir(), Accounts: launcherAccountStore{account: accounts.Account{ID: "acc-1", Provider: providers.Codex, CredentialsRef: "managed"}}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	var exitErr *launcher.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 7 {
		t.Fatalf("error = %v, want exit code 7", err)
	}
}

func TestLauncherRejectsBeforeSpawn(t *testing.T) {
	for name, account := range map[string]accounts.Account{
		"disconnected":   {ID: "acc-1", Provider: providers.Codex},
		"wrong provider": {ID: "acc-1", Provider: "antigravity", CredentialsRef: "managed"},
	} {
		t.Run(name, func(t *testing.T) {
			err := launcher.Run(context.Background(), launcher.Options{AccountID: "acc-1"}, launcher.RunConfig{Binary: "/does/not/spawn", DataDir: t.TempDir(), Accounts: launcherAccountStore{account: account}})
			if name == "disconnected" && !errors.Is(err, launcher.ErrAccountDisconnected) {
				t.Fatalf("error = %v", err)
			}
			if name == "wrong provider" && !errors.Is(err, launcher.ErrWrongProvider) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLauncherRejectsUnknownAndUnsafeHomes(t *testing.T) {
	err := launcher.Run(context.Background(), launcher.Options{AccountID: "missing"}, launcher.RunConfig{
		Binary: os.Args[0], DataDir: t.TempDir(), Accounts: accountErrorStore{err: accounts.ErrNotFound},
	})
	if !errors.Is(err, launcher.ErrAccountNotFound) {
		t.Fatalf("unknown account error = %v", err)
	}

	dataDir := t.TempDir()
	home := filepath.Join(dataDir, "providers", "codex", "accounts", "acc-1", "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("not read"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = launcher.Run(context.Background(), launcher.Options{AccountID: "acc-1"}, launcher.RunConfig{
		Binary: os.Args[0], DataDir: dataDir, Accounts: launcherAccountStore{account: accounts.Account{ID: "acc-1", Provider: providers.Codex, CredentialsRef: "managed"}},
	})
	if !errors.Is(err, launcher.ErrUnsafeManagedHome) {
		t.Fatalf("auth.json error = %v", err)
	}
}

func TestLauncherCancellationReapsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := launcher.Run(ctx, launcher.Options{AccountID: "acc-1", Args: []string{"-test.run=TestLauncherHelperProcess", "hang"}}, launcher.RunConfig{
		Binary: os.Args[0], DataDir: t.TempDir(), Accounts: launcherAccountStore{account: accounts.Account{ID: "acc-1", Provider: providers.Codex, CredentialsRef: "managed"}}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("cancellation should be handled by child signal, got %v", err)
	}
}

type accountErrorStore struct{ err error }

func (s accountErrorStore) Get(context.Context, string) (accounts.Account, error) {
	return accounts.Account{}, s.err
}
