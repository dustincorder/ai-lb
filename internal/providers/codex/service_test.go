package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestLoginSurvivesRequestCancel(t *testing.T) {
	svc, a := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"u@e.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
	)
	// Cancellable "HTTP-like" context for the start call.
	httpCtx, cancel := context.WithCancel(context.Background())
	sess, err := svc.StartLogin(httpCtx, a.ID, LoginBrowser)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if sess.State != LoginWaiting {
		t.Fatalf("expected waiting, got %q", sess.State)
	}
	// The HTTP request dies; the session process must stay alive.
	cancel()
	svc.mu.Lock()
	live, ok := svc.logins[a.ID]
	svc.mu.Unlock()
	if !ok {
		t.Fatal("session vanished after request cancel")
	}
	select {
	case <-live.client.waitDone:
		t.Fatal("session process died with the HTTP request context")
	default:
	}
	// Completion must still succeed afterwards.
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
			t.Fatal("login did not complete after request cancel")
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Connected() {
		t.Error("post-cancel completion must still bind")
	}
}

func TestQuotaPrimarySecondaryPreserved(t *testing.T) {
	usedP, usedS := 20, 60
	res := rateLimitsResult{
		RateLimits: &rateLimitSnapshot{
			LimitID:   strRef("codex"),
			LimitName: strRef("Codex"),
			Primary:   &rateLimitWindow{UsedPercent: &usedP},
			Secondary: &rateLimitWindow{UsedPercent: &usedS},
		},
		// Same buckets mirrored under byLimitId must not duplicate.
		RateLimitsByLimitID: map[string]rateLimitSnapshot{
			"codex": {
				LimitID:   strRef("codex"),
				LimitName: strRef("Codex"),
				Primary:   &rateLimitWindow{UsedPercent: &usedP},
				Secondary: &rateLimitWindow{UsedPercent: &usedS},
			},
			"extra": {
				Primary:   &rateLimitWindow{UsedPercent: intPtr(10)},
				Secondary: &rateLimitWindow{UsedPercent: intPtr(90)},
			},
		},
	}
	snap := mapQuota(res, time.Now().UTC())
	if len(snap.Windows) != 4 {
		t.Fatalf("want primary+secondary+extra×2, got %+v", snap.Windows)
	}
	got := map[string]int{}
	for _, w := range snap.Windows {
		got[w.LimitID]++
	}
	if got["codex"] != 2 || got["extra"] != 2 {
		t.Errorf("bucket roles lost or duplicated: %+v", snap.Windows)
	}
}

func TestQuotaCacheAvoidsLiveCall(t *testing.T) {
	svc, a := testCodexService(t)
	ctx := context.Background()
	first, err := svc.ReadRateLimits(ctx, a.ID)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Break live reads; a TTL-fresh second read must serve cache.
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32000:boom")
	second, err := svc.ReadRateLimits(ctx, a.ID)
	if err != nil {
		t.Fatalf("cached read must not touch live backend: %v", err)
	}
	if second.Stale {
		t.Error("fresh cache must not be marked stale")
	}
	if len(second.Windows) != len(first.Windows) {
		t.Errorf("cached snapshot differs: %+v vs %+v", second, first)
	}
}

