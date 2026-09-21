package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Timeouts for one app-server process.
const (
	requestTimeout = 30 * time.Second
	// maxStderr bounds process diagnostics; stderr is internal only and
	// is never forwarded to API responses.
	maxStderr = 32 * 1024
	// maxLine bounds one NDJSON message.
	maxLine = 4 << 20
)

// startupTimeout bounds the initialize handshake. It is a variable (not
// a constant) so tests can shrink it; production always uses the
// default.
var startupTimeout = 20 * time.Second

// closeKillTimeout bounds the graceful exit wait in Close before the
// process is killed. Variable for tests; production uses the default.
var closeKillTimeout = 5 * time.Second

// Client is a JSON-RPC-ish stdio client for one `codex app-server`
// process. One client owns exactly one process; login sessions that
// must outlive a single call keep their client alive instead of
// sharing one.
//
// Lifetime: exactly one goroutine ever calls cmd.Wait (see reap); Close
// and context cancellation share that single reaper path, so Wait is
// never invoked twice.
type Client struct {
	binary string
	home   string
	info   clientInfo
	// ExtraEnv appends explicit KEY=VALUE pairs after the restricted
	// environment. Production leaves it empty; tests use it to drive
	// the fake executable (which restrictedEnv would otherwise strip).
	ExtraEnv []string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	nextID   atomic.Int64
	pending  map[string]chan rpcResult
	notify   func(notification)
	closed   chan struct{}
	closeDo  sync.Once
	waitDone chan error
	stderr   *boundedBuffer
}

type rpcResult struct {
	result json.RawMessage
	rpcErr *rpcError
}

// boundedBuffer keeps the tail of stderr for diagnostics. The stored
// contents never exceed maxStderr bytes, no matter the write pattern.
type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, _ := b.buf.Write(p)
	if over := b.buf.Len() - maxStderr; over > 0 {
		b.buf.Next(over)
	}
	return n, nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// NewClient builds an unstarted client. notify receives server
// notifications and must return quickly and never block.
func NewClient(binary, codexHome, clientVersion string, notify func(notification)) *Client {
	return &Client{
		binary:   binary,
		home:     codexHome,
		info:     aiLBClientInfo(clientVersion),
		pending:  map[string]chan rpcResult{},
		notify:   notify,
		closed:   make(chan struct{}),
		waitDone: make(chan error, 1),
		stderr:   &boundedBuffer{},
	}
}

// Start spawns `codex app-server` (binary invoked directly, no shell),
// performs the initialize handshake, and sends initialized.
//
// Two contexts separate concerns: lifetime owns the process (cancellation
// kills it; it can never orphan) while ops bounds the handshake. Login
// sessions spawn with a detached lifetime and a request-merged ops
// context, so HTTP cancellation aborts startup without killing a
// session that already started.
func (c *Client) Start(lifetime, ops context.Context) error {
	startCtx, cancel := context.WithTimeout(ops, startupTimeout)
	defer cancel()

	cmd := exec.CommandContext(lifetime, c.binary, "app-server")
	env := restrictedEnv(c.home)
	env = append(env, c.ExtraEnv...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("%w: stdin pipe: %v", ErrCodexProcessFailed, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: stdout pipe: %v", ErrCodexProcessFailed, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("%w: stderr pipe: %v", ErrCodexProcessFailed, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: spawn: %v", ErrCodexProcessFailed, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.mu.Unlock()

	go func() {
		_, _ = io.Copy(c.stderr, stderr)
	}()
	go c.readLoop(stdout)
	go c.reap(cmd)

	var initRes initializeResult
	if err := c.call(startCtx, "initialize", map[string]any{"clientInfo": c.info}, &initRes); err != nil {
		_ = c.Close()
		return err
	}
	if err := c.notifyServer(startCtx, "initialized", nil); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

// reap is the single process reaper: exactly one goroutine per client
// ever calls cmd.Wait. It records the exit, fails pending requests,
// and unblocks Close. Context cancellation reaches the process through
// exec.CommandContext and lands here like any other exit.
func (c *Client) reap(cmd *exec.Cmd) {
	err := cmd.Wait()
	c.failAll(fmt.Errorf("%w: process exited: %v", ErrCodexProcessFailed, err))
	c.waitDone <- err
	close(c.waitDone)
}

// restrictedEnv inherits a minimal environment with an explicit
// CODEX_HOME override. No ai-lb keys, tokens, or unrelated provider
// credentials are passed; PATH/HOME are preserved so Codex runs
// normally instead of being broken by over-sanitization.
func restrictedEnv(codexHome string) []string {
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TMPDIR": true,
		"TEMP": true, "TMP": true, "SystemRoot": true, "windir": true,
		"USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
		"XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true,
	}
	out := []string{"CODEX_HOME=" + codexHome}
	for _, kv := range os.Environ() {
		k := kv[:strings.Index(kv, "=")]
		if keep[k] {
			out = append(out, kv)
		}
	}
	return out
}

// Call performs one request/response round trip with a timeout. Unknown
// response fields are ignored by the caller's decode.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.call(callCtx, method, params, out)
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan rpcResult, 1)

	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return fmt.Errorf("%w: client closed", ErrCodexProcessFailed)
	default:
	}
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	msg, err := json.Marshal(rpcRequest{ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: encode %s: %v", ErrCodexProtocol, method, err)
	}
	if _, err := c.writeLine(msg); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: %s: %v", ErrCodexProcessFailed, method, ctx.Err())
	case res := <-ch:
		if res.rpcErr != nil {
			return classifyRPC(res.rpcErr)
		}
		if out != nil && len(res.result) > 0 {
			dec := json.NewDecoder(bytes.NewReader(res.result))
			// Forward-compatible: additive upstream fields ignored.
			if err := dec.Decode(out); err != nil {
				return fmt.Errorf("%w: decode %s: %v", ErrCodexProtocol, method, err)
			}
		}
		return nil
	}
}

