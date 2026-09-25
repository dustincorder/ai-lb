package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/providers/codex"
)

func TestConsoleReporterReadyOutput(t *testing.T) {
	tests := []struct {
		name       string
		info       ReadyInfo
		quiet      bool
		wantSub    []string
		refuseSub  []string
	}{
		{
			name: "full ready presentation",
			info: ReadyInfo{
				Build: build.Info{
					Version: "v0.1.0",
					Channel: "stable",
				},
				ControlURL: "http://127.0.0.1:8317",
				GatewayURL: "http://127.0.0.1:8318",
				DataDir:    "/var/data/ai-lb",
				Database:   "/var/data/ai-lb/ai-lb.db",
				Codex: codex.Detection{
					Installed: true,
					Version:   "0.54.0",
				},
			},
			quiet: false,
			wantSub: []string{
				"ai-lb",
				"v0.1.0",
				"stable",
				"http://127.0.0.1:8317",
				"http://127.0.0.1:8318",
				"/var/data/ai-lb",
				"Database   ready",
				"Codex      ready · 0.54.0",
				"Antigravity planned",
				"Ready.",
				"Press Ctrl+C to stop.",
			},
			refuseSub: []string{
				"password",
				"token",
				"credentials_ref",
				"provider_account_id",
				"auth.json",
			},
		},
		{
			name: "codex not installed",
			info: ReadyInfo{
				Build: build.Info{
					Version: "dev",
					Channel: "dev",
				},
				ControlURL: "http://127.0.0.1:8317",
				GatewayURL: "http://127.0.0.1:8318",
				DataDir:    "/resolved/data",
				Database:   "/resolved/data/ai-lb.db",
				Codex: codex.Detection{
					Installed: false,
				},
			},
			quiet: false,
			wantSub: []string{
				"ai-lb",
				"dev",
				"Codex      not installed",
			},
		},
		{
			name: "quiet suppresses output",
			info: ReadyInfo{
				Build: build.Info{
					Version: "v0.1.0",
					Channel: "stable",
				},
				ControlURL: "http://127.0.0.1:8317",
				GatewayURL: "http://127.0.0.1:8318",
				DataDir:    "/var/data/ai-lb",
			},
			quiet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			reporter := ConsoleReporter{Writer: &buf, Quiet: tt.quiet}
			reporter.Ready(tt.info)
			got := buf.String()

			if tt.quiet {
				if got != "" {
					t.Fatalf("quiet mode produced output: %q", got)
				}
				return
			}

			for _, sub := range tt.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q, got:\n%s", sub, got)
				}
			}
			for _, bad := range tt.refuseSub {
				if strings.Contains(got, bad) {
					t.Errorf("output exposed sensitive/unwanted string %q, got:\n%s", bad, got)
				}
			}
		})
	}
}

func TestConsoleReporterShutdownAndStopped(t *testing.T) {
	var buf bytes.Buffer
	reporter := ConsoleReporter{Writer: &buf, Quiet: false}
	reporter.ShuttingDown()
	reporter.Stopped()

	out := buf.String()
	if !strings.Contains(out, "Shutting down...") {
		t.Errorf("missing shutdown message, got %q", out)
	}
	if !strings.Contains(out, "Stopped.") {
		t.Errorf("missing stopped message, got %q", out)
	}

	buf.Reset()
	quietReporter := ConsoleReporter{Writer: &buf, Quiet: true}
	quietReporter.ShuttingDown()
	quietReporter.Stopped()
	if buf.String() != "" {
		t.Errorf("quiet mode produced output on shutdown/stop: %q", buf.String())
	}
}
