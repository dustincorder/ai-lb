// Command ai-lb runs the local service: control server (web UI +
// management API) and gateway listener, backed by SQLite.
//
// By default both listeners bind loopback only and the service runs in
// the foreground. It works fully without any open browser window.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dustincorder/ai-lb/internal/app"
	"github.com/dustincorder/ai-lb/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ai-lb: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	defaults := config.Defaults()
	var (
		dataDir     = flag.String("data-dir", "", "data directory (default: OS app data dir)")
		controlHost = flag.String("control-host", "", "control bind host, loopback only (default from settings)")
		controlPort = flag.Int("control-port", 0, "control bind port (default from settings)")
		gatewayHost = flag.String("gateway-host", "", "gateway bind host, loopback only (default from settings)")
		gatewayPort = flag.Int("gateway-port", 0, "gateway bind port (default from settings)")
	)
	flag.Parse()

	// Flag overrides apply to this run; PUT /api/settings persists.
	useOverrides := *controlHost != "" || *controlPort != 0 || *gatewayHost != "" || *gatewayPort != 0
	overrides := defaults
	if *controlHost != "" {
		overrides.ControlHost = *controlHost
	}
	if *controlPort != 0 {
		overrides.ControlPort = *controlPort
	}
	if *gatewayHost != "" {
		overrides.GatewayHost = *gatewayHost
	}
	if *gatewayPort != 0 {
		overrides.GatewayPort = *gatewayPort
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, *dataDir, overrides, useOverrides)
}
