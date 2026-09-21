package codex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
)

// startAttempt is a cancellable login reservation: published before
// any subprocess exists so shutdown can kill attempts that never
// became sessions.
type startAttempt struct {
	cancel context.CancelFunc
}

// mergeCtx returns a context cancelled when either parent is done.
// The caller must call stop; it releases the relay goroutine.
func mergeCtx(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-a.Done():
			cancel()
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// StartLogin begins a browser or device login for a Codex profile. At
// most one session runs per account; a second start is a conflict.
//
// Lifetime: the app-server process belongs to the login session, not to
// the HTTP request that started it. StartLogin creates a detached
// session context before spawning; the login/start call itself races
// HTTP cancellation (aborting the start) against the session lifetime,
// but once login/start succeeds the process outlives the request and
// ends only on completion, cancel, timeout, or service shutdown.
func (s *Service) StartLogin(ctx context.Context, accountID string, method LoginMethod) (LoginSession, error) {
	if method != LoginBrowser && method != LoginDevice {
		return LoginSession{}, fmt.Errorf("unknown login method %q", method)
	}
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return LoginSession{}, err
	}
	// Reserve the login slot before spawning anything: concurrent starts
	// for one account serialize here, so exactly one upstream
	// account/login/start can happen. The reservation carries its own
	// cancel func, so Service.Close kills attempts still in spawn or
	// handshake. The mutex is never held across subprocess or RPC work.
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, busy := s.logins[accountID]; busy {
		s.mu.Unlock()
		sessionCancel()
		return LoginSession{}, ErrLoginInProgress
	}
	if _, busy := s.starting[accountID]; busy {
		s.mu.Unlock()
		sessionCancel()
		return LoginSession{}, ErrLoginInProgress
	}
	s.starting[accountID] = &startAttempt{cancel: sessionCancel}
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.starting, accountID)
		s.mu.Unlock()
	}

	loginType := "chatgpt"
	if method == LoginDevice {
		loginType = "chatgptDeviceCode"
	}
	sess := &loginSession{
		events: make(chan loginCompletedParams, 1),
		done:   make(chan struct{}),
		state:  LoginWaiting,
	}
	sess.view = LoginSession{
		AccountID: accountID,
		Method:    method,
		State:     LoginWaiting,
		StartedAt: time.Now().UTC(),
	}
	sess.client = nil
	sess.cancel = sessionCancel
	notify := func(n notification) {
		if n.Method != "account/login/completed" {
			return
		}
		var p loginCompletedParams
		if err := unmarshalParams(n.Params, &p); err != nil {
			return
		}
		sess.mu.Lock()
		want := sess.loginID
		sess.mu.Unlock()
		if p.LoginID == nil || *p.LoginID != want {
			return // wrong login id (or none yet): ignore
		}
		select {
		case sess.events <- p:
		default:
		}
	}
	// One merged operations context for the whole begin phase: HTTP
	// cancellation aborts spawn/handshake/login-start, while the
	// session context owns the process afterwards.
	beginCtx, stopBegin := mergeCtx(ctx, sessionCtx)
	c, _, err := s.spawn(sessionCtx, beginCtx, accountID, notify)
	if err != nil {
		stopBegin()
		sessionCancel()
		release()
		return LoginSession{}, err
	}
	sess.client = c

	var started loginStartResult
	err = c.Call(beginCtx, "account/login/start", map[string]any{"type": loginType}, &started)
	stopBegin()
	if err != nil {
		sessionCancel()
		c.Close()
		release()
		return LoginSession{}, err
	}
	if started.LoginID == "" {
		sessionCancel()
		c.Close()
		release()
		return LoginSession{}, fmt.Errorf("%w: login/start returned no loginId", ErrCodexProtocol)
	}
	sess.mu.Lock()
	sess.loginID = started.LoginID
	sess.view.LoginID = started.LoginID
	if started.AuthURL != nil {
		sess.view.AuthURL = *started.AuthURL
	}
	if started.VerificationURL != nil {
		sess.view.VerificationURL = *started.VerificationURL
	}
	if started.UserCode != nil {
		sess.view.UserCode = *started.UserCode
	}
	sess.mu.Unlock()

	s.mu.Lock()
	// The reservation makes a busy map entry here impossible, but check
	// defensively: never publish over another session.
	if _, busy := s.logins[accountID]; busy {
		s.mu.Unlock()
		sessionCancel()
		c.Close()
		release()
		return LoginSession{}, ErrLoginInProgress
	}
	delete(s.starting, accountID)
	s.logins[accountID] = sess
	delete(s.last, accountID)
	s.mu.Unlock()

	go s.waitLogin(sessionCtx, accountID, sess)
	return sess.snapshot(), nil
}

