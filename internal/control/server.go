// Package control implements the management (control-plane) HTTP server:
// the embedded web UI plus the /api/* management API. Same-origin by
// design; no CORS is configured.
//
// Settings model: the database holds the configured (persisted) settings
// while the process runs with the active settings its listeners actually
// bound. PUT /api/settings updates the configured values only; listener
// ports apply on restart and the response says so honestly.
package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/build"
	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/providers"
	"github.com/dustincorder/ai-lb/internal/terminal"
	"github.com/dustincorder/ai-lb/internal/update"
	web "github.com/dustincorder/ai-lb/web"
)

// Version is set at build time via -ldflags "-X ...control.Version=...".
var Version = "dev"

// maxSettingsBody caps PUT /api/settings payloads. Settings are tiny;
// anything larger is rejected before decoding.
const maxSettingsBody = 64 * 1024

// updateRepo identifies the release source for the checker.
const updateRepoOwner = "dustincorder"
const updateRepoName = "ai-lb"

// Server is the control-plane HTTP server.
type Server struct {
	DB         *db.DB
	Active     func() config.Settings
	Build      build.Info
	Updates    *update.Checker
	Accounts   *accounts.Service
	Providers  *providers.Registry
	Codex      codexBackend
	Terminal   terminal.Launcher
	Executable func() (string, error)
	mux        *http.ServeMux
}

// New builds the control server routes. active reports the settings the
// running listeners bound; the database holds the configured settings.
func New(database *db.DB, active func() config.Settings, current build.Info, acc *accounts.Service, registry *providers.Registry, codexSvc codexBackend, launchers ...terminal.Launcher) *Server {
	launcher := terminal.Launcher(terminal.New())
	if len(launchers) > 0 && launchers[0] != nil {
		launcher = launchers[0]
	}
	s := &Server{
		DB:         database,
		Active:     active,
		Build:      current,
		Updates:    update.NewChecker(update.NewClient(updateRepoOwner, updateRepoName, current.Version)),
		Accounts:   acc,
		Providers:  registry,
		Codex:      codexSvc,
		Terminal:   launcher,
		Executable: os.Executable,
		mux:        http.NewServeMux(),
	}
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/app", s.handleApp)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/gateway", s.handleGateway)
	s.mux.HandleFunc("/api/update", s.handleUpdate)
	s.mux.HandleFunc("/api/update/check", s.handleUpdateCheck)
	s.mux.HandleFunc("/api/update/download", s.handleUpdateDownload)
	s.mux.HandleFunc("/api/providers", s.handleProviders)
	s.mux.HandleFunc("/api/providers/codex/status", s.handleCodexProviderStatus)
	s.mux.HandleFunc("/api/accounts", s.handleAccounts)
	s.mux.HandleFunc("/api/accounts/{id}", s.handleAccount)
	s.mux.HandleFunc("/api/accounts/{id}/codex/login", s.handleCodexLogin)
	s.mux.HandleFunc("/api/accounts/{id}/codex/status", s.handleCodexStatus)
	s.mux.HandleFunc("/api/accounts/{id}/codex/refresh", s.handleCodexRefresh)
	s.mux.HandleFunc("/api/accounts/{id}/codex/logout", s.handleCodexLogout)
	s.mux.HandleFunc("/api/accounts/{id}/codex/launch", s.handleCodexLaunch)
	s.mux.HandleFunc("/", s.handleUI)
	return s
}

// ServeHTTP enforces the browser security baseline before routing:
// loopback Host only (DNS-rebinding guard), plus same-origin checks on
// state-changing management methods. Non-browser requests without an
// Origin header remain valid. CORS is intentionally not enabled.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackHost(r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden_host"})
		return
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost ||
		r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		if !isSameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "foreign_origin"})
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// isLoopbackHost accepts Host values whose hostname is a loopback IP or
// "localhost". Anything else (including empty) is rejected.
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// isSameOrigin allows requests without an Origin header (CLI, curl,
// non-browser clients) and requires browser requests to carry this
// server's own origin.
func isSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleHealth reports control-plane liveness.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "control"})
}

// handleApp reports version, build metadata, ACTIVE listener addresses,
// database status, and platform. It never exposes sensitive filesystem
// or auth data.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	cfg := s.Active()
	writeJSON(w, http.StatusOK, map[string]any{
		"data_dir": filepath.Dir(s.DB.Path),
		"version":  Version,
		"build": map[string]string{
			"version":    s.Build.Version,
			"commit":     s.Build.Commit,
			"build_time": s.Build.BuildTime,
			"channel":    s.Build.Channel,
		},
		"control": map[string]string{
			"host": cfg.ControlHost,
			"port": itoa(cfg.ControlPort),
			"url":  "http://" + config.Addr(cfg.ControlHost, cfg.ControlPort),
		},
		"gateway": map[string]string{
			"host": cfg.GatewayHost,
			"port": itoa(cfg.GatewayPort),
			"url":  "http://" + config.Addr(cfg.GatewayHost, cfg.GatewayPort),
		},
		"database": s.DB.Status(),
		"platform": map[string]string{
			"os":   runtime.GOOS,
			"arch": runtime.GOARCH,
		},
	})
}

