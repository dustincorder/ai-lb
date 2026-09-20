// Package app wires the service: settings, database, control server,
// gateway server, and graceful shutdown. The backend runs fully without
// any open browser window; the UI is an optional client of the control
// server, never a requirement for the service to live.
package app

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/control"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/gateway"
	"github.com/dustincorder/ai-lb/internal/providers"
)

// shutdownTimeout bounds graceful drain of both HTTP servers.
const shutdownTimeout = 10 * time.Second

// App is the running service.
type App struct {
	DB            *db.DB
	settings      atomic.Value // config.Settings
	control       *http.Server
	controlRoutes *control.Server
	gateway       *http.Server
}

// Run opens the database, loads settings, binds both listeners, and
// serves until ctx is cancelled. Listeners bind before serving so a busy
// port fails fast with a clear error instead of a half-started service.
//
// The active settings for this run are the persisted settings with CLI
// overrides applied in memory. Overrides never rewrite SQLite.
func Run(ctx context.Context, dataDir string, overrides config.Overrides) error {
	database, err := db.Open(dataDir)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	stored, err := database.LoadSettings()
	if err != nil {
		database.Close()
		return fmt.Errorf("settings: %w", err)
	}
	settings, err := overrides.Apply(stored)
	if err != nil {
		database.Close()
		return fmt.Errorf("settings: %w", err)
	}

	a := &App{DB: database}
	a.settings.Store(settings)

	controlListener, err := control.Listen(settings)
	if err != nil {
		database.Close()
		return fmt.Errorf("control listener %s: %w",
			config.Addr(settings.ControlHost, settings.ControlPort), err)
	}
	gatewayListener, err := gateway.Listen(settings)
	if err != nil {
		controlListener.Close()
		database.Close()
		return fmt.Errorf("gateway listener %s: %w",
			config.Addr(settings.GatewayHost, settings.GatewayPort), err)
	}

	registry := providers.Default()
	a.controlRoutes = control.New(
		database,
		a.currentSettings,
		build.Current(),
		accounts.NewService(registry, accounts.NewRepository(database.Conn)),
		registry,
	)
	a.control = &http.Server{
		Handler:           a.controlRoutes,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// No WriteTimeout on the gateway: a global write deadline would break
	// future streaming responses. Slowloris protection comes from
	// ReadHeaderTimeout; per-route policy arrives with the proxy.
	a.gateway = &http.Server{
		Handler:           gateway.New(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- a.control.Serve(controlListener) }()
	go func() { errCh <- a.gateway.Serve(gatewayListener) }()

	// Background update check: advisory only. It never blocks startup,
	// never touches the gateway path, and any failure (including no
	// network) stays a state flag. Dev builds skip it entirely.
	a.checkForUpdates(context.Background(), stored.UpdateChannel)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		// A server failed on its own; drain the other one and report.
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = a.control.Shutdown(shutCtx)
		_ = a.gateway.Shutdown(shutCtx)
		_ = database.Close()
		return fmt.Errorf("server failed: %w", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = a.control.Shutdown(shutCtx)
	_ = a.gateway.Shutdown(shutCtx)
	return database.Close()
}

// currentSettings returns the settings active for this run.
func (a *App) currentSettings() config.Settings {
	return a.settings.Load().(config.Settings)
}

// checkForUpdates runs one update check in the background once the
// listeners are up.
func (a *App) checkForUpdates(ctx context.Context, channel string) {
	current := build.Current()
	if current.IsDev() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		a.controlRoutes.Updates.Check(ctx, current, channel)
	}()
}
