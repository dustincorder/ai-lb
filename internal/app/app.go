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

	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/control"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/gateway"
)

// shutdownTimeout bounds graceful drain of both HTTP servers.
const shutdownTimeout = 10 * time.Second

// App is the running service.
type App struct {
	DB       *db.DB
	settings atomic.Value // config.Settings
	control  *http.Server
	gateway  *http.Server
}

// Run opens the database, loads settings, binds both listeners, and
// serves until ctx is cancelled. Listeners bind before serving so a busy
// port fails fast with a clear error instead of a half-started service.
func Run(ctx context.Context, dataDir string, overrides config.Settings, useOverrides bool) error {
	database, err := db.Open(dataDir)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	stored, err := database.LoadSettings()
	if err != nil {
		database.Close()
		return fmt.Errorf("settings: %w", err)
	}
	settings := stored
	if useOverrides {
		settings = overrides
		if err := config.Validate(settings); err != nil {
			database.Close()
			return fmt.Errorf("settings override: %w", err)
		}
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

	a.control = &http.Server{Handler: control.New(database, a.currentSettings)}
	a.gateway = &http.Server{Handler: gateway.New()}

	errCh := make(chan error, 2)
	go func() { errCh <- a.control.Serve(controlListener) }()
	go func() { errCh <- a.gateway.Serve(gatewayListener) }()

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
