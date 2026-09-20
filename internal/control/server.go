// Package control implements the management (control-plane) HTTP server:
// the embedded web UI plus the /api/* management API. Same-origin by
// design; no CORS is configured.
package control

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dustincorder/ai-lb/internal/config"
	"github.com/dustincorder/ai-lb/internal/db"
	web "github.com/dustincorder/ai-lb/web"
)

// Version is set at build time via -ldflags "-X ...control.Version=...".
var Version = "dev"

// Server is the control-plane HTTP server.
type Server struct {
	DB       *db.DB
	Settings func() config.Settings
	mux      *http.ServeMux
}

// New builds the control server routes.
func New(database *db.DB, settings func() config.Settings) *Server {
	s := &Server{DB: database, Settings: settings, mux: http.NewServeMux()}
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/app", s.handleApp)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/gateway", s.handleGateway)
	s.mux.HandleFunc("/", s.handleUI)
	return s
}

// ServeHTTP dispatches requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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

// handleApp reports version, listener addresses, database status, and
// platform. It never exposes sensitive filesystem or auth data.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	cfg := s.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version,
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

// handleSettings reads (GET) or replaces (PUT) the persisted settings.
// Listener ports apply on restart; the response says so honestly.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.Settings())
	case http.MethodPut:
		var next config.Settings
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if err := config.Validate(next); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		current := s.Settings()
		if err := s.DB.SaveSettings(next); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed"})
			return
		}
		writeJSON(w, http.StatusOK, SettingsResponse{
			Settings:        next,
			RestartRequired: next != current,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
	}
}

// handleGateway proxies the gateway liveness probe so the browser UI
// stays same-origin (no CORS). It reports reachability, never credentials.
func (s *Server) handleGateway(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	cfg := s.Settings()
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
		http.Error(w, "frontend not built: run npm --prefix web run build", http.StatusServiceUnavailable)
		return
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		http.Error(w, "frontend not built: run npm --prefix web run build", http.StatusServiceUnavailable)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(dist, path); err != nil || strings.HasSuffix(r.URL.Path, "/") {
		path = "index.html"
	}
	data, err := fs.ReadFile(dist, path)
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
