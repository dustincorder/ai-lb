package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/providers/codex"
)

type ReadyInfo struct {
	Build      build.Info
	ControlURL string
	GatewayURL string
	DataDir    string
	Database   string
	Codex      codex.Detection
}

type Reporter interface {
	Ready(ReadyInfo)
	ShuttingDown()
	Stopped()
}

type ConsoleReporter struct {
	Writer io.Writer
	Quiet  bool
	Color  bool
}

func (r ConsoleReporter) Ready(info ReadyInfo) {
	if r.Quiet {
		return
	}
	versionLine := info.Build.Version
	if info.Build.Channel != "" && info.Build.Channel != info.Build.Version {
		versionLine = fmt.Sprintf("%s · %s", info.Build.Version, info.Build.Channel)
	}
	fmt.Fprintf(r.Writer, "ai-lb  %s\n\nService\n  Web UI     %s\n  Gateway    %s\n  Data       %s\n  Database   ready\n\nProviders\n  Codex      %s\n  Antigravity planned\n\nReady.\nPress Ctrl+C to stop.\n", versionLine, info.ControlURL, info.GatewayURL, info.DataDir, codexLabel(info.Codex))
}

func (r ConsoleReporter) ShuttingDown() {
	if !r.Quiet {
		fmt.Fprintln(r.Writer, "Shutting down...")
	}
}

func (r ConsoleReporter) Stopped() {
	if !r.Quiet {
		fmt.Fprintln(r.Writer, "Stopped.")
	}
}

func codexLabel(d codex.Detection) string {
	if !d.Installed {
		return "not installed"
	}
	if d.Version == "" {
		return "ready"
	}
	return "ready · " + strings.TrimSpace(d.Version)
}

func NewConsoleReporter(w io.Writer, quiet bool) ConsoleReporter {
	color := os.Getenv("NO_COLOR") == ""
	return ConsoleReporter{Writer: w, Quiet: quiet, Color: color}
}
