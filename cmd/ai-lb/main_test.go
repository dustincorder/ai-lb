package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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
	if strings.Contains(strings.Join(os.Args, "\x1f"), "\x1fsignal-exit") {
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
		sig := <-interrupted
		if sig == syscall.SIGTERM {
			os.Exit(143)
		}
		os.Exit(130)
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

func TestLauncherForwardsSignals(t *testing.T) {
	for _, test := range []struct {
		name string
		sig  os.Signal
		want int
	}{
		{name: "SIGINT", sig: os.Interrupt, want: 130},
		{name: "SIGTERM", sig: syscall.SIGTERM, want: 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := test.want
			if runtime.GOOS == "windows" {
				want = 1
			}
			signals := make(chan os.Signal, 1)
			go func() {
				time.Sleep(50 * time.Millisecond)
				signals <- test.sig
			}()
			err := launcher.Run(context.Background(), launcher.Options{AccountID: "acc-1", Args: []string{"-test.run=TestLauncherHelperProcess", "signal-exit"}}, launcher.RunConfig{
				Binary: os.Args[0], DataDir: t.TempDir(), Accounts: launcherAccountStore{account: accounts.Account{ID: "acc-1", Provider: providers.Codex, CredentialsRef: "managed"}}, Signals: signals,
			})
			var exitErr *launcher.ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != want {
				t.Fatalf("error = %v, want exit code %d", err, want)
			}
		})
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
		if runtime.GOOS == "windows" {
			var exitErr *launcher.ExitError
			if errors.As(err, &exitErr) {
				return
			}
		}
		t.Fatalf("cancellation should be handled by child signal, got %v", err)
	}
}

type accountErrorStore struct{ err error }

func (s accountErrorStore) Get(context.Context, string) (accounts.Account, error) {
	return accounts.Account{}, s.err
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func testFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestCLIVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runWithIO(context.Background(), []string{"--version"}, &bytes.Buffer{}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run --version: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "ai-lb") || !strings.Contains(out, "commit:") || !strings.Contains(out, "channel:") {
		t.Errorf("unexpected version output: %q", out)
	}
	if stderr.String() != "" {
		t.Errorf("expected empty stderr, got %q", stderr.String())
	}
}

func TestCLIFatalErrorVisibleEvenWithQuiet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runWithIO(context.Background(), []string{"--quiet", "--invalid-flag-that-does-not-exist"}, &bytes.Buffer{}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid flag")
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Errorf("stderr missing flag error: %q", stderr.String())
	}
}

func TestCLILifecyclePresentation(t *testing.T) {
	dir := t.TempDir()
	cPort := testFreePort(t)
	gPort := testFreePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr safeBuffer

	done := make(chan error, 1)
	go func() {
		done <- runWithIO(ctx, []string{
			"--data-dir", dir,
			"--control-port", fmt.Sprintf("%d", cPort),
			"--gateway-port", fmt.Sprintf("%d", gPort),
		}, &bytes.Buffer{}, &stdout, &stderr)
	}()

	// Wait for stderr to receive Ready presentation
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), "Ready.") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("service returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not stop cleanly")
	}

	if stdout.String() != "" {
		t.Errorf("stdout should remain clean for scriptability, got: %q", stdout.String())
	}

	out := stderr.String()
	for _, expected := range []string{
		"ai-lb",
		fmt.Sprintf("http://127.0.0.1:%d", cPort),
		fmt.Sprintf("http://127.0.0.1:%d", gPort),
		dir,
		"Database   ready",
		"Ready.",
		"Shutting down...",
		"Stopped.",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("stderr missing %q, got:\n%s", expected, out)
		}
	}
}

func TestCLILifecycleQuiet(t *testing.T) {
	dir := t.TempDir()
	cPort := testFreePort(t)
	gPort := testFreePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- runWithIO(ctx, []string{
			"--data-dir", dir,
			"--control-port", fmt.Sprintf("%d", cPort),
			"--gateway-port", fmt.Sprintf("%d", gPort),
			"--quiet",
		}, &bytes.Buffer{}, &stdout, &stderr)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("service returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not stop cleanly")
	}

	if stdout.String() != "" {
		t.Errorf("stdout should be empty, got: %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("quiet mode should have empty stderr, got: %q", stderr.String())
	}
}
