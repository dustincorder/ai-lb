package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
)

// BindingRef prefixes the opaque credential binding for managed Codex
// accounts. It carries no path and no secret; the actual lifecycle
// belongs to the Codex app-server and its keyring storage.
const bindingPrefix = "codex-managed:"

// BindingRefFor returns the opaque binding stored as credentials_ref
// after a verified login.
func BindingRefFor(accountID string) string {
	return bindingPrefix + accountID
}

// loginTimeout bounds one browser/device login session.
const loginTimeout = 10 * time.Minute

// quotaCacheTTL bounds the in-memory quota snapshot reuse.
const quotaCacheTTL = 20 * time.Second

// LoginMethod selects the ChatGPT login flow.
type LoginMethod string

const (
	// LoginBrowser opens an auth URL (account/login/start type chatgpt).
	LoginBrowser LoginMethod = "browser"
	// LoginDevice shows a user code (type chatgptDeviceCode).
	LoginDevice LoginMethod = "device"
)

// LoginState is the observable session lifecycle. Sessions live in
// memory only, never in SQLite.
type LoginState string

const (
	LoginIdle      LoginState = "idle"
	LoginWaiting   LoginState = "waiting"
	LoginSucceeded LoginState = "succeeded"
	LoginFailed    LoginState = "failed"
	LoginCancelled LoginState = "cancelled"
	LoginExpired   LoginState = "expired"
)

// LoginSession is the public view of an active login.
type LoginSession struct {
	AccountID       string      `json:"account_id"`
	LoginID         string      `json:"login_id"`
	Method          LoginMethod `json:"method"`
	State           LoginState  `json:"state"`
	AuthURL         string      `json:"auth_url,omitempty"`
	VerificationURL string      `json:"verification_url,omitempty"`
	UserCode        string      `json:"user_code,omitempty"`
	Error           string      `json:"error,omitempty"`
	StartedAt       time.Time   `json:"started_at"`
}

// AccountInfo is the normalized account/read result.
type AccountInfo struct {
	Connected          bool      `json:"connected"`
	AuthMode           string    `json:"auth_mode,omitempty"`
	Email              string    `json:"email,omitempty"`
	PlanType           string    `json:"plan_type,omitempty"`
	RequiresOpenaiAuth bool      `json:"requires_openai_auth"`
	ObservedAt         time.Time `json:"observed_at"`
}

// QuotaWindow is one normalized limit bucket. Percent pointers are nil
// when upstream did not report a value — unknown is shown as unknown,
// never as zero.
type QuotaWindow struct {
	LimitID            string `json:"limit_id,omitempty"`
	LimitName          string `json:"limit_name,omitempty"`
	UsedPercent        *int   `json:"used_percent,omitempty"`
	RemainingPercent   *int   `json:"remaining_percent,omitempty"`
	WindowDurationMins *int64 `json:"window_duration_minutes,omitempty"`
	ResetAt            *int64 `json:"reset_at,omitempty"`
}

// QuotaSnapshot is the normalized rate-limits read.
type QuotaSnapshot struct {
	Provider  string        `json:"provider"`
	Windows   []QuotaWindow `json:"windows"`
	UpdatedAt time.Time     `json:"updated_at"`
	Stale     bool          `json:"stale"`
}

type loginSession struct {
	view   LoginSession
	client *Client
	events chan loginCompletedParams
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	// loginID mirrors view.LoginID for the notification filter; it is
	// read from the reader goroutine, so all access holds mu.
	loginID string
	state   LoginState
	failure string
}

func (s *loginSession) setState(st LoginState, failure string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
	s.failure = failure
	s.view.State = st
	s.view.Error = failure
}

func (s *loginSession) snapshot() LoginSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

type cachedQuota struct {
	snapshot QuotaSnapshot
	at       time.Time
}

