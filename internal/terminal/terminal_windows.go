//go:build windows

package terminal

func (s *System) launch(request Request) error {
	if path, err := s.LookPath("wt.exe"); err == nil {
		args := append([]string{"new-tab", "--startingDirectory", request.WorkingDir, request.Executable}, request.Args...)
		return s.start(path, args, request.WorkingDir)
	}
	path, err := s.LookPath("cmd.exe")
	if err != nil {
		return ErrUnavailable
	}
	args := append([]string{"/c", "start", "", request.Executable}, request.Args...)
	return s.start(path, args, request.WorkingDir)
}
