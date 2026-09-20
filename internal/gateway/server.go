// Package gateway implements the data-plane HTTP server. In the current
// version it exposes only GET /health as a liveness probe; OpenAI
// compatibility is future work and must not be assumed from this stub.
package gateway

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/dustincorder/ai-lb/internal/config"
)

// Server is the gateway (data-plane) HTTP server.
type Server struct {
	mux *http.ServeMux
}

// New builds the gateway routes.
func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/", s.handleNotFound)
	return s
}

// ServeHTTP dispatches requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleHealth reports gateway liveness.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleNotFound answers every non-health route with a controlled JSON
// error. No blind pass-through exists in this version.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Listen binds the gateway listener. A port conflict surfaces here as a
// clear startup error.
func Listen(cfg config.Settings) (net.Listener, error) {
	return net.Listen("tcp", config.Addr(cfg.GatewayHost, cfg.GatewayPort))
}
