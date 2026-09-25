package terminal

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

var errStartFailed = errors.New("start failed")

func TestLaunchRejectsMissingWorkingDirectory(t *testing.T) {
	if err := New().Launch(Request{Executable: "ai-lb"}); err != ErrUnavailable {
		t.Fatalf("error = %v", err)
	}
}

func TestSuccessfulStartIsReapedWithoutBlockingLaunch(t *testing.T) {
	reaped := make(chan *exec.Cmd, 1)
	s := &System{
		Start: func(*exec.Cmd) error { return nil },
		Reap:  func(cmd *exec.Cmd) { reaped <- cmd },
	}
	if err := s.start("fake-terminal", nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Fatal("started child was not reaped")
	}
}

func TestFailedStartIsNotReaped(t *testing.T) {
	reaped := make(chan *exec.Cmd, 1)
	s := &System{
		Start: func(*exec.Cmd) error { return errStartFailed },
		Reap:  func(cmd *exec.Cmd) { reaped <- cmd },
	}
	if err := s.start("fake-terminal", nil, t.TempDir()); err != ErrUnavailable {
		t.Fatalf("error = %v", err)
	}
	select {
	case <-reaped:
		t.Fatal("failed child was reaped")
	case <-time.After(10 * time.Millisecond):
	}
}
