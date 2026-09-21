package control

// Stub codex backend for HTTP-layer tests: canned subprocess-free
// answers exercising routing, validation, DTO mapping, and error codes.
// Real app-server behavior is covered in package codex with the fake
// executable.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dustincorder/ai-lb/internal/providers/codex"
)

type stubCodex struct {
	detection  codex.Detection
	compatible bool
	login      codex.LoginSession
	loginErr   error
	account    codex.AccountInfo
	accountErr error
	quota      codex.QuotaSnapshot
	quotaErr   error
	logoutErr  error
	cancelled  []string
	loggedOut  []string
}

func (s *stubCodex) Detect(_ context.Context) codex.Detection { return s.detection }
func (s *stubCodex) Compatible(_ context.Context) bool        { return s.compatible }
func (s *stubCodex) StartLogin(_ context.Context, _ string, _ codex.LoginMethod) (codex.LoginSession, error) {
	if s.loginErr != nil {
		return codex.LoginSession{}, s.loginErr
	}
	return s.login, nil
}
func (s *stubCodex) GetLogin(_ string) codex.LoginSession { return s.login }
func (s *stubCodex) CancelLogin(_ context.Context, accountID, loginID string) (codex.LoginSession, error) {
	s.cancelled = append(s.cancelled, loginID)
	s.login.State = codex.LoginCancelled
	return s.login, nil
}
func (s *stubCodex) ReadAccount(_ context.Context, _ string) (codex.AccountInfo, error) {
	if s.accountErr != nil {
		return codex.AccountInfo{}, s.accountErr
	}
	return s.account, nil
}
func (s *stubCodex) ReadRateLimits(_ context.Context, _ string) (codex.QuotaSnapshot, error) {
	if s.quotaErr != nil {
		return codex.QuotaSnapshot{}, s.quotaErr
	}
	return s.quota, nil
}
func (s *stubCodex) RefreshRateLimits(_ context.Context, _ string) (codex.QuotaSnapshot, error) {
	if s.quotaErr != nil {
		return codex.QuotaSnapshot{}, s.quotaErr
	}
	return s.quota, nil
}
func (s *stubCodex) Logout(_ context.Context, accountID string) error {
	if s.logoutErr != nil {
		return s.logoutErr
	}
	s.loggedOut = append(s.loggedOut, accountID)
	return nil
}
func (s *stubCodex) Close() {}

// codexTestServer builds a control server whose Codex backend is the
// stub, plus one Codex profile id for route tests.
func codexTestServer(t *testing.T, stub *stubCodex) (*httptest.Server, string) {
	t.Helper()
	s := testServer(t)
	s.Codex = stub
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	code, created := doJSON(t, http.MethodPost, srv.URL+"/api/accounts",
		`{"provider":"codex","label":"C"}`)
	if code != http.StatusCreated {
		t.Fatalf("setup profile: %d %v", code, created)
	}
	return srv, created["id"].(string)
}

func TestCodexProviderStatus(t *testing.T) {
	stub := &stubCodex{
		detection:  codex.Detection{Installed: true, Path: "/usr/bin/codex", Version: "codex-cli 0.1"},
		compatible: true,
	}
	srv, _ := codexTestServer(t, stub)
	code, body := getJSON(t, srv.URL+"/api/providers/codex/status")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if body["installed"] != true || body["app_server_compatible"] != true {
		t.Errorf("unexpected status body: %v", body)
	}
	if _, ok := body["path"]; !ok {
		t.Errorf("status must include path: %v", body)
	}
}

func TestCodexProviderNotInstalled(t *testing.T) {
	stub := &stubCodex{detection: codex.Detection{Installed: false, Error: "codex CLI not installed"}}
	srv, _ := codexTestServer(t, stub)
	code, body := getJSON(t, srv.URL+"/api/providers/codex/status")
	if code != http.StatusOK {
		t.Fatalf("missing CLI is data, not 500: %d", code)
	}
	if body["installed"] != false {
		t.Errorf("expected installed=false: %v", body)
	}
}

func TestCodexLoginFlows(t *testing.T) {
	stub := &stubCodex{
		login: codex.LoginSession{
			AccountID: "x", LoginID: "login-1", Method: codex.LoginBrowser,
			State: codex.LoginWaiting, AuthURL: "https://example.com/auth",
		},
	}
	srv, id := codexTestServer(t, stub)

	// Browser start.
	code, started := doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+id+"/codex/login", `{"method":"browser"}`)
	if code != http.StatusCreated || started["auth_url"] != "https://example.com/auth" {
		t.Fatalf("browser start: %d %v", code, started)
	}
	// Poll.
	code, polled := doJSON(t, http.MethodGet, srv.URL+"/api/accounts/"+id+"/codex/login", "")
	if code != http.StatusOK || polled["login_id"] != "login-1" {
		t.Fatalf("poll: %d %v", code, polled)
	}
	// Cancel with explicit id.
	code, cancelled := doJSON(t, http.MethodDelete, srv.URL+"/api/accounts/"+id+"/codex/login", `{"login_id":"login-1"}`)
	if code != http.StatusOK || cancelled["state"] != string(codex.LoginCancelled) {
		t.Fatalf("cancel: %d %v", code, cancelled)
	}
	if len(stub.cancelled) != 1 || stub.cancelled[0] != "login-1" {
		t.Errorf("cancel id not forwarded: %v", stub.cancelled)
	}
}

