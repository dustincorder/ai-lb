package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startFake(t *testing.T, ctx context.Context, extra ...string) *Client {
	t.Helper()
	exe, env := fakeEnv(t, extra...)
	dir := t.TempDir()
	c := NewClient(exe, dir, "test", nil)
	c.ExtraEnv = env[len(os.Environ()):]
	if err := c.Start(ctx); err != nil {
		t.Fatalf("fake Start: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestInitializeHandshake(t *testing.T) {
	ctx := context.Background()
	c := startFake(t, ctx)
	var out struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &out); err != nil {
		t.Fatalf("call after handshake: %v", err)
	}
	// Handshake itself succeeding is the assertion; a bad handshake
	// would have failed Start.
}

func TestRequestResponseMatching(t *testing.T) {
	ctx := context.Background()
	c := startFake(t, ctx)
	var a, b accountReadResult
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &a); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &b); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if a.Account != nil || b.Account != nil {
		t.Errorf("fake default should be disconnected: %+v %+v", a, b)
	}
}

func TestNotificationDispatch(t *testing.T) {
	ctx := context.Background()
	var count atomic.Int64
	dir := t.TempDir()
	c := NewClient(mustExe(t), dir, "test", func(n notification) {
		if n.Method == "account/login/completed" {
			count.Add(1)
		}
	})
	c.ExtraEnv = fakeExtra(t, "AI_LB_FAKE_LOGIN=ok")
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()
	var started loginStartResult
	if err := c.Call(ctx, "account/login/start", map[string]any{"type": "chatgpt"}, &started); err != nil {
		t.Fatalf("login/start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if count.Load() != 1 {
		t.Error("expected exactly one login/completed notification")
	}
}

func mustExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

func fakeExtra(t *testing.T, extra ...string) []string {
	t.Helper()
	base := len(os.Environ())
	_, env := fakeEnv(t, extra...)
	return env[base:]
}

func TestMalformedLineIgnored(t *testing.T) {
	ctx := context.Background()
	c := startFake(t, ctx, "AI_LB_FAKE_BADLINE=1")
	var a accountReadResult
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &a); err != nil {
		t.Fatalf("reader must survive garbage lines: %v", err)
	}
}

func TestUnexpectedResponseIDIgnored(t *testing.T) {
	// Covered implicitly: the fake only answers known ids; dispatch
	// drops anything unmatched. The direct unit check:
	c := &Client{pending: map[string]chan rpcResult{}, closed: make(chan struct{})}
	c.dispatch([]byte(`{"id":999,"result":{}}`)) // must not panic or block
}

func TestProcessExitFailsPending(t *testing.T) {
	// The fake exits on the first input line, so even the initialize
	// handshake must fail instead of hanging.
	exe, env := fakeEnv(t, "AI_LB_FAKE_EXIT_AFTER=0")
	c := NewClient(exe, t.TempDir(), "test", nil)
	c.ExtraEnv = env[len(os.Environ()):]
	if err := c.Start(context.Background()); err == nil {
		t.Error("handshake against an exiting process must fail")
		defer c.Close()
	}
}

func TestCallTimeout(t *testing.T) {
	ctx := context.Background()
	c := startFake(t, ctx, "AI_LB_FAKE_QUIET_AFTER_INIT=1")
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err := c.Call(short, "account/read", nil, nil)
	if err == nil {
		t.Fatal("silent server must time out")
	}
}

func TestContextCancelKillsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := startFake(t, ctx, "AI_LB_FAKE_QUIET_AFTER_INIT=1")
	proc := c.cmd.Process
	if proc == nil {
		t.Fatal("no process")
	}
	cancel()
	// The process is bound to ctx via CommandContext: it must exit
	// without an explicit kill from the test.
	reaped := make(chan struct{}, 1)
	go func() {
		_, _ = proc.Wait()
		reaped <- struct{}{}
	}()
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Error("cancelled context left the process alive")
	}
	c.Close()
}

func TestStderrBounded(t *testing.T) {
	b := &boundedBuffer{}
	for i := 0; i < 100; i++ {
		_, _ = b.Write(make([]byte, 1024))
	}
	if got := len(b.String()); got > maxStderr+1024 {
		t.Errorf("stderr buffer unbounded: %d", got)
	}
}

func TestRestrictedEnv(t *testing.T) {
	t.Setenv("AI_LB_SECRET_SHOULD_NOT_PASS", "1")
	env := restrictedEnv("/tmp/home")
	home := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			home = true
		}
		if strings.HasPrefix(kv, "AI_LB_SECRET_SHOULD_NOT_PASS=") {
			t.Error("unrelated env must not pass to Codex")
		}
	}
	if !home {
		t.Error("CODEX_HOME override missing")
	}
}

func TestDetectNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty of binaries, cross-platform
	d := Detect(context.Background())
	if d.Installed {
		t.Error("empty PATH must report not installed")
	}
}

func TestCompatibleWithFake(t *testing.T) {
	exe, env := fakeEnv(t)
	extra := env[len(os.Environ()):]
	if err := Compatible(context.Background(), exe, t.TempDir(), "test", extra...); err != nil {
		t.Errorf("fake must pass compatibility: %v", err)
	}
	if err := Compatible(context.Background(), filepath.Join(t.TempDir(), "nope"), t.TempDir(), "test"); err == nil {
		t.Error("missing binary must fail compatibility")
	}
}
