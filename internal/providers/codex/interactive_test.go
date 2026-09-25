package codex

import (
	"os"
	"strings"
	"testing"
)

func TestInteractiveCLIEnvKeepsTerminalButNotCredentials(t *testing.T) {
	t.Setenv("PATH", "/test/bin")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("CODEX_HOME", "/home/default/.codex")
	t.Setenv("OPENAI_API_KEY", "must-not-pass")
	t.Setenv("CODEX_API_KEY", "must-not-pass")
	t.Setenv("OPENAI_BASE_URL", "https://wrong.example")
	t.Setenv("openai_api_key", "must-not-pass")
	t.Setenv("Codex_Api_Key", "must-not-pass")
	t.Setenv("codex_home", "/home/default/.codex")
	t.Setenv("OpenAI_Base_Url", "https://wrong.example")
	t.Setenv("HTTP_PROXY", "http://proxy.example")
	t.Setenv("https_proxy", "http://lower-proxy.example")
	t.Setenv("SSL_CERT_FILE", "/etc/test-ca.pem")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh.sock")
	t.Setenv("DISPLAY", ":99")
	t.Setenv("TMUX", "/tmp/tmux,123,0")
	t.Setenv("TMUX_PANE", "%0")

	parent := append([]string(nil), os.Environ()...)
	env := InteractiveEnvForLauncher("/managed/codex-home")
	if got := strings.Join(os.Environ(), "\n"); got != strings.Join(parent, "\n") {
		t.Fatalf("caller environment changed: before=%q after=%q", strings.Join(parent, "\n"), got)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"CODEX_HOME=/managed/codex-home", "PATH=/test/bin", "TERM=xterm-256color", "LANG=C.UTF-8",
		"HTTP_PROXY=http://proxy.example", "https_proxy=http://lower-proxy.example",
		"SSL_CERT_FILE=/etc/test-ca.pem", "SSH_AUTH_SOCK=/tmp/ssh.sock", "DISPLAY=:99",
		"TMUX=/tmp/tmux,123,0", "TMUX_PANE=%0",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("interactive env missing %q: %v", want, env)
		}
	}
	for _, blocked := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL"} {
		for _, kv := range env {
			key, _, _ := strings.Cut(kv, "=")
			if strings.ToUpper(key) == blocked {
				t.Fatalf("interactive env leaked blocked key %q: %v", key, env)
			}
		}
	}
	if strings.Contains(joined, "default/.codex") {
		t.Fatalf("interactive env leaked default home or provider credential: %v", env)
	}
	count := 0
	for _, kv := range env {
		if kv == "CODEX_HOME=/managed/codex-home" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("selected CODEX_HOME count = %d, env = %v", count, env)
	}
	if got := os.Getenv("CODEX_HOME"); got != "/home/default/.codex" {
		t.Fatalf("caller CODEX_HOME changed: %q", got)
	}
}
