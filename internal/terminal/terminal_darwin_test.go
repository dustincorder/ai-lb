//go:build darwin

package terminal

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDarwinAppleScriptKeepsArgumentsAsData(t *testing.T) {
	var got *exec.Cmd
	s := &System{
		LookPath: func(name string) (string, error) { return "/usr/bin/osascript", nil },
		Start:    func(cmd *exec.Cmd) error { got = cmd; return nil },
	}
	request := Request{Executable: "/tmp/ai lb/'tool", Args: []string{"codex", "--data-dir", `/tmp/a "quoted"`, "--account", "id; echo unsafe"}, WorkingDir: `/tmp/work dir/'quoted`}
	if err := s.Launch(request); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/usr/bin/osascript" || len(got.Args) != 3 || got.Args[1] != "-e" {
		t.Fatalf("osascript argv = %#v", got.Args)
	}
	script := got.Args[2]
	if !strings.Contains(script, `Terminal`) || !strings.Contains(script, `id; echo unsafe`) || !strings.Contains(script, `quoted`) {
		t.Fatalf("script lost data: %q", script)
	}
	if !strings.Contains(script, `\\`) || !strings.Contains(script, `\"`) {
		t.Fatalf("script escaping missing: %q", script)
	}
}
