//go:build !windows

package launcher

import (
	"os"
	"syscall"
)

func stopChild(process *os.Process, sig os.Signal) error {
	return process.Signal(sig)
}

func processStateExitCode(state *os.ProcessState) int {
	status, ok := state.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	code := state.ExitCode()
	if code < 0 {
		return 1
	}
	return code
}
