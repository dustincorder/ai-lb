package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/providers"
	"github.com/dustincorder/ai-lb/internal/providers/codex"
	"github.com/dustincorder/ai-lb/internal/terminal"
)

type launchCapture struct {
	request terminal.Request
	err     error
}

func (l *launchCapture) Launch(request terminal.Request) error {
	l.request = request
	return l.err
}

func TestCodexLaunchValidatesAndBuildsInvocation(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	registry := providers.Default()
	acc := accounts.NewService(registry, accounts.NewRepository(database.Conn))
	account, err := acc.Create(context.Background(), accounts.CreateInput{Provider: providers.Codex, Label: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.NewRepository(database.Conn).SetCredentialsRef(context.Background(), account.ID, "opaque", time.Now()); err != nil {
		t.Fatal(err)
	}
	capture := &launchCapture{}
	stub := &stubCodex{detection: codex.Detection{Installed: true}}
	s := New(database, func() config.Settings { return config.Defaults() }, build.Info{Version: "test", Channel: "dev"}, acc, registry, stub, capture)
	srv := httptest.NewServer(s)
	defer srv.Close()
	work := t.TempDir()
	request, err := http.NewRequest(http.MethodPost, srv.URL+"/api/accounts/"+account.ID+"/codex/launch", stringsReader(`{"working_dir":"`+work+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if capture.request.WorkingDir != work {
		t.Fatalf("working dir = %q", capture.request.WorkingDir)
	}
	wantArgs := []string{"codex", "--data-dir", filepath.Dir(database.Path), "--account", account.ID}
	if !equalStrings(capture.request.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", capture.request.Args, wantArgs)
	}
	if capture.request.Executable == "" {
		t.Fatal("executable missing")
	}
	if got := postLaunch(t, srv.URL, account.ID, `{}`); got != "" {
		t.Fatalf("default launch error = %q", got)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if capture.request.WorkingDir != homeDir {
		t.Fatalf("default working dir = %q, want %q", capture.request.WorkingDir, homeDir)
	}
	home, err := codex.ManagedHome(filepath.Dir(database.Path), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := postLaunch(t, srv.URL, account.ID, `{}`); got != "unsafe_managed_home" {
		t.Fatalf("unsafe error = %q", got)
	}
	if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	capture.err = terminal.ErrUnavailable
	if got := postLaunch(t, srv.URL, account.ID, `{}`); got != "terminal_unavailable" {
		t.Fatalf("terminal error = %q", got)
	}
}

func TestCodexLaunchRejectsMissingBinary(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	registry := providers.Default()
	acc := accounts.NewService(registry, accounts.NewRepository(database.Conn))
	account, err := acc.Create(context.Background(), accounts.CreateInput{Provider: providers.Codex, Label: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.NewRepository(database.Conn).SetCredentialsRef(context.Background(), account.ID, "opaque", time.Now()); err != nil {
		t.Fatal(err)
	}
	stub := &stubCodex{detection: codex.Detection{Installed: false}}
	s := New(database, func() config.Settings { return config.Defaults() }, build.Info{Version: "test", Channel: "dev"}, acc, registry, stub, &launchCapture{})
	srv := httptest.NewServer(s)
	defer srv.Close()
	if got := postLaunch(t, srv.URL, account.ID, `{}`); got != "codex_not_installed" {
		t.Fatalf("error = %q", got)
	}
}

func TestCodexLaunchRejectsInvalidInputs(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	registry := providers.Default()
	acc := accounts.NewService(registry, accounts.NewRepository(database.Conn))
	connected, err := acc.Create(context.Background(), accounts.CreateInput{Provider: providers.Codex, Label: "Connected"})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.NewRepository(database.Conn).SetCredentialsRef(context.Background(), connected.ID, "opaque", time.Now()); err != nil {
		t.Fatal(err)
	}
	disconnected, err := acc.Create(context.Background(), accounts.CreateInput{Provider: providers.Codex, Label: "Disconnected"})
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := acc.Create(context.Background(), accounts.CreateInput{Provider: providers.Antigravity, Label: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	capture := &launchCapture{}
	stub := &stubCodex{detection: codex.Detection{Installed: true}}
	s := New(database, func() config.Settings { return config.Defaults() }, build.Info{Version: "test", Channel: "dev"}, acc, registry, stub, capture)
	srv := httptest.NewServer(s)
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingDir := filepath.Join(t.TempDir(), "missing")
	for _, test := range []struct{ name, id, body, want string }{
		{"unknown", "missing", `{}`, "account_not_found"},
		{"disconnected", disconnected.ID, `{}`, "codex_not_connected"},
		{"wrong provider", wrong.ID, `{}`, "invalid_provider"},
		{"file", connected.ID, `{"working_dir":"` + file + `"}`, "invalid_working_directory"},
		{"missing dir", connected.ID, `{"working_dir":"` + missingDir + `"}`, "invalid_working_directory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/accounts/"+test.id+"/codex/launch", stringsReader(test.body))
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body map[string]string
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["error"] != test.want {
				t.Fatalf("error = %q, want %q", body["error"], test.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func postLaunch(t *testing.T, baseURL, accountID, body string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/accounts/"+accountID+"/codex/launch", stringsReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]string
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result["error"]
}

func stringsReader(value string) *strings.Reader { return strings.NewReader(value) }
