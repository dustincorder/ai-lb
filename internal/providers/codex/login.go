package codex

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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
	s.mu.Lock()
	if _, busy := s.logins[accountID]; busy {
		s.mu.Unlock()
		return LoginSession{}, ErrLoginInProgress
	}
	s.mu.Unlock()

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

	// Detached session lifetime: independent of the starting request.
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
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
	c, _, err := s.spawn(sessionCtx, accountID, notify)
	if err != nil {
		sessionCancel()
		return LoginSession{}, err
	}
	sess.client = c

	// The start call aborts if the HTTP request dies mid-start, but the
	// session context keeps the process alive once started.
	startCtx, stopStart := context.WithCancel(sessionCtx)
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			stopStart()
		case <-stopped:
		}
	}()
	var started loginStartResult
	err = c.Call(startCtx, "account/login/start", map[string]any{"type": loginType}, &started)
	stopStart()
	if err != nil {
		sessionCancel()
		c.Close()
		return LoginSession{}, err
	}
	if started.LoginID == "" {
		sessionCancel()
		c.Close()
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
	if _, busy := s.logins[accountID]; busy {
		s.mu.Unlock()
		sessionCancel()
		c.Close()
		return LoginSession{}, ErrLoginInProgress
	}
	s.logins[accountID] = sess
	delete(s.last, accountID)
	s.mu.Unlock()

	sess.cancel = sessionCancel
	go s.waitLogin(sessionCtx, accountID, sess)
	return sess.snapshot(), nil
}

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
			msg := "login failed"
			if ev.Error != nil && *ev.Error != "" {
				msg = sanitizeError(*ev.Error)
			}
			sess.setState(LoginFailed, msg)
			s.removeLogin(accountID, sess)
			return
		}
	}
	// Success claimed by notification is not trusted blindly: verify a
	// live connected account, refuse insecure storage, then bind.
	// Terminal errors are sanitized to stable public messages: internal
	// paths, subprocess errors, RPC payloads, and stderr must never
	// reach the poll API.
	if err := s.completeLogin(context.Background(), accountID, sess); err != nil {
		sess.setState(LoginFailed, publicLoginError(err))
	}
	s.removeLogin(accountID, sess)
}

// completeLogin verifies the login against a live account/read, checks
// managed storage safety, and commits binding plus identity atomically.
// Any failure leaves the profile exactly as it was (fail closed).
func (s *Service) completeLogin(ctx context.Context, accountID string, sess *loginSession) error {
	c, home, err := s.spawn(ctx, accountID, nil)
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
	_, err = s.accounts.CompleteProviderConnection(ctx, accountID, BindingRefFor(accountID), info.Email)
	if err != nil {
		return err
	}
	sess.setState(LoginSucceeded, "")
	return nil
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
	c, home, err := s.spawn(ctx, accountID, nil)
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
	return s.accounts.SetCredentialBinding(ctx, accountID, "")
}

// Close cancels all pending login sessions. Called on service shutdown.
func (s *Service) Close() {
	s.mu.Lock()
	sessions := make([]*loginSession, 0, len(s.logins))
	for _, sess := range s.logins {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		if sess.cancel != nil {
			sess.cancel()
		}
		<-sess.done
		sess.client.Close()
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
