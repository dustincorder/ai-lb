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
	t.Setenv("HTTP_PROXY", "http://proxy.example")
	t.Setenv("https_proxy", "http://lower-proxy.example")
	t.Setenv("SSL_CERT_FILE", "/etc/test-ca.pem")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/ssh.sock")
	t.Setenv("DISPLAY", ":99")
	t.Setenv("TMUX", "/tmp/tmux,123,0")
	t.Setenv("TMUX_PANE", "%0")

	env := InteractiveEnvForLauncher("/managed/codex-home")
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
	if strings.Contains(joined, "default/.codex") || strings.Contains(joined, "OPENAI_API_KEY") ||
		strings.Contains(joined, "CODEX_API_KEY") || strings.Contains(joined, "OPENAI_BASE_URL") {
		t.Fatalf("interactive env leaked default home or provider credential: %v", env)
	}
	if got := os.Getenv("CODEX_HOME"); got != "/home/default/.codex" {
		t.Fatalf("caller CODEX_HOME changed: %q", got)
	}
}
