package codex

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/providers"
)

func testExtraEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	base := len(os.Environ())
	_, env := fakeEnv(t, extra...)
	return env[base:]
}

func testCodexService(t *testing.T, extra ...string) (*Service, accounts.Account) {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	accSvc := accounts.NewService(providers.Default(), accounts.NewRepository(database.Conn))
	a, err := accSvc.Create(context.Background(), accounts.CreateInput{
		Provider: providers.Codex, Label: "Test",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	svc := NewService(mustExe(t), t.TempDir(), "test", accSvc)
	svc.ExtraEnv = testExtraEnv(t, extra...)
	return svc, a
}

func strPtr(s string) string { return s }

func strRef(s string) *string { return &s }

func intPtr(n int) *int { return &n }

func orZero(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func orZero64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestQuotaMapping(t *testing.T) {
	now := time.Now().UTC()
	used30 := 30
	dur := int64(300)
	reset := now.Add(time.Hour).Unix()
	res := rateLimitsResult{
		RateLimits: &rateLimitSnapshot{
			LimitID:   strRef("codex"),
			LimitName: strRef("Codex"),
			Primary:   &rateLimitWindow{UsedPercent: &used30, WindowDurationMins: &dur, ResetsAt: &reset},
		},
		RateLimitsByLimitID: map[string]rateLimitSnapshot{
			"extra": {Primary: &rateLimitWindow{UsedPercent: intPtr(90)}},
		},
	}
	snap := mapQuota(res, now)
	if snap.Provider != "codex" || snap.Stale {
		t.Errorf("bad snapshot header: %+v", snap)
	}
	if len(snap.Windows) != 2 {
		t.Fatalf("want primary + extra bucket, got %+v", snap.Windows)
	}
	primary := snap.Windows[0]
	if primary.LimitID != "codex" || primary.LimitName != "Codex" {
		t.Errorf("primary ids lost: %+v", primary)
	}
	if orZero(primary.UsedPercent) != 30 || orZero(primary.RemainingPercent) != 70 {
		t.Errorf("used 30 must map to remaining 70: %+v", primary)
	}
	if orZero64(primary.WindowDurationMins) != 300 || orZero64(primary.ResetAt) != reset {
		t.Errorf("window metadata lost: %+v", primary)
	}
	extra := snap.Windows[1]
	if extra.LimitID != "extra" || orZero(extra.RemainingPercent) != 10 {
		t.Errorf("extra bucket wrong: %+v", extra)
	}
}

func TestQuotaUnknownStaysUnknown(t *testing.T) {
	snap := mapQuota(rateLimitsResult{
		RateLimits: &rateLimitSnapshot{Primary: &rateLimitWindow{}},
	}, time.Now().UTC())
	if len(snap.Windows) != 1 {
		t.Fatalf("want one window, got %+v", snap.Windows)
	}
	w := snap.Windows[0]
	if w.UsedPercent != nil || w.RemainingPercent != nil {
		t.Errorf("absent upstream values must stay nil, got %+v", w)
	}
}

func TestQuotaClamp(t *testing.T) {
	over := 150
	snap := mapQuota(rateLimitsResult{
		RateLimits: &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: &over}},
	}, time.Now().UTC())
	w := snap.Windows[0]
	if orZero(w.UsedPercent) != 100 || orZero(w.RemainingPercent) != 0 {
		t.Errorf("150 must clamp to 100/0: %+v", w)
	}
}

func TestReadAccountDisconnected(t *testing.T) {
	svc, a := testCodexService(t)
	info, err := svc.ReadAccount(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("ReadAccount: %v", err)
	}
	if info.Connected {
		t.Error("fake default account must read disconnected")
	}
	if !info.RequiresOpenaiAuth {
		t.Error("requiresOpenaiAuth should pass through")
	}
}

func TestInsecureStorageRefusesConnectedClaim(t *testing.T) {
	svc, a := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"user@example.com","planType":"plus"}`)
	// Plant plaintext auth.json into the managed home through the same
	// path production uses.
	home, err := ManagedHome(svc.dataDir, a.ID)
	if err != nil {
		t.Fatalf("ManagedHome: %v", err)
	}
	if err := os.WriteFile(home+"/auth.json", []byte(`{}`), 0o600); err != nil {
		t.Fatalf("plant auth.json: %v", err)
	}
	if _, err := svc.ReadAccount(context.Background(), a.ID); err == nil {
		t.Error("auth.json in managed home must fail the read")
	}
}

func TestBrowserLoginSuccessBinds(t *testing.T) {
	svc, a := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"user@example.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
	)
	sess, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if sess.LoginID == "" || sess.AuthURL == "" {
		t.Fatalf("browser login must return loginId + authUrl: %+v", sess)
	}
	if sess.State != LoginWaiting {
		t.Fatalf("fresh session must be waiting: %+v", sess)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cur := svc.GetLogin(a.ID)
		if cur.State == LoginSucceeded {
			break
		}
		if cur.State == LoginFailed || cur.State == LoginExpired {
			t.Fatalf("login ended %q: %s", cur.State, cur.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("login did not complete")
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Connected() {
		t.Error("successful login must link credentials_ref")
	}
	if got.Identity != "user@example.com" {
		t.Errorf("identity must sync from account/read, got %q", got.Identity)
	}
}

func TestDeviceLoginReturnsCode(t *testing.T) {
	svc, a := testCodexService(t)
	sess, err := svc.StartLogin(context.Background(), a.ID, LoginDevice)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if sess.VerificationURL == "" || sess.UserCode == "" {
		t.Errorf("device login must return url + code: %+v", sess)
	}
	if _, err := svc.CancelLogin(context.Background(), a.ID, sess.LoginID); err != nil {
		t.Fatalf("CancelLogin: %v", err)
	}
	if cur := svc.GetLogin(a.ID); cur.State != LoginCancelled && cur.State != LoginIdle {
		t.Errorf("cancelled session must resolve, got %q", cur.State)
	}
}

func TestFailedLoginStaysDisconnected(t *testing.T) {
	svc, a := testCodexService(t, "AI_LB_FAKE_LOGIN=fail")
	sess, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	_ = sess
	deadline := time.Now().Add(5 * time.Second)
	for {
		cur := svc.GetLogin(a.ID)
		if cur.State == LoginFailed {
			break
		}
		if cur.State == LoginSucceeded {
			t.Fatal("failed notification must not succeed")
		}
		if time.Now().After(deadline) {
			t.Fatal("login did not resolve")
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() {
		t.Error("failed login must not link credentials")
	}
}

func TestDuplicateLoginRejected(t *testing.T) {
	svc, a := testCodexService(t)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("first StartLogin: %v", err)
	}
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginDevice); err == nil {
		t.Error("second concurrent login must be rejected")
	} else if !errors.Is(err, ErrLoginInProgress) {
		t.Errorf("expected ErrLoginInProgress, got %v", err)
	}
	sess := svc.GetLogin(a.ID)
	if _, err := svc.CancelLogin(context.Background(), a.ID, sess.LoginID); err != nil {
		t.Fatalf("CancelLogin: %v", err)
	}
}

func TestLogoutClearsBinding(t *testing.T) {
	svc, a := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"user@example.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
	)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for svc.GetLogin(a.ID).State != LoginSucceeded && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// The fake stays "logged in" (account/read keeps returning the
	// account), so logout verification would fail closed — instead flip
	// the fake to logged-out for the logout step.
	svc.ExtraEnv = testExtraEnv(t)
	if err := svc.Logout(context.Background(), a.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() {
		t.Error("verified logout must clear the binding")
	}
}

func TestRateLimitsLoggedOutIsControlled(t *testing.T) {
	svc, a := testCodexService(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32600:codex account authentication required to read rate limits")
	_, err := svc.ReadRateLimits(context.Background(), a.ID)
	if err == nil {
		t.Fatal("logged-out rate limits must fail")
	}
	if !errors.Is(err, ErrCodexNotConnected) {
		t.Errorf("must classify as not-connected, got: %v", err)
	}
}

func TestClassifyRPC(t *testing.T) {
	if err := classifyRPC(&rpcError{Code: -32601, Message: "Method not found"}); !errors.Is(err, ErrCodexIncompatible) {
		t.Errorf("method-not-found must map to incompatible: %v", err)
	}
	if err := classifyRPC(&rpcError{Code: -32600, Message: "X authentication required Y"}); !errors.Is(err, ErrCodexNotConnected) {
		t.Errorf("auth-required must map to not-connected: %v", err)
	}
	if err := classifyRPC(&rpcError{Code: -32000, Message: "boom"}); !errors.Is(err, ErrCodexProcessFailed) {
		t.Errorf("unknown code must map to process-failed: %v", err)
	}
	if s := classifyRPC(&rpcError{Code: -32000, Message: "secret-material-here"}).Error(); strings.Contains(s, "secret-material-here") {
		t.Errorf("raw upstream text must not leak: %q", s)
	}
}