func TestRefreshBypassesCache(t *testing.T) {
	svc, a := testCodexService(t)
	ctx := context.Background()
	if _, err := svc.ReadRateLimits(ctx, a.ID); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// New live data must win over cache on explicit refresh.
	svc.ExtraEnv = testExtraEnv(t, `AI_LB_FAKE_RATELIMITS={"rateLimits":{"limitId":"codex","primary":{"usedPercent":77}}}`)
	refreshed, err := svc.RefreshRateLimits(ctx, a.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.Stale || len(refreshed.Windows) == 0 || orZero(refreshed.Windows[0].UsedPercent) != 77 {
		t.Errorf("refresh must return fresh live data: %+v", refreshed)
	}
	// Failing live with cache present returns stale, not an error.
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32000:boom")
	stale, err := svc.RefreshRateLimits(ctx, a.ID)
	if err != nil {
		t.Fatalf("refresh with cache must degrade to stale: %v", err)
	}
	if !stale.Stale {
		t.Error("degraded refresh must be marked stale")
	}
}

func TestCompleteConnectionAtomic(t *testing.T) {
	svc, a := testCodexService(t)
	ctx := context.Background()
	// Oversized identity fails validation: nothing may commit.
	big := ""
	for i := 0; i < 300; i++ {
		big += "x"
	}
	if _, err := svc.accounts.CompleteProviderConnection(ctx, a.ID, BindingRefFor(a.ID), big, ""); err == nil {
		t.Fatal("oversized identity must fail")
	}
	got, err := svc.accounts.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() || got.Identity != "" {
		t.Errorf("failed connection must leave profile untouched: %+v", got)
	}
	// Success commits binding and identity together.
	done, err := svc.accounts.CompleteProviderConnection(ctx, a.ID, BindingRefFor(a.ID), "u@e.com", "")
	if err != nil {
		t.Fatalf("CompleteProviderConnection: %v", err)
	}
	if !done.Connected() || done.Identity != "u@e.com" {
		t.Errorf("binding+identity must commit together: %+v", done)
	}
}

func TestTamperedConfigRepaired(t *testing.T) {
	dir := t.TempDir()
	home, err := ManagedHome(dir, "acc-1")
	if err != nil {
		t.Fatalf("ManagedHome: %v", err)
	}
	tampered := "cli_auth_credentials_store = \"file\"\ncheck_for_update_on_startup = false\n"
	if err := os.WriteFile(home+"/config.toml", []byte(tampered), 0o600); err != nil {
		t.Fatalf("tamper config: %v", err)
	}
	if _, err := ManagedHome(dir, "acc-1"); err != nil {
		t.Fatalf("ManagedHome repair: %v", err)
	}
	data, err := os.ReadFile(home + "/config.toml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != managedConfigTOML {
		t.Errorf("tampered config must be restored exactly, got:\n%s", data)
	}
}

func TestPublicLoginErrorSanitized(t *testing.T) {
	nasty := errors.New("open /tmp/ailb-codex/providers/codex/accounts/x/codex-home/auth.json: permission denied; token abc123; {\"raw\":1}")
	for _, err := range []error{
		nasty,
		errors.Join(ErrCodexProcessFailed, nasty),
	} {
		if got := publicLoginError(err); got != "login verification failed" {
			t.Errorf("unrecognized failure must be generic, got %q", got)
		}
	}
	if got := publicLoginError(ErrCredentialStoreUnavailable); got != "managed credential storage is unsafe" {
		t.Errorf("unsafe-storage message wrong: %q", got)
	}
	if got := publicLoginError(ErrCodexProtocol); got != "login verification failed" {
		t.Errorf("protocol message wrong: %q", got)
	}
}

func TestConcurrentStartSingleFlight(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "calls.log")
	svc, a := testCodexService(t, "AI_LB_FAKE_COUNT="+countFile)
	const racers = 8
	type outcome struct {
		sess LoginSession
		err  error
	}
	results := make(chan outcome, racers)
	for i := 0; i < racers; i++ {
		go func() {
			sess, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser)
			results <- outcome{sess, err}
		}()
	}
	ok, busy := 0, 0
	for i := 0; i < racers; i++ {
		r := <-results
		if r.err == nil {
			ok++
		} else if errors.Is(r.err, ErrLoginInProgress) {
			busy++
		} else {
			t.Errorf("unexpected start error: %v", r.err)
		}
	}
	if ok != 1 || busy != racers-1 {
		t.Fatalf("want exactly 1 winner and %d rejections, got %d/%d", racers-1, ok, busy)
	}
	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	starts := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "account/login/start" {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("want exactly 1 upstream login/start, got %d", starts)
	}
	sess := svc.GetLogin(a.ID)
	if _, err := svc.CancelLogin(context.Background(), a.ID, sess.LoginID); err != nil {
		t.Fatalf("cleanup cancel: %v", err)
	}
}