// SettingsResponse is returned by PUT /api/settings.
type SettingsResponse struct {
	Settings        config.Settings `json:"settings"`
	RestartRequired bool            `json:"restart_required"`
}

// handleSettings serves the CONFIGURED settings: GET returns the persisted
// values (visible immediately after PUT); PUT validates, persists, and
// reports whether a restart is needed (configured != active).
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		configured, err := s.DB.LoadSettings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
			return
		}
		writeJSON(w, http.StatusOK, configured)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, maxSettingsBody)
		var next config.Settings
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body_too_large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if err := config.Validate(next); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if err := s.DB.SaveSettings(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed"})
			return
		}
		// The update channel applies to the next check without a restart;
		// only listener changes require one.
		active := s.Active()
		listeners := next
		listeners.UpdateChannel = active.UpdateChannel
		writeJSON(w, http.StatusOK, SettingsResponse{
			Settings:        next,
			RestartRequired: listeners != active,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
	}
}

// handleGateway proxies the ACTIVE gateway liveness probe so the browser
// UI stays same-origin (no CORS). It reports reachability, never
// credentials.
func (s *Server) handleGateway(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	cfg := s.Active()
	url := "http://" + config.Addr(cfg.GatewayHost, cfg.GatewayPort) + "/health"
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"url": url, "reachable": false, "error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"url": url, "reachable": false, "error": "invalid_response",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url": url, "reachable": true, "status": body["status"],
	})
}

// updateChannel reads the configured update channel. Settings load
// failures fail the update action, never the whole request path.
func (s *Server) updateChannel() (string, error) {
	configured, err := s.DB.LoadSettings()
	if err != nil {
		return "", err
	}
	return configured.UpdateChannel, nil
}

// handleUpdate serves the runtime update state. It never embeds raw
// GitHub JSON.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	writeJSON(w, http.StatusOK, s.Updates.Get())
}

// handleUpdateCheck runs one update check synchronously (bounded by the
// checker's short timeout) and returns the resulting state.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	channel, err := s.updateChannel()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
		return
	}
	writeJSON(w, http.StatusOK, s.Updates.Check(r.Context(), s.Build, channel))
}

// handleUpdateDownload fetches the selected artifact into a temp
// directory and accepts it only on SHA-256 match against the release
// checksums. Nothing is installed or executed.
func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	channel, err := s.updateChannel()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
		return
	}
	res, err := s.Updates.Download(r.Context(), s.Build, channel)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "downloaded",
		"path":   res.Path,
		"sha256": res.SHA256,
		"size":   res.Size,
	})
}

// handleUI serves the embedded React build with SPA fallback. Unknown
// /api/* paths get JSON 404; all other unknown paths fall back to
// index.html so client-side routing works.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		frontendMissing(w)
		return
	}
	serveUI(w, r, dist)
}

// frontendMissing explains that the production UI was not built. This is
// the expected state on backend-only checkouts and in CI before the
// frontend job runs.
func frontendMissing(w http.ResponseWriter) {
	http.Error(w, "frontend not built: run npm --prefix web run build", http.StatusServiceUnavailable)
}

// serveUI serves static assets from distFS with SPA fallback to
// index.html. distFS is a parameter (rather than the embedded FS
// directly) so the not-built and built paths are both testable.
func serveUI(w http.ResponseWriter, r *http.Request, distFS fs.FS) {
	if _, err := fs.Stat(distFS, "index.html"); err != nil {
		frontendMissing(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(distFS, path); err != nil || strings.HasSuffix(r.URL.Path, "/") {
		path = "index.html"
	}
	data, err := fs.ReadFile(distFS, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path, zeroTime, bytes.NewReader(data))
}

// Listen binds the control listener. A port conflict surfaces here as a
// clear startup error.
func Listen(cfg config.Settings) (net.Listener, error) {
	return net.Listen("tcp", config.Addr(cfg.ControlHost, cfg.ControlPort))
}

// zeroTime disables content caching heuristics for embedded assets;
// the binary itself is versioned, so client caching is not needed yet.
var zeroTime = time.Time{}

func itoa(n int) string {
	return strconv.Itoa(n)
}
