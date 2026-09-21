package codex

import (
	"context"
	"fmt"
	"time"
)

// StartLogin begins a browser or device login for a Codex profile. At
// most one session runs per account; a second start is a conflict.
// The session's app-server process stays alive until the flow completes,
// is cancelled, times out, or the service shuts down.
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

	notify := func(n notification) {
		if n.Method != "account/login/completed" {
			return
		}
		var p loginCompletedParams
		if err := unmarshalParams(n.Params, &p); err != nil {
			return
		}
		if p.LoginID == nil || *p.LoginID != sess.view.LoginID {
			return // wrong login id: ignore
		}
		select {
		case sess.events <- p:
		default:
		}
	}
	c, _, err := s.spawn(ctx, accountID, notify)
	if err != nil {
		return LoginSession{}, err
	}
	sess.client = c

	var started loginStartResult
	if err := c.Call(ctx, "account/login/start", map[string]any{"type": loginType}, &started); err != nil {
		c.Close()
		return LoginSession{}, err
	}
	if started.LoginID == "" {
		c.Close()
		return LoginSession{}, fmt.Errorf("%w: login/start returned no loginId", ErrCodexProtocol)
	}
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

	s.mu.Lock()
	if _, busy := s.logins[accountID]; busy {
		s.mu.Unlock()
		c.Close()
		return LoginSession{}, ErrLoginInProgress
	}
	s.logins[accountID] = sess
	delete(s.last, accountID)
	s.mu.Unlock()

	sessionCtx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
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
	if err := s.completeLogin(context.Background(), accountID, sess); err != nil {
		sess.setState(LoginFailed, err.Error())
	}
	s.removeLogin(accountID, sess)
}

// completeLogin verifies the login against a live account/read, checks
// managed storage safety, links credentials_ref, and syncs identity.
// Any failure leaves the profile unbound (fail closed).
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
	if err := s.accounts.SetCredentialBinding(ctx, accountID, BindingRefFor(accountID)); err != nil {
		return err
	}
	if _, err := s.accounts.SyncProviderIdentity(ctx, accountID, info.Email); err != nil {
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

// sanitizeError keeps only a bounded, single-line failure description
// from upstream. Raw protocol dumps are never stored.
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
