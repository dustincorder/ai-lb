//go:build darwin

package terminal

import (
	"fmt"
	"strings"
)

func (s *System) launch(request Request) error {
	path, err := s.LookPath("osascript")
	if err != nil {
		return ErrUnavailable
	}
	command := append([]string{request.Executable}, request.Args...)
	parts := make([]string, len(command))
	for i, value := range command {
		parts[i] = shellQuote(value)
	}
	shellCommand := fmt.Sprintf("cd -- %s && exec %s", shellQuote(request.WorkingDir), strings.Join(parts, " "))
	script := fmt.Sprintf("tell application \"Terminal\" to do script %s", appleScriptQuote(shellCommand))
	return s.start(path, []string{"-e", script}, request.WorkingDir)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func appleScriptQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