// Service runs managed Codex operations for account profiles.
type Service struct {
	binary   string
	dataDir  string
	version  string
	accounts *accounts.Service
	// ExtraEnv reaches spawned processes after the restricted
	// environment (test fake control vars; empty in production).
	ExtraEnv []string

	mu     sync.Mutex
	logins map[string]*loginSession
	// starting holds cancellable reservations for logins still in
	// spawn/initialize/login-start. A reservation carries its own
	// cancel func, so Service.Close can kill attempts that never
	// became sessions. Never held across subprocess or RPC work.
	starting map[string]*startAttempt
	// last keeps the most recent terminal session view per account so
	// polling observes succeeded/failed/cancelled/expired instead of a
	// disappearing session. Cleared when the next login starts.
	last   map[string]LoginSession
	quotas map[string]cachedQuota
}

// NewService builds the Codex service. binary is the resolved codex path
// (empty when not installed — detection reports that, methods fail with
// ErrCodexNotInstalled). dataDir roots managed homes.
func NewService(binary, dataDir, clientVersion string, acc *accounts.Service) *Service {
	return &Service{
		binary:   binary,
		dataDir:  dataDir,
		version:  clientVersion,
		accounts: acc,
		logins:   map[string]*loginSession{},
		starting: map[string]*startAttempt{},
		last:     map[string]LoginSession{},
		quotas:   map[string]cachedQuota{},
	}
}

func (s *Service) requireBinary() (string, error) {
	if s.binary == "" {
		return "", ErrCodexNotInstalled
	}
	return s.binary, nil
}

// spawn starts an initialized client on the account's managed home.
// lifetime owns the process; ops bounds the initialize handshake.
func (s *Service) spawn(lifetime, ops context.Context, accountID string, notify func(notification)) (*Client, string, error) {
	binary, err := s.requireBinary()
	if err != nil {
		return nil, "", err
	}
	home, err := ManagedHome(s.dataDir, accountID)
	if err != nil {
		return nil, "", err
	}
	c := NewClient(binary, home, s.version, notify)
	c.ExtraEnv = s.ExtraEnv
	if err := c.Start(lifetime, ops); err != nil {
		return nil, "", err
	}
	return c, home, nil
}

// readAccountLocked performs account/read on a live client and
// normalizes the result. Plaintext auth.json in a managed home fails
// the read: storage is unsafe and ai-lb must not claim a safe
// connection. File contents are never inspected.
func readAccount(ctx context.Context, c *Client, home string) (AccountInfo, error) {
	var res accountReadResult
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &res); err != nil {
		return AccountInfo{}, err
	}
	info := AccountInfo{
		RequiresOpenaiAuth: res.RequiresOpenaiAuth,
		ObservedAt:         time.Now().UTC(),
	}
	if res.Account != nil {
		info.Connected = res.Account.Type == "chatgpt" || res.Account.Type == "apiKey"
		info.AuthMode = res.Account.Type
		if res.Account.Email != nil {
			info.Email = *res.Account.Email
		}
		if res.Account.PlanType != nil {
			info.PlanType = *res.Account.PlanType
		}
	}
	if PlaintextAuthPresent(home) {
		return AccountInfo{}, fmt.Errorf("%w: plaintext auth.json in managed home", ErrCredentialStoreUnavailable)
	}
	return info, nil
}

// Detect reports CLI installation.
func (s *Service) Detect(ctx context.Context) Detection {
	return Detect(ctx)
}

// Compatible probes the resolved binary with an isolated throwaway home.
// It never touches any managed or user home.
func (s *Service) Compatible(ctx context.Context) bool {
	if s.binary == "" {
		return false
	}
	dir, err := os.MkdirTemp("", "ai-lb-codex-probe-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	home := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return false
	}
	return Compatible(ctx, s.binary, home, s.version) == nil
}

// ReadAccount returns live account info for a Codex profile.
func (s *Service) ReadAccount(ctx context.Context, accountID string) (AccountInfo, error) {
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return AccountInfo{}, err
	}
	c, home, err := s.spawn(ctx, ctx, accountID, nil)
	if err != nil {
		return AccountInfo{}, err
	}
	defer c.Close()
	return readAccount(ctx, c, home)
}
