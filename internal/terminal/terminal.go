// Package terminal starts an interactive command in a user-visible terminal.
package terminal

import (
	"errors"
	"os/exec"
)

var ErrUnavailable = errors.New("terminal unavailable")

type Request struct {
	Executable string
	Args       []string
	WorkingDir string
}

type Launcher interface {
	Launch(Request) error
}

type System struct {
	LookPath func(string) (string, error)
	Start    func(*exec.Cmd) error
}

func New() *System {
	return &System{
		LookPath: exec.LookPath,
		Start:    func(cmd *exec.Cmd) error { return cmd.Start() },
	}
}

func (s *System) Launch(request Request) error {
	if request.Executable == "" || request.WorkingDir == "" {
		return ErrUnavailable
	}
	return s.launch(request)
}

func (s *System) start(path string, args []string, dir string) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	if err := s.Start(cmd); err != nil {
		return ErrUnavailable
	}
	return nil
}
