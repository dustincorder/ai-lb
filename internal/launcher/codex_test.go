package launcher

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

func TestProcessResultPreservesUnixSignalExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix signal exit status")
	}
	for _, test := range []struct {
		name string
		sig  string
		want int
	}{
		{name: "SIGINT", sig: "INT", want: 130},
		{name: "SIGTERM", sig: "TERM", want: 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", "kill -"+test.sig+" $$")
			err := cmd.Run()
			var exitErr *ExitError
			result := processResult(err)
			if !errors.As(result, &exitErr) || exitErr.Code != test.want {
				t.Fatalf("processResult(%v) = %v, want exit code %d", err, result, test.want)
			}
		})
	}
}