// verifyTimeout bounds the post-notification verification subprocess.
// It derives from the session context, so shutdown, cancel, and session
// timeout stop verification instead of abandoning it.
const verifyTimeout = 30 * time.Second

// waitLogin resolves one session: success notification → verify, bind,
// and sync identity; failure/timeout/cancel → terminal state with
// process cleanup. Sessions never persist.
func (s *Service) waitLogin(ctx context.Context, accountID string, sess *loginSession) {
	defer sess.client.Close()
	defer close(sess.done)
	deadline := time.After(loginTimeout)
	select {
	case <-ctx.Done():
		sess.setState(LoginCancelled, "login cancelled")
		s.removeLogin(accountID, sess)
		return
	case <-deadline:
		sess.setState(LoginExpired, "login timed out")
		s.removeLogin(accountID, sess)
		return
	case ev := <-sess.events:
		if ev.Success == nil || !*ev.Success {
			// Provider failure text is internal diagnostics only;
			// the public terminal message stays stable and generic.
			sess.setState(LoginFailed, "login failed")
			s.removeLogin(accountID, sess)
			return
		}
	}
	// Success claimed by notification is not trusted blindly: verify a
	// live connected account, refuse insecure storage, then bind.
	// From this point on the provider credential may already exist, so
	// every failure path reconciles (best-effort logout) before
	// reporting. Terminal errors stay sanitized: internal paths,
	// subprocess errors, RPC payloads, and stderr never reach the poll
	// API.
	vctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	if err := s.completeLogin(vctx, accountID, sess); err != nil {
		var dup *duplicateError
		if errors.As(err, &dup) {
			s.failDuplicate(accountID, sess, dup.pid)
		} else {
			s.reconcileOrphan(accountID)
			sess.setState(LoginFailed, publicLoginError(err))
		}
	}
	s.removeLogin(accountID, sess)
}

// duplicateError carries the colliding upstream identity past the
// commit so the waiter can reconcile and report without re-querying.
type duplicateError struct {
	pid string
}

func (e *duplicateError) Error() string { return "duplicate provider account" }

func (e *duplicateError) Unwrap() error { return accounts.ErrProviderAccountAlreadyConnected }

// failDuplicate handles a won-but-duplicate login: best-effort logout
// runs only in this profile's managed home (the already-connected
// profile is untouched), and the terminal state names the existing
// local label when it can be resolved cleanly. The opaque upstream ID
// never reaches the UI.
func (s *Service) failDuplicate(accountID string, sess *loginSession, pid string) {
	s.reconcileOrphan(accountID)
	msg := "This Codex account is already connected."
	if label, err := s.accounts.ConnectedLabel(context.Background(), "codex", pid); err == nil && label != "" {
		msg = "This Codex account is already connected as \"" + label + "\"."
	}
	sess.mu.Lock()
	sess.view.ErrorCode = "duplicate_provider_account"
	sess.mu.Unlock()
	sess.setState(LoginFailed, msg)
}

// completeLogin verifies the login against a live account/read, checks
// managed storage safety, and commits binding plus identity atomically.
// Any failure leaves the profile exactly as it was (fail closed). The
// caller reconciles post-success failures via reconcileOrphan.
func (s *Service) completeLogin(ctx context.Context, accountID string, sess *loginSession) error {
	c, home, err := s.spawn(ctx, ctx, accountID, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	info, err := readAccount(ctx, c, home)
	if err != nil {
		return err
	}
	if !info.Connected {
		return fmt.Errorf("%w: login completed but account is not connected", ErrCodexProtocol)
	}
	// Duplicate prevention is authoritative only when Codex exposes the
	// upstream account identity. A failed or empty accountId leaves the
	// binding unknown and the login still succeeds — email is never
	// used as a hard uniqueness key.
	providerAccountID := ""
	var rl rateLimitsResult
	if err := c.Call(ctx, "account/rateLimits/read", nil, &rl); err == nil && rl.AccountID != nil {
		providerAccountID = strings.TrimSpace(*rl.AccountID)
	}
	_, err = s.accounts.CompleteProviderConnection(ctx, accountID, BindingRefFor(accountID), info.Email, providerAccountID)
	if err != nil {
		if errors.Is(err, accounts.ErrProviderAccountAlreadyConnected) {
			return &duplicateError{pid: providerAccountID}
		}
		return err
	}
	s.dropQuotaCache(accountID)
	sess.setState(LoginSucceeded, "")
	return nil
}

// cleanupTimeout bounds the detached orphan-reconciliation logout. It
// is deliberately short: reconciliation is best-effort, never a
// background job, and must not stall shutdown.
const cleanupTimeout = 8 * time.Second

// reconcileOrphan best-effort logs out after provider success when the
// local commit did not complete, so no unmanaged Codex credential
// survives next to an unbound profile. It runs on a detached bounded
// cleanup context — never on an already-cancelled session context —
// and stays internal: the session still reports the generic failed
// state. Keyring and SQLite are not one atomic transaction and this is
// not presented as such.
func (s *Service) reconcileOrphan(accountID string) {
	rctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	c, _, err := s.spawn(rctx, rctx, accountID, nil)
	if err != nil {
		return
	}
	defer c.Close()
	var ignored struct{}
	_ = c.Call(rctx, "account/logout", nil, &ignored)
}

func (s *Service) removeLogin(accountID string, sess *loginSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.logins[accountID]; ok && cur == sess {
		delete(s.logins, accountID)
		s.last[accountID] = sess.snapshot()
	}
}

// GetLogin returns the live session view, the last terminal view, or
// idle when no login ever ran for the account.
func (s *Service) GetLogin(accountID string) LoginSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.logins[accountID]; ok {
		return sess.snapshot()
	}
	if last, ok := s.last[accountID]; ok {
		return last
	}
	return LoginSession{AccountID: accountID, State: LoginIdle}
}

