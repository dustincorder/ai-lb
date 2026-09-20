package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/update"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	settings, err := database.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	current := settings
	return New(database, func() config.Settings { return current }, build.Info{Version: "test", Channel: "dev"})
}

// divergentServer returns a control server whose ACTIVE settings differ
// from the CONFIGURED (persisted) ones, plus both values.
func divergentServer(t *testing.T) (*Server, config.Settings, config.Settings) {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	configured := config.Settings{
		ControlHost:   "127.0.0.1",
		ControlPort:   8441,
		GatewayHost:   "127.0.0.1",
		GatewayPort:   8442,
		UpdateChannel: config.UpdateChannelStable,
	}
	if err := database.SaveSettings(configured); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	active := config.Defaults()
	return New(database, func() config.Settings { return active }, build.Info{Version: "test", Channel: "dev"}), configured, active
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp.StatusCode, body
}

func TestHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" || body["service"] != "control" {
		t.Errorf("unexpected body: %v", body)
	}
}

func TestAppEndpointShape(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/app")
	if err != nil {
		t.Fatalf("GET /api/app: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"version", "control", "gateway", "database", "platform"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in /api/app", key)
		}
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "auth") || strings.Contains(string(raw), "token") {
		t.Errorf("/api/app must not expose auth data: %s", raw)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := testServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/settings")
	if err != nil {
		t.Fatalf("GET /api/settings: %v", err)
	}
	var current config.Settings
	if err := json.NewDecoder(resp.Body).Decode(&current); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if current != config.Defaults() {
		t.Errorf("fresh settings should be defaults, got %+v", current)
	}

	next := config.Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 8411,
		GatewayHost: "127.0.0.1",
		GatewayPort: 8412,
		UpdateChannel: config.UpdateChannelStable,
	}
	raw, _ := json.Marshal(next)
	put, err := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("PUT request: %v", err)
	}
	put.Header.Set("Content-Type", "application/json")
	presp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("PUT /api/settings: %v", err)
	}
	defer presp.Body.Close()
	if presp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", presp.StatusCode)
	}
	var out SettingsResponse
	if err := json.NewDecoder(presp.Body).Decode(&out); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if out.Settings != next {
		t.Errorf("PUT response settings = %+v, want %+v", out.Settings, next)
	}
	if !out.RestartRequired {
		t.Error("changed ports must report restart_required=true")
	}

	stored, err := s.DB.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if stored != next {
		t.Errorf("stored settings = %+v, want %+v", stored, next)
	}
}

func TestSettingsRejectsInvalid(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	for _, payload := range []string{
		`{"control_host":"127.0.0.1","control_port":99999,"gateway_host":"127.0.0.1","gateway_port":8318}`,
		`{"control_host":"0.0.0.0","control_port":8317,"gateway_host":"127.0.0.1","gateway_port":8318}`,
		`{"control_host":"127.0.0.1","control_port":9000,"gateway_host":"127.0.0.1","gateway_port":9000}`,
		`not-json`,
	} {
		put, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(payload))
		presp, err := http.DefaultClient.Do(put)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		presp.Body.Close()
		if presp.StatusCode == http.StatusOK {
			t.Errorf("payload %q should be rejected", payload)
		}
	}
}

// TestConfiguredVsActive pins the core settings contract: /api/settings
// reports CONFIGURED (persisted) values while /api/app and /api/gateway
// describe the ACTIVE listeners.
func TestConfiguredVsActive(t *testing.T) {
	s, configured, active := divergentServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	code, body := getJSON(t, srv.URL+"/api/settings")
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d", code)
	}
	if int(body["control_port"].(float64)) != configured.ControlPort {
		t.Errorf("GET /api/settings should show configured %d, got %v",
			configured.ControlPort, body["control_port"])
	}

	code, app := getJSON(t, srv.URL+"/api/app")
	if code != http.StatusOK {
		t.Fatalf("GET /api/app status = %d", code)
	}
	control := app["control"].(map[string]any)
	if control["port"] != itoa(active.ControlPort) {
		t.Errorf("GET /api/app should show active port %d, got %v",
			active.ControlPort, control["port"])
	}
}

// TestPutShowsConfiguredImmediately verifies PUT persists, GET reflects
// the new configured values at once, and the active addresses stay put.
func TestPutShowsConfiguredImmediately(t *testing.T) {
	s, _, active := divergentServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()

	next := config.Settings{
		ControlHost:   "127.0.0.1",
		ControlPort:   8451,
		GatewayHost:   "127.0.0.1",
		GatewayPort:   8452,
		UpdateChannel: config.UpdateChannelStable,
	}
	raw, _ := json.Marshal(next)
	put, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", strings.NewReader(string(raw)))
	presp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	var out SettingsResponse
	if err := json.NewDecoder(presp.Body).Decode(&out); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	presp.Body.Close()
	if out.Settings != next {
		t.Errorf("PUT response = %+v, want %+v", out.Settings, next)
	}
	if !out.RestartRequired {
		t.Error("configured != active must report restart_required=true")
	}

	code, body := getJSON(t, srv.URL+"/api/settings")
	if code != http.StatusOK || int(body["gateway_port"].(float64)) != next.GatewayPort {
		t.Errorf("GET after PUT should show configured %+v, got %v", next, body)
	}

	code, app := getJSON(t, srv.URL+"/api/app")
	if code != http.StatusOK {
		t.Fatalf("GET /api/app status = %d", code)
	}
	control := app["control"].(map[string]any)
	if control["port"] != itoa(active.ControlPort) {
		t.Errorf("active addresses must not move before restart, got %v", control["port"])
	}
}