// notifyServer sends a fire-and-forget notification.
func (c *Client) notifyServer(ctx context.Context, method string, params any) error {
	msg, err := json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("%w: encode %s: %v", ErrCodexProtocol, method, err)
	}
	_, err = c.writeLineCtx(ctx, msg)
	return err
}

func (c *Client) writeLine(msg []byte) (int, error) {
	return c.writeLineCtx(context.Background(), msg)
}

func (c *Client) writeLineCtx(ctx context.Context, msg []byte) (int, error) {
	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()
	if stdin == nil {
		return 0, fmt.Errorf("%w: not started", ErrCodexProcessFailed)
	}
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() {
		n, err := stdin.Write(append(msg, '\n'))
		ch <- res{n, err}
	}()
	select {
	case <-ctx.Done():
		return 0, fmt.Errorf("%w: write: %v", ErrCodexProcessFailed, ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return r.n, fmt.Errorf("%w: write: %v", ErrCodexProcessFailed, r.err)
		}
		return r.n, nil
	}
}

func (c *Client) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		c.dispatch(line)
	}
	c.failAll(fmt.Errorf("%w: stdout closed", ErrCodexProcessFailed))
}

func (c *Client) dispatch(line []byte) {
	var probe struct {
		ID     any             `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&probe); err != nil {
		return // malformed line: ignore, never crash the reader
	}
	if probe.Method != "" && probe.ID == nil {
		if c.notify != nil {
			var n notification
			if err := json.Unmarshal(line, &n); err == nil {
				c.notify(n)
			}
		}
		return
	}
	if probe.ID == nil {
		return
	}
	key := fmt.Sprintf("%v", probe.ID)
	c.mu.Lock()
	ch, ok := c.pending[key]
	c.mu.Unlock()
	if !ok {
		return // unexpected response id: ignore
	}
	select {
	case ch <- rpcResult{result: probe.Result, rpcErr: probe.Error}:
	default:
	}
}

func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, ch := range c.pending {
		select {
		case ch <- rpcResult{rpcErr: &rpcError{Code: -32000, Message: err.Error()}}:
		default:
		}
		delete(c.pending, key)
	}
}

// Close terminates the process through the single reaper path: close
// stdin, wait briefly for the reaped exit, kill on expiry and wait
// again. It is safe to call twice and never invokes Wait itself.
// Stderr tail is available to the owner via Stderr() for sanitized
// diagnostics only.
func (c *Client) Close() error {
	var cmd *exec.Cmd
	c.closeDo.Do(func() {
		close(c.closed)
		c.mu.Lock()
		cmd = c.cmd
		stdin := c.stdin
		c.stdin = nil
		c.mu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
	})
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	select {
	case <-c.waitDone:
		return nil
	case <-time.After(closeKillTimeout):
		_ = cmd.Process.Kill()
		<-c.waitDone
		return nil
	}
}

// Stderr returns the bounded stderr tail for sanitized diagnostics. It
// may contain anything the subprocess printed, so it is internal only
// and must never reach API responses or logs verbatim.
func (c *Client) Stderr() string {
	return c.stderr.String()
}