func TestQuotaInvalidatedOnReconnect(t *testing.T) {
	svc, a := testCodexService(t)
	ctx := context.Background()
	if _, err := svc.ReadRateLimits(ctx, a.ID); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Break live reads: while cached, the old snapshot still serves.
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32000:boom")
	if _, err := svc.ReadRateLimits(ctx, a.ID); err != nil {
		t.Fatalf("cached read must survive broken backend: %v", err)
	}
	// Reconnect (login success) invalidates the cache: the next read
	// must go live again instead of returning the previous snapshot.
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_LOGIN=ok",
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"u@e.com","planType":"plus"}`)
	if _, err := svc.StartLogin(ctx, a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for svc.GetLogin(a.ID).State != LoginSucceeded && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if svc.GetLogin(a.ID).State != LoginSucceeded {
		t.Fatal("login did not succeed")
	}
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32000:boom")
	if _, err := svc.ReadRateLimits(ctx, a.ID); err == nil {
		t.Error("post-reconnect read must go live, not serve the pre-login snapshot")
	}
}

func TestQuotaInvalidatedOnLogout(t *testing.T) {
	svc, a := testCodexService(t)
	ctx := context.Background()
	if _, err := svc.ReadRateLimits(ctx, a.ID); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// Link a binding directly (as a completed login would), then log
	// out against a logged-out fake: the cache must drop.
	if _, err := svc.accounts.CompleteProviderConnection(ctx, a.ID, BindingRefFor(a.ID), "u@e.com", ""); err != nil {
		t.Fatalf("bind: %v", err)
	}
	svc.ExtraEnv = testExtraEnv(t)
	if err := svc.Logout(ctx, a.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	svc.ExtraEnv = testExtraEnv(t, "AI_LB_FAKE_RATELIMITS=ERROR:-32000:boom")
	if _, err := svc.ReadRateLimits(ctx, a.ID); err == nil {
		t.Error("post-logout read must go live, not serve the pre-logout snapshot")
	}
}

func TestNastyNotificationErrorStaysGeneric(t *testing.T) {
	nasty := "/tmp/ailb-x/auth.json token=sk-abc {\"raw\":true}\nstderr dump"
	svc, a := testCodexService(t, "AI_LB_FAKE_LOGIN=fail:"+nasty)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var cur LoginSession
	for {
		cur = svc.GetLogin(a.ID)
		if cur.State == LoginFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("login did not resolve as failed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.Error != "login failed" {
		t.Errorf("terminal error must be generic, got %q", cur.Error)
	}
	for _, leak := range []string{"/tmp/ailb-x", "sk-abc", "stderr dump", "raw"} {
		if strings.Contains(cur.Error, leak) {
			t.Errorf("terminal error leaks %q: %q", leak, cur.Error)
		}
	}
}

func TestShutdownDuringVerification(t *testing.T) {
	svc, a := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"u@e.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
		"AI_LB_FAKE_READ_HANG=1",
	)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	// Wait until the waiter is inside verification (past the
	// notification): poll briefly, then shut down mid-verify.
	time.Sleep(800 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		svc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Close did not finish while verification hung")
	}
	cur := svc.GetLogin(a.ID)
	if cur.State != LoginFailed && cur.State != LoginCancelled {
		t.Errorf("interrupted verification must end failed/cancelled, got %q", cur.State)
	}
}

func TestFailedBindingCleansUpProviderCredential(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "calls.log")
	big := ""
	for i := 0; i < 30; i++ {
		big += "user@example.com"
	}
	accountJSON := `{"type":"chatgpt","email":"` + big + `","planType":"plus"}`
	svc, a := testCodexService(t,
		"AI_LB_FAKE_ACCOUNT="+accountJSON,
		"AI_LB_FAKE_LOGIN=ok",
		"AI_LB_FAKE_COUNT="+countFile,
	)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var cur LoginSession
	for {
		cur = svc.GetLogin(a.ID)
		if cur.State == LoginFailed {
			break
		}
		if cur.State == LoginSucceeded {
			t.Fatal("oversized identity must not succeed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not resolve, state=%q", cur.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.Error != "login verification failed" {
		t.Errorf("terminal error must be generic, got %q", cur.Error)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() {
		t.Error("failed binding must leave the profile unbound")
	}
	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if !strings.Contains(string(data), "account/logout") {
		t.Errorf("failed binding must attempt provider logout cleanup, calls:\n%s", data)
	}
}

func TestCloseKillsStartingAttempt(t *testing.T) {
	svc, a := testCodexService(t, "AI_LB_FAKE_INIT_HANG=1")
	started := make(chan error, 1)
	go func() {
		_, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser)
		started <- err
	}()
	// Let the attempt reach the hanging handshake, then shut down.
	time.Sleep(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		svc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not finish with a hanging start")
	}
	select {
	case err := <-started:
		if err == nil {
			t.Error("killed start must fail")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("starting attempt did not resolve after Close")
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.starting) != 0 || len(svc.logins) != 0 {
		t.Errorf("maps must be empty after Close: starting=%d logins=%d",
			len(svc.starting), len(svc.logins))
	}
}

func TestConcurrentStartingShutdown(t *testing.T) {
	svc, _ := testCodexService(t, "AI_LB_FAKE_INIT_HANG=1")
	ctx := context.Background()
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		a, err := svc.accounts.Create(ctx, accounts.CreateInput{
			Provider: providers.Codex, Label: "race",
		})
		if err != nil {
			t.Fatalf("create profile: %v", err)
		}
		ids = append(ids, a.ID)
	}
	for _, id := range ids {
		go func(id string) {
			_, _ = svc.StartLogin(context.Background(), id, LoginBrowser)
		}(id)
	}
	time.Sleep(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		svc.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Close did not finish with concurrent starts")
	}
	// Starting attempts clean up asynchronously after Close cancels
	// them; poll briefly rather than asserting instantly.
	deadline := time.Now().Add(10 * time.Second)
	for {
		svc.mu.Lock()
		starting, logins := len(svc.starting), len(svc.logins)
		svc.mu.Unlock()
		if starting == 0 && logins == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("maps must empty after Close: starting=%d logins=%d", starting, logins)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestVerifyFailureTriggersOrphanLogout(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "calls.log")
	svc, a := testCodexService(t,
		"AI_LB_FAKE_LOGIN=ok",
		"AI_LB_FAKE_READ_ERROR=-32000:verification backend exploded",
		"AI_LB_FAKE_COUNT="+countFile,
	)
	if _, err := svc.StartLogin(context.Background(), a.ID, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var cur LoginSession
	for {
		cur = svc.GetLogin(a.ID)
		if cur.State == LoginFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not resolve, state=%q", cur.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.Error != "login verification failed" {
		t.Errorf("terminal error must be generic, got %q", cur.Error)
	}
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() {
		t.Error("failed verification must leave the profile unbound")
	}
	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if !strings.Contains(string(data), "account/logout") {
		t.Errorf("post-success verification failure must attempt orphan logout, calls:\n%s", data)
	}
}

func loginToSuccess(t *testing.T, svc *Service, id string) {
	t.Helper()
	if _, err := svc.StartLogin(context.Background(), id, LoginBrowser); err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		cur := svc.GetLogin(id)
		if cur.State == LoginSucceeded {
			return
		}
		if cur.State == LoginFailed || cur.State == LoginExpired {
			t.Fatalf("login ended %q: %s", cur.State, cur.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not succeed, state=%q", cur.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func createCodexProfile(t *testing.T, svc *Service, label string) accounts.Account {
	t.Helper()
	a, err := svc.accounts.Create(context.Background(), accounts.CreateInput{
		Provider: providers.Codex, Label: label,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	return a
}

// TestDuplicateUpstreamRejected proves one upstream quota identity
// cannot back two local profiles: the second login fails with the
// duplicate code, stays unbound, the first profile is untouched, and
// cleanup logout runs only in the second managed home.
func TestDuplicateUpstreamRejected(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "calls.log")
	accountJSON := `{"type":"chatgpt","email":"same@example.com","planType":"plus"}`
	ratelimits := `{"accountId":"acct-X","rateLimits":{"limitId":"codex","primary":{"usedPercent":10}}}`
	svc, _ := testCodexService(t,
		"AI_LB_FAKE_ACCOUNT="+accountJSON,
		"AI_LB_FAKE_LOGIN=ok",
		"AI_LB_FAKE_RATELIMITS="+ratelimits,
		"AI_LB_FAKE_COUNT="+countFile,
	)
	ctx := context.Background()
	a := createCodexProfile(t, svc, "Personal")
	b := createCodexProfile(t, svc, "Work")

	loginToSuccess(t, svc, a.ID)

	if _, err := svc.StartLogin(ctx, b.ID, LoginBrowser); err != nil {
		t.Fatalf("second StartLogin: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	var cur LoginSession
	for {
		cur = svc.GetLogin(b.ID)
		if cur.State == LoginFailed {
			break
		}
		if cur.State == LoginSucceeded {
			t.Fatal("duplicate login must not succeed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("duplicate did not resolve, state=%q", cur.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cur.ErrorCode != "duplicate_provider_account" {
		t.Errorf("terminal code = %q, want duplicate_provider_account", cur.ErrorCode)
	}
	if !strings.Contains(cur.Error, "already connected") {
		t.Errorf("terminal message must name the conflict, got %q", cur.Error)
	}
	if !strings.Contains(cur.Error, "Personal") {
		t.Errorf("terminal message should name the existing profile, got %q", cur.Error)
	}
	if strings.Contains(cur.Error, "acct-X") {
		t.Errorf("opaque upstream id must never reach the UI: %q", cur.Error)
	}

	// Second profile stays fully unbound.
	bb, err := svc.accounts.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if bb.Connected() || bb.ProviderAccountID != "" {
		t.Errorf("duplicate profile must stay unbound: %+v", bb)
	}
	// First profile untouched: still bound, same identity, same upstream id.
	aa, err := svc.accounts.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if !aa.Connected() || aa.ProviderAccountID != "acct-X" {
		t.Errorf("first profile must stay connected: %+v", aa)
	}
	// Cleanup logout ran exactly once, in B's home only.
	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	logouts := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "account/logout") {
			continue
		}
		logouts++
		if !strings.HasSuffix(line, "|"+b.ID) {
			t.Errorf("logout must run in the duplicate home only, got %q (B=%s)", line, b.ID)
		}
	}
	if logouts != 1 {
		t.Errorf("want exactly 1 cleanup logout, got %d:\n%s", logouts, data)
	}
}

// TestDistinctUpstreamIDsAllowed proves different quota identities and
// unknown identities never collide — and that email alone is not a
// uniqueness key.
func TestDistinctUpstreamIDsAllowed(t *testing.T) {
	svc, _ := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"same@example.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
	)
	mk := func(label, pid string) accounts.Account {
		t.Helper()
		a := createCodexProfile(t, svc, label)
		svc.ExtraEnv = testExtraEnv(t,
			`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"same@example.com","planType":"plus"}`,
			"AI_LB_FAKE_LOGIN=ok",
			`AI_LB_FAKE_RATELIMITS={"accountId":"`+pid+`","rateLimits":{"limitId":"codex","primary":{"usedPercent":10}}}`,
		)
		loginToSuccess(t, svc, a.ID)
		return a
	}
	one := mk("One", "acct-1")
	two := mk("Two", "acct-2")
	for _, tc := range []struct {
		id  string
		pid string
	}{
		{one.ID, "acct-1"},
		{two.ID, "acct-2"},
	} {
		got, err := svc.accounts.Get(context.Background(), tc.id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !got.Connected() || got.ProviderAccountID != tc.pid {
			t.Errorf("profile must bind its own pid: %+v", got)
		}
		if got.Identity != "same@example.com" {
			t.Errorf("same email on distinct pids is fine: %+v", got)
		}
	}
}

// TestMissingUpstreamIDAllowsLogin proves login still succeeds when the
// provider exposes no accountId, without email-based hard dedupe.
func TestMissingUpstreamIDAllowsLogin(t *testing.T) {
	svc, _ := testCodexService(t,
		`AI_LB_FAKE_ACCOUNT={"type":"chatgpt","email":"same@example.com","planType":"plus"}`,
		"AI_LB_FAKE_LOGIN=ok",
	)
	a := createCodexProfile(t, svc, "NoID")
	loginToSuccess(t, svc, a.ID)
	got, err := svc.accounts.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Connected() || got.ProviderAccountID != "" {
		t.Errorf("unknown upstream id must bind without pid: %+v", got)
	}
}
