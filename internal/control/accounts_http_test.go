package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doJSON(t *testing.T, method, url, payload string) (int, map[string]any) {
	t.Helper()
	var body *strings.Reader
	if payload == "" {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return resp.StatusCode, out
}

func TestProvidersEndpoint(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	code, body := getJSON(t, srv.URL+"/api/providers")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	list, ok := body["providers"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected 2 providers, got %v", body)
	}
	seen := map[string]bool{}
	for _, p := range list {
		m := p.(map[string]any)
		seen[m["id"].(string)] = true
		if m["implemented"] != false {
			t.Errorf("provider %v must report implemented=false", m["id"])
		}
		if m["display_name"] == "" {
			t.Errorf("provider %v needs a display name", m["id"])
		}
	}
	if !seen["codex"] || !seen["antigravity"] {
		t.Errorf("expected codex + antigravity, got %v", seen)
	}
}

func TestAccountsEmpty(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	code, body := getJSON(t, srv.URL+"/api/accounts")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	list, ok := body["accounts"].([]any)
	if !ok || len(list) != 0 {
		t.Errorf("fresh db should list zero accounts, got %v", body)
	}
}

func TestAccountCRUDFlow(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	// Create with defaults: enabled=true, connected=false.
	code, created := doJSON(t, http.MethodPost, srv.URL+"/api/accounts",
		`{"provider":"codex","label":"Personal","identity":"me@example.com"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST status = %d (%v), want 201", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created account has no id: %v", created)
	}
	for _, key := range []string{"provider", "label", "identity", "enabled", "connected", "created_at", "updated_at"} {
		if _, ok := created[key]; !ok {
			t.Errorf("response missing %q: %v", key, created)
		}
	}
	if created["enabled"] != true || created["connected"] != false {
		t.Errorf("defaults wrong: %v", created)
	}

	// Read back.
	code, got := doJSON(t, http.MethodGet, srv.URL+"/api/accounts/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d", code)
	}
	if got["label"] != "Personal" {
		t.Errorf("GET returned %v", got)
	}

	// Patch label + disable.
	code, patched := doJSON(t, http.MethodPatch, srv.URL+"/api/accounts/"+id,
		`{"label":"Renamed","enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("PATCH status = %d (%v)", code, patched)
	}
	if patched["label"] != "Renamed" || patched["enabled"] != false {
		t.Errorf("PATCH not applied: %v", patched)
	}
	if patched["provider"] != "codex" {
		t.Errorf("provider must be immutable: %v", patched)
	}

	// Delete, then read is 404.
	if code, _ := doJSON(t, http.MethodDelete, srv.URL+"/api/accounts/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", code)
	}
	code, gone := doJSON(t, http.MethodGet, srv.URL+"/api/accounts/"+id, "")
	if code != http.StatusNotFound {
		t.Fatalf("GET deleted status = %d (%v), want 404", code, gone)
	}
	if gone["error"] != "account_not_found" {
		t.Errorf("expected account_not_found code, got %v", gone)
	}
}

func TestAccountValidation(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	cases := []struct {
		name    string
		method  string
		path    string
		payload string
		code    int
		errCode string
	}{
		{"unknown provider", "POST", "/api/accounts", `{"provider":"banana-ai","label":"x"}`, 422, "invalid_provider"},
		{"missing label", "POST", "/api/accounts", `{"provider":"codex","label":""}`, 422, "invalid_label"},
		{"missing provider", "POST", "/api/accounts", `{"label":"x"}`, 422, "invalid_provider"},
		{"malformed json", "POST", "/api/accounts", `{"provider":`, 400, "invalid_json"},
		{"unknown field", "POST", "/api/accounts", `{"provider":"codex","label":"x","credentials_ref":"lol"}`, 400, "invalid_json"},
		{"provider patch rejected", "PATCH", "/api/accounts/whatever", `{"provider":"antigravity"}`, 400, "invalid_json"},
		{"id patch rejected", "PATCH", "/api/accounts/whatever", `{"id":"other"}`, 400, "invalid_json"},
		{"unknown account", "GET", "/api/accounts/does-not-exist", "", 404, "account_not_found"},
	}
	for _, tc := range cases {
		code, body := doJSON(t, tc.method, srv.URL+tc.path, tc.payload)
		if code != tc.code {
			t.Errorf("%s: status = %d, want %d (%v)", tc.name, code, tc.code, body)
		}
		if body["error"] != tc.errCode {
			t.Errorf("%s: error = %v, want %q", tc.name, body["error"], tc.errCode)
		}
	}
}

func TestAccountResponsesNeverLeakSecrets(t *testing.T) {
	srv := httptest.NewServer(testServer(t))
	defer srv.Close()

	code, created := doJSON(t, http.MethodPost, srv.URL+"/api/accounts",
		`{"provider":"antigravity","label":"Leak check"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST status = %d", code)
	}
	id := created["id"].(string)
	code, got := doJSON(t, http.MethodGet, srv.URL+"/api/accounts/"+id, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d", code)
	}
	raw, _ := json.Marshal(map[string]any{"one": created, "two": got})
	lowered := strings.ToLower(string(raw))
	for _, leak := range []string{"credentials_ref", "credentialsref", "secret", "token", "refresh", "auth blob"} {
		if strings.Contains(lowered, leak) {
			t.Errorf("response leaks %q: %s", leak, raw)
		}
	}
	code, list := doJSON(t, http.MethodGet, srv.URL+"/api/accounts", "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	raw, _ = json.Marshal(list)
	if strings.Contains(strings.ToLower(string(raw)), "credentials_ref") {
		t.Errorf("list leaks credentials_ref: %s", raw)
	}
}
