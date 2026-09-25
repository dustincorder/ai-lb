//go:build linux

package terminal

import "path/filepath"

func (s *System) launch(request Request) error {
	candidates := []struct {
		name string
		args func(string, []string) []string
	}{
		{"x-terminal-emulator", func(dir string, command []string) []string { return append([]string{"-e"}, command...) }},
		{"gnome-terminal", func(dir string, command []string) []string {
			return append([]string{"--working-directory", dir, "--"}, command...)
		}},
		{"konsole", func(dir string, command []string) []string {
			return append([]string{"--workdir", dir, "-e"}, command...)
		}},
		{"xfce4-terminal", func(dir string, command []string) []string {
			return append([]string{"--working-directory=" + dir, "--"}, command...)
		}},
		{"xterm", func(dir string, command []string) []string { return append([]string{"-e"}, command...) }},
	}
	command := append([]string{request.Executable}, request.Args...)
	for _, candidate := range candidates {
		path, err := s.LookPath(candidate.name)
		if err != nil {
			continue
		}
		if err := s.start(path, candidate.args(filepath.Clean(request.WorkingDir), command), request.WorkingDir); err == nil {
			return nil
		}
	}
	return ErrUnavailable
}
