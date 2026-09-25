//go:build windows

package launcher

import "os"

func stopChild(process *os.Process, sig os.Signal) error {
	return process.Signal(sig)
}

func processStateExitCode(state *os.ProcessState) int {
	code := state.ExitCode()
	if code < 0 {
		return 1
	}
	return code
}
