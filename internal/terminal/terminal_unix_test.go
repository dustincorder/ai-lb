//go:build linux

package terminal

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestLinuxCandidateOrderFallbackWorkingDirAndArgv(t *testing.T) {
	var names []string
	var got *exec.Cmd
	s := &System{
		LookPath: func(name string) (string, error) {
			names = append(names, name)
			if name == "konsole" {
				return "/usr/bin/konsole", nil
			}
			return "", exec.ErrNotFound
		},
		Start: func(cmd *exec.Cmd) error { got = cmd; return nil },
	}
	request := Request{Executable: "/tmp/ai lb;echo", Args: []string{"codex", "--data-dir", "/tmp/data with space", "--account", "id;echo"}, WorkingDir: t.TempDir()}
	if err := s.Launch(request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"x-terminal-emulator", "gnome-terminal", "konsole"}) {
		t.Fatalf("candidate order = %#v", names)
	}
	want := []string{"/usr/bin/konsole", "--workdir", request.WorkingDir, "-e", request.Executable, "codex", "--data-dir", "/tmp/data with space", "--account", "id;echo"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("argv = %#v, want %#v", got.Args, want)
	}
	if got.Dir != request.WorkingDir {
		t.Fatalf("dir = %q", got.Dir)
	}
}