func doWithHost(t *testing.T, method, url, host, origin, payload string) int {
	t.Helper()
	var reader *strings.Reader
	if payload == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestHostPolicy(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()
	loopback := strings.TrimPrefix(srv.URL, "http://")

	if code := doWithHost(t, http.MethodGet, srv.URL+"/api/health", loopback, "", ""); code != http.StatusOK {
		t.Errorf("loopback Host should be accepted, got %d", code)
	}
	if code := doWithHost(t, http.MethodGet, srv.URL+"/api/health", "localhost:1234", "", ""); code != http.StatusOK {
		t.Errorf("localhost Host should be accepted, got %d", code)
	}
	for _, foreign := range []string{"evil.com", "example.com:80", "192.168.1.10:8317"} {
		if code := doWithHost(t, http.MethodGet, srv.URL+"/api/health", foreign, "", ""); code != http.StatusForbidden {
			t.Errorf("foreign Host %q should be rejected, got %d", foreign, code)
		}
	}
	// Empty Host can only arrive over raw HTTP/1.0; the predicate itself
	// must still reject it.
	if isLoopbackHost("") {
		t.Error("empty Host must not count as loopback")
	}
}

func TestOriginPolicyOnPUT(t *testing.T) {
	s, configured, _ := divergentServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()
	loopback := strings.TrimPrefix(srv.URL, "http://")
	raw, _ := json.Marshal(configured)

	// No Origin (CLI/curl): allowed.
	if code := doWithHost(t, http.MethodPut, srv.URL+"/api/settings", loopback, "", string(raw)); code != http.StatusOK {
		t.Errorf("PUT without Origin should be accepted, got %d", code)
	}
	// Same-origin browser request: allowed.
	if code := doWithHost(t, http.MethodPut, srv.URL+"/api/settings", loopback, "http://"+loopback, string(raw)); code != http.StatusOK {
		t.Errorf("same-origin PUT should be accepted, got %d", code)
	}
	// Foreign origin: rejected before touching settings.
	if code := doWithHost(t, http.MethodPut, srv.URL+"/api/settings", loopback, "http://evil.com", string(raw)); code != http.StatusForbidden {
		t.Errorf("foreign-Origin PUT should be rejected, got %d", code)
	}
}

func TestServeUIFallbackWithoutBuild(t *testing.T) {
	// Empty dist (clean checkout: only .gitkeep, no index.html) → 503
	// with build guidance, not a broken page.
	empty := fstest.MapFS{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	serveUI(rec, req, empty)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("missing frontend should be 503, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "npm --prefix web run build") {
		t.Errorf("503 should explain the frontend build, got %q", body)
	}

	// Built dist → index.html served, unknown paths fall back to it.
	built := fstest.MapFS{
		"index.html": {Data: []byte("<html>ai-lb</html>")},
	}
	for _, path := range []string{"/", "/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		serveUI(rec, req, built)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "ai-lb") {
			t.Errorf("GET %s should serve index.html, got %q", path, body)
		}
	}
}

// TestUpdateEndpoints wires the checker to a mock releases server and
// exercises the management contract: idle state, manual check offering
// a newer stable, and a verified download. No real network is used.
//
// The mock asset name is derived from the test runner's own platform
// through update.ArtifactName: hardcoding one OS here breaks matrix CI
// on every other OS (the checker selects by runtime.GOOS/GOARCH).
func TestUpdateEndpoints(t *testing.T) {
	payload := []byte("fake-binary-bytes")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	tag := "v9.9.0"
	assetName := update.ArtifactName(tag, runtime.GOOS, runtime.GOARCH)

	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		rel := map[string]any{
			"tag_name": tag, "name": tag, "html_url": base + "/rel",
			"draft": false, "prerelease": false,
			"published_at": "2026-09-20T12:00:00Z",
			"assets": []map[string]any{
				{"name": assetName,
					"browser_download_url": base + "/dl/bin", "size": len(payload)},
				{"name": "checksums.txt",
					"browser_download_url": base + "/dl/sums", "size": 100},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{rel})
	})
	mux.HandleFunc("/dl/bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/dl/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(hexSum + "  " + assetName + "\n"))
	})
	gh := httptest.NewServer(mux)
	defer gh.Close()
	base = gh.URL

	s := testServer(t)
	uc := update.NewClient("o", "r", "test")
	uc.APIBase = gh.URL
	s.Updates = update.NewChecker(uc)
	// The test binary reports a dev build; the endpoints below exercise
	// the check path explicitly, which is allowed on any build.
	s.Build = build.Info{Version: "v9.8.0", Commit: "c", Channel: "dev"}
	srv := httptest.NewServer(s)
	defer srv.Close()

	code, body := getJSON(t, srv.URL+"/api/update")
	if code != http.StatusOK {
		t.Fatalf("GET /api/update status = %d", code)
	}
	if body["status"] != "idle" {
		t.Errorf("initial update status = %v, want idle", body["status"])
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/update/check", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/update/check: %v", err)
	}
	var checked map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&checked); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	resp.Body.Close()
	if checked["status"] != "available" || checked["available_version"] != tag {
		t.Errorf("check should offer %s, got %v", tag, checked)
	}

	dreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/update/download", nil)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		t.Fatalf("POST /api/update/download: %v", err)
	}
	var dl map[string]any
	if err := json.NewDecoder(dresp.Body).Decode(&dl); err != nil {
		t.Fatalf("decode download: %v", err)
	}
	dresp.Body.Close()
	if dl["status"] != "downloaded" || dl["sha256"] != hexSum {
		t.Errorf("download should verify and report path, got %v", dl)
	}
	path, _ := dl["path"].(string)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded artifact: %v", err)
	}
	if string(data) != string(payload) {
		t.Error("downloaded bytes differ")
	}
	os.RemoveAll(filepath.Dir(path))
}
