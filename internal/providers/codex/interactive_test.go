package codex

import (
	"strings"
	"testing"
)

func TestInteractiveCLIEnvKeepsTerminalButNotCredentials(t *testing.T) {
	t.Setenv("PATH", "/test/bin")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("CODEX_HOME", "/home/default/.codex")
	t.Setenv("OPENAI_API_KEY", "must-not-pass")

	env := InteractiveEnvForLauncher("/managed/codex-home")
	joined := strings.Join(env, "\n")
	for _, want := range []string{"CODEX_HOME=/managed/codex-home", "PATH=/test/bin", "TERM=xterm-256color", "LANG=C.UTF-8"} {
		if !strings.Contains(joined, want) {
			t.Errorf("interactive env missing %q: %v", want, env)
		}
	}
	if strings.Contains(joined, "default/.codex") || strings.Contains(joined, "OPENAI_API_KEY") {
		t.Fatalf("interactive env leaked default home or provider credential: %v", env)
	}
}
