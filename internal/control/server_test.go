package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
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
	return New(database, func() config.Settings { return current })
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
