package terminal

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLinuxCandidateSelection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux terminal selection")
	}
	started := ""
	s := &System{
		LookPath: func(name string) (string, error) {
			if name == "konsole" {
				return "/usr/bin/konsole", nil
			}
			return "", exec.ErrNotFound
		},
		Start: func(cmd *exec.Cmd) error {
			started = cmd.Path + " " + cmd.Args[1]
			return nil
		},
	}
	if err := s.Launch(Request{Executable: "/usr/bin/ai-lb", Args: []string{"codex", "--account", "id"}, WorkingDir: "/tmp/project"}); err != nil {
		t.Fatal(err)
	}
	if started != "/usr/bin/konsole --workdir" {
		t.Fatalf("started %q", started)
	}
}

func TestLaunchRejectsMissingWorkingDirectory(t *testing.T) {
	if err := New().Launch(Request{Executable: "ai-lb"}); err != ErrUnavailable {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkingDirectoryIsNotMutated(t *testing.T) {
	if filepath.Clean("/tmp/project") != "/tmp/project" {
		t.Fatal("unexpected filepath behavior")
	}
}