func TestCodexLoginValidation(t *testing.T) {
	stub := &stubCodex{loginErr: codex.ErrCodexNotInstalled}
	srv, id := codexTestServer(t, stub)

	// Missing binary → controlled error, not 500.
	code, body := doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+id+"/codex/login", `{"method":"browser"}`)
	if code != http.StatusServiceUnavailable || body["error"] != "codex_not_installed" {
		t.Errorf("missing binary: %d %v", code, body)
	}
	// Bad method rejected.
	code, body = doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+id+"/codex/login", `{"method":"sms"}`)
	if code != http.StatusUnprocessableEntity {
		t.Errorf("bad method: %d %v", code, body)
	}
	// Wrong provider rejected.
	code, created := doJSON(t, http.MethodPost, srv.URL+"/api/accounts", `{"provider":"antigravity","label":"A"}`)
	if code != http.StatusCreated {
		t.Fatalf("setup antigravity: %d", code)
	}
	aid := created["id"].(string)
	code, body = doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+aid+"/codex/login", `{"method":"browser"}`)
	if code != http.StatusUnprocessableEntity || body["error"] != "invalid_provider" {
		t.Errorf("non-codex login: %d %v", code, body)
	}
	// Unknown account.
	code, body = doJSON(t, http.MethodGet, srv.URL+"/api/accounts/nope/codex/status", "")
	if code != http.StatusNotFound {
		t.Errorf("unknown account: %d %v", code, body)
	}
}

func TestCodexStatusAndQuota(t *testing.T) {
	used, remaining := 25, 75
	stub := &stubCodex{
		account: codex.AccountInfo{
			Connected: true, AuthMode: "chatgpt", Email: "u@example.com",
			PlanType: "plus", RequiresOpenaiAuth: true,
			ObservedAt: time.Now().UTC(),
		},
		quota: codex.QuotaSnapshot{
			Provider: "codex", UpdatedAt: time.Now().UTC(),
			Windows: []codex.QuotaWindow{{
				LimitID: "codex", LimitName: "Codex",
				UsedPercent: &used, RemainingPercent: &remaining,
			}},
		},
	}
	srv, id := codexTestServer(t, stub)

	code, status := doJSON(t, http.MethodGet, srv.URL+"/api/accounts/"+id+"/codex/status", "")
	if code != http.StatusOK {
		t.Fatalf("status: %d %v", code, status)
	}
	if status["connected"] != true || status["email"] != "u@example.com" || status["plan_type"] != "plus" {
		t.Errorf("status DTO wrong: %v", status)
	}
	q, ok := status["quota"].(map[string]any)
	if !ok {
		t.Fatalf("connected status must include quota: %v", status)
	}
	windows := q["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("quota windows: %v", q)
	}
	w := windows[0].(map[string]any)
	if w["used_percent"] != float64(25) || w["remaining_percent"] != float64(75) {
		t.Errorf("quota percents wrong: %v", w)
	}

	// Refresh route.
	code, rq := doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+id+"/codex/refresh", "")
	if code != http.StatusOK {
		t.Fatalf("refresh: %d %v", code, rq)
	}

	// Logout.
	code, lo := doJSON(t, http.MethodPost, srv.URL+"/api/accounts/"+id+"/codex/logout", "")
	if code != http.StatusOK || lo["connected"] != false {
		t.Fatalf("logout: %d %v", code, lo)
	}
	if len(stub.loggedOut) != 1 {
		t.Errorf("logout not forwarded: %v", stub.loggedOut)
	}
}

func TestCodexResponsesNeverLeak(t *testing.T) {
	stub := &stubCodex{
		account: codex.AccountInfo{Connected: true, Email: "u@example.com"},
		quota:   codex.QuotaSnapshot{Provider: "codex", Windows: []codex.QuotaWindow{}},
	}
	srv, id := codexTestServer(t, stub)
	for _, tc := range []struct{ method, path, payload string }{
		{"GET", "/api/providers/codex/status", ""},
		{"POST", "/api/accounts/" + id + "/codex/login", `{"method":"device"}`},
		{"GET", "/api/accounts/" + id + "/codex/status", ""},
		{"POST", "/api/accounts/" + id + "/codex/refresh", ""},
	} {
		code, body := doJSON(t, tc.method, srv.URL+tc.path, tc.payload)
		if code >= 400 {
			t.Fatalf("%s %s: %d %v", tc.method, tc.path, code, body)
		}
		raw, _ := json.Marshal(body)
		lowered := strings.ToLower(string(raw))
		// Field names auth_mode / requires_openai_auth are legitimate;
		// secret-shaped content (refs, paths, blobs) is forbidden.
		for _, leak := range []string{"credentials_ref", "auth.json", "codex-home", "keyring key", ".codex/", "apiKey", "access_token", "refresh_token"} {
			if strings.Contains(lowered, leak) {
				t.Errorf("%s %s leaks %q: %s", tc.method, tc.path, leak, raw)
			}
		}
	}
}
