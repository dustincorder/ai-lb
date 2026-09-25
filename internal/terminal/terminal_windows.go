//go:build windows

package terminal

import (
	"os/exec"
	"syscall"
)

const createNewConsole = 0x00000010

func (s *System) launch(request Request) error {
	if path, err := s.LookPath("wt.exe"); err == nil {
		args := append([]string{"new-tab", "--startingDirectory", request.WorkingDir, request.Executable}, request.Args...)
		return s.start(path, args, request.WorkingDir)
	}
	path, err := s.LookPath(request.Executable)
	if err != nil {
		return ErrUnavailable
	}
	cmd := exec.Command(path, request.Args...)
	cmd.Dir = request.WorkingDir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if err := s.Start(cmd); err != nil {
		return ErrUnavailable
	}
	if s.Reap != nil {
		go s.Reap(cmd)
	}
	return nil
}
