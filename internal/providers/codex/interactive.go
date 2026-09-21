package codex

import (
	"os"
	"strings"
)

// InteractiveEnvForLauncher keeps the terminal and locale variables an
// interactive Codex process needs while excluding provider credentials and
// the caller's CODEX_HOME. The selected managed home is always the only
// CODEX_HOME.
func InteractiveEnvForLauncher(codexHome string) []string {
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "LC_COLLATE": true,
		"LC_MESSAGES": true, "LC_MONETARY": true, "LC_NUMERIC": true,
		"LC_TIME": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"SystemRoot": true, "windir": true, "USERPROFILE": true,
		"HOMEDRIVE": true, "HOMEPATH": true, "XDG_RUNTIME_DIR": true,
		"DBUS_SESSION_BUS_ADDRESS": true, "TERM": true, "COLORTERM": true,
		"TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "SSH_TTY": true,
		"GPG_TTY": true, "COLUMNS": true, "LINES": true,
	}
	out := []string{"CODEX_HOME=" + codexHome}
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && keep[key] {
			out = append(out, kv)
		}
	}
	return out
}
