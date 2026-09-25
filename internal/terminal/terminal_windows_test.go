//go:build windows

package terminal

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsTerminalUsesWTArgv(t *testing.T) {
	var got *exec.Cmd
	s := &System{LookPath: func(name string) (string, error) {
		if name == "wt.exe" {
			return `C:\\Windows\\wt.exe`, nil
		}
		return "", exec.ErrNotFound
	}, Start: func(cmd *exec.Cmd) error { got = cmd; return nil }}
	req := Request{Executable: `C:\\Program Files\\ai-lb.exe`, Args: []string{"codex", "--data-dir", `C:\\Data with space;echo`, "--account", `id"quoted`}, WorkingDir: `C:\\Work Dir`}
	if err := s.Launch(req); err != nil {
		t.Fatal(err)
	}
	want := []string{`C:\\Windows\\wt.exe`, "new-tab", "--startingDirectory", req.WorkingDir, req.Executable, "codex", "--data-dir", req.Args[2], "--account", req.Args[4]}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("argv = %#v, want %#v", got.Args, want)
	}
}

func TestWindowsFallbackUsesExecutableAndNewConsole(t *testing.T) {
	var got *exec.Cmd
	s := &System{LookPath: func(name string) (string, error) {
		if name == `C:\\Program Files\\ai-lb.exe` {
			return name, nil
		}
		return "", exec.ErrNotFound
	}, Start: func(cmd *exec.Cmd) error { got = cmd; return nil }}
	req := Request{Executable: `C:\\Program Files\\ai-lb.exe`, Args: []string{"codex", "--data-dir", `C:\\Data;echo`, "--account", `id"quoted`}, WorkingDir: `C:\\Work Dir`}
	if err := s.Launch(req); err != nil {
		t.Fatal(err)
	}
	if got.Path != req.Executable || !reflect.DeepEqual(got.Args[1:], req.Args) {
		t.Fatalf("fallback argv = %#v", got.Args)
	}
	if strings.Contains(strings.ToLower(got.Path), "cmd.exe") {
		t.Fatal("cmd.exe fallback used")
	}
	if got.SysProcAttr == nil || got.SysProcAttr.CreationFlags != createNewConsole {
		t.Fatalf("creation flags = %#v", got.SysProcAttr)
	}
}

func TestWindowsCreateNewConsoleConstant(t *testing.T) {
	if createNewConsole != 0x00000010 {
		t.Fatalf("constant = %#x", createNewConsole)
	}
}
