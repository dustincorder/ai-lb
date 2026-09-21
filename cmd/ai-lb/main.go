// Command ai-lb runs the local service: control server (web UI +
// management API) and gateway listener, backed by SQLite.
//
// By default both listeners bind loopback only and the service runs in
// the foreground. It works fully without any open browser window.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/app"
	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/launcher"
	"github.com/dustincorder/ai-lb/internal/providers"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitErr *launcher.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "ai-lb: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "codex" {
		return runCodexCommand(args[1:])
	}
	fs := flag.NewFlagSet("ai-lb", flag.ContinueOnError)
	var (
		dataDir     = fs.String("data-dir", "", "data directory (default: OS app data dir)")
		controlHost = fs.String("control-host", "", "control bind host, loopback only (default from settings)")
		controlPort = fs.Int("control-port", 0, "control bind port (default from settings)")
		gatewayHost = fs.String("gateway-host", "", "gateway bind host, loopback only (default from settings)")
		gatewayPort = fs.Int("gateway-port", 0, "gateway bind port (default from settings)")
		showVersion = fs.Bool("version", false, "print build information and exit")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		b := build.Current()
		fmt.Printf("ai-lb %s\ncommit: %s\nchannel: %s\n", b.Version, b.Commit, b.Channel)
		return nil
	}

	// Flag overrides apply to this run only; PUT /api/settings persists.
	// A zero flag value means "not supplied" (port 0 and empty host are
	// invalid settings anyway).
	var overrides config.Overrides
	if *controlHost != "" {
		overrides.ControlHost = controlHost
	}
	if *controlPort != 0 {
		overrides.ControlPort = controlPort
	}
	if *gatewayHost != "" {
		overrides.GatewayHost = gatewayHost
	}
	if *gatewayPort != 0 {
		overrides.GatewayPort = gatewayPort
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.Run(ctx, *dataDir, overrides)
}

func runCodexCommand(args []string) error {
	opts, err := launcher.ParseArgs(args)
	if err != nil {
		return err
	}
	database, err := db.Open(opts.DataDir)
	if err != nil {
		return fmt.Errorf("database unavailable")
	}
	defer database.Close()

	registry := providers.Default()
	accountService := accounts.NewService(registry, accounts.NewRepository(database.Conn))
	binary, err := exec.LookPath("codex")
	if err != nil {
		binary = ""
	}
	ctx, stop := launcher.SignalContext(context.Background())
	defer stop()
	return launcher.Run(ctx, opts, launcher.RunConfig{
		Binary:   binary,
		DataDir:  filepath.Dir(database.Path),
		Accounts: accountService,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	})
}