// CancelLogin issues account/login/cancel for the running session and
// cleans up. If the process is already gone the state still resolves
// deterministically to cancelled.
func (s *Service) CancelLogin(ctx context.Context, accountID, loginID string) (LoginSession, error) {
	s.mu.Lock()
	sess, ok := s.logins[accountID]
	s.mu.Unlock()
	if !ok {
		return LoginSession{AccountID: accountID, State: LoginIdle}, nil
	}
	if sess.view.LoginID != loginID {
		return sess.snapshot(), fmt.Errorf("%w: unknown login id", ErrCodexProtocol)
	}
	var res struct {
		Status string `json:"status"`
	}
	_ = sess.client.Call(ctx, "account/login/cancel", map[string]any{"loginId": loginID}, &res)
	if sess.cancel != nil {
		sess.cancel()
	}
	<-sess.done
	sess.setState(LoginCancelled, "")
	s.removeLogin(accountID, sess)
	return sess.snapshot(), nil
}

// Logout disconnects a managed account: logout over a fresh app-server,
// verified disconnected read, then binding cleared. Any failure keeps
// the binding (fail closed); the profile itself always remains.
func (s *Service) Logout(ctx context.Context, accountID string) error {
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return err
	}
	c, home, err := s.spawn(ctx, ctx, accountID, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	var ignored struct{}
	if err := c.Call(ctx, "account/logout", nil, &ignored); err != nil {
		return err
	}
	info, err := readAccount(ctx, c, home)
	if err != nil {
		return err
	}
	if info.Connected {
		return fmt.Errorf("%w: account still connected after logout", ErrCodexProtocol)
	}
	if err := s.accounts.SetCredentialBinding(ctx, accountID, ""); err != nil {
		return err
	}
	// A later login may connect a different ChatGPT identity under this
	// profile: drop the previous identity's quota snapshot so it can
	// never be served fresh within its TTL.
	s.dropQuotaCache(accountID)
	return nil
}

// Close cancels all pending login sessions. Called on service shutdown.
// closeWaitTimeout bounds the whole service shutdown: starting
// attempts and sessions are cancelled first, then Close waits for
// session cleanup only up to this deadline.
const closeWaitTimeout = 30 * time.Second

// Close cancels starting attempts and active sessions, then waits
// boundedly for session cleanup. Starting attempts die with their
// contexts (their StartLogin callers finish the cleanup); sessions
// resolve through the normal waiter path.
func (s *Service) Close() {
	s.mu.Lock()
	for _, st := range s.starting {
		st.cancel()
	}
	sessions := make([]*loginSession, 0, len(s.logins))
	for _, sess := range s.logins {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	deadline := time.After(closeWaitTimeout)
	for _, sess := range sessions {
		select {
		case <-sess.done:
		case <-deadline:
			return
		}
		if sess.client != nil {
			sess.client.Close()
		}
	}
}

// publicLoginError maps completion failures to stable, bounded public
// messages. Anything unrecognized becomes a generic verification
// failure so paths, subprocess output, and protocol text never leak.
func publicLoginError(err error) string {
	switch {
	case errors.Is(err, ErrCredentialStoreUnavailable):
		return "managed credential storage is unsafe"
	case errors.Is(err, ErrCodexNotConnected):
		return "login completed but the account is not connected"
	case errors.Is(err, ErrCodexProtocol):
		return "login verification failed"
	default:
		return "login verification failed"
	}
}

// sanitizeError keeps a bounded, single-line failure description from
// upstream failure notifications. Raw protocol dumps are never stored.
func sanitizeError(s string) string {
	if len(s) > 300 {
		s = s[:300]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' || s[i] == '\t' {
			out = append(out, ' ')
		} else {
			out = append(out, s[i])
		}
	}
	return string(out)
}
