package control

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
	"github.com/dustincorder/ai-lb/internal/providers/codex"
)

// codexBackend is the Codex integration surface the handlers need. The
// production *codex.Service implements it; tests inject a stub. Keeping
// the interface narrow keeps the fake honest: routing, validation, and
// DTO mapping are tested here, subprocess behavior in package codex.
type codexBackend interface {
	Detect(ctx context.Context) codex.Detection
	Compatible(ctx context.Context) bool
	StartLogin(ctx context.Context, accountID string, method codex.LoginMethod) (codex.LoginSession, error)
	GetLogin(accountID string) codex.LoginSession
	CancelLogin(ctx context.Context, accountID, loginID string) (codex.LoginSession, error)
	ReadAccount(ctx context.Context, accountID string) (codex.AccountInfo, error)
	ReadRateLimits(ctx context.Context, accountID string) (codex.QuotaSnapshot, error)
	RefreshRateLimits(ctx context.Context, accountID string) (codex.QuotaSnapshot, error)
	Logout(ctx context.Context, accountID string) error
	Close()
}

// Codex routes are provider-scoped on purpose: no generic abstraction
// is built for Antigravity, which does not exist yet. Every route
// rejects non-Codex account IDs, and no response ever carries tokens,
// paths, refs, or raw upstream payloads.

// codexStatusDTO is GET /api/providers/codex/status.
type codexStatusDTO struct {
	Installed           bool   `json:"installed"`
	Path                string `json:"path,omitempty"`
	Version             string `json:"version,omitempty"`
	AppServerCompatible bool   `json:"app_server_compatible"`
	Error               string `json:"error,omitempty"`
}

// codexAccountStatusDTO is GET /api/accounts/{id}/codex/status.
type codexAccountStatusDTO struct {
	Connected          bool           `json:"connected"`
	AuthMode           string         `json:"auth_mode,omitempty"`
	Email              string         `json:"email,omitempty"`
	PlanType           string         `json:"plan_type,omitempty"`
	RequiresOpenaiAuth bool           `json:"requires_openai_auth"`
	Quota              *codexQuotaDTO `json:"quota,omitempty"`
	ObservedAt         string         `json:"observed_at"`
}

type codexQuotaDTO struct {
	Windows   []codexWindowDTO `json:"windows"`
	UpdatedAt string           `json:"updated_at"`
	Stale     bool             `json:"stale"`
}

type codexWindowDTO struct {
	LimitID            string `json:"limit_id,omitempty"`
	LimitName          string `json:"limit_name,omitempty"`
	UsedPercent        *int   `json:"used_percent,omitempty"`
	RemainingPercent   *int   `json:"remaining_percent,omitempty"`
	WindowDurationMins *int64 `json:"window_duration_minutes,omitempty"`
	ResetAt            *int64 `json:"reset_at,omitempty"`
}

func toCodexQuotaDTO(q codex.QuotaSnapshot) *codexQuotaDTO {
	out := &codexQuotaDTO{
		Windows:   make([]codexWindowDTO, 0, len(q.Windows)),
		UpdatedAt: q.UpdatedAt.UTC().Format(time.RFC3339Nano),
		Stale:     q.Stale,
	}
	for _, w := range q.Windows {
		out.Windows = append(out.Windows, codexWindowDTO{
			LimitID:            w.LimitID,
			LimitName:          w.LimitName,
			UsedPercent:        w.UsedPercent,
			RemainingPercent:   w.RemainingPercent,
			WindowDurationMins: w.WindowDurationMins,
			ResetAt:            w.ResetAt,
		})
	}
	return out
}

// codexError maps integration errors to stable codes. Raw subprocess
// output never reaches clients.
func codexError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, codex.ErrCodexNotInstalled):
		writeJSON(w, http.StatusServiceUnavailable, errorBody("codex_not_installed", "Codex CLI is not installed"))
	case errors.Is(err, codex.ErrCodexIncompatible):
		writeJSON(w, http.StatusServiceUnavailable, errorBody("codex_incompatible", "Codex CLI cannot serve the account API"))
	case errors.Is(err, codex.ErrCodexNotConnected):
		writeJSON(w, http.StatusConflict, errorBody("codex_not_connected", "account is not connected"))
	case errors.Is(err, codex.ErrCredentialStoreUnavailable):
		writeJSON(w, http.StatusConflict, errorBody("codex_insecure_credential_storage", "managed credentials are not in safe OS keyring storage"))
	case errors.Is(err, codex.ErrLoginInProgress):
		writeJSON(w, http.StatusConflict, errorBody("login_in_progress", "a login is already running for this account"))
	case errors.Is(err, codex.ErrCodexProcessFailed):
		writeJSON(w, http.StatusBadGateway, errorBody("codex_process_failed", "Codex subprocess failed"))
	case errors.Is(err, codex.ErrCodexProtocol):
		writeJSON(w, http.StatusBadGateway, errorBody("codex_protocol_error", "Codex protocol error"))
	case errors.Is(err, accounts.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody("account_not_found", "no account with this id"))
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "unexpected failure"))
	}
}

// requireCodexAccount loads the profile and rejects anything that is
// not a Codex account.
func (s *Server) requireCodexAccount(r *http.Request) (accounts.Account, bool, http.Handler) {
	a, err := s.Accounts.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return accounts.Account{}, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			accountError(w, err)
		})
	}
	if a.Provider != "codex" {
		return accounts.Account{}, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody("invalid_provider", "route only serves codex accounts"))
		})
	}
	return a, true, nil
}

// handleCodexProviderStatus reports CLI installation and app-server
// compatibility. Missing CLI is data (installed=false), not a 500.
func (s *Server) handleCodexProviderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
		return
	}
	d := s.Codex.Detect(r.Context())
	out := codexStatusDTO{
		Installed: d.Installed,
		Path:      d.Path,
		Version:   d.Version,
		Error:     d.Error,
	}
	if d.Installed {
		out.AppServerCompatible = s.Codex.Compatible(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCodexLogin multiplexes the login session resource.
func (s *Server) handleCodexLogin(w http.ResponseWriter, r *http.Request) {
	a, ok, h := s.requireCodexAccount(r)
	if !ok {
		h.ServeHTTP(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Method string `json:"method"`
		}
		if !decodeStrict(w, r, &req) {
			return
		}
		var method codex.LoginMethod
		switch req.Method {
		case "", "browser":
			method = codex.LoginBrowser
		case "device":
			method = codex.LoginDevice
		default:
			writeJSON(w, http.StatusUnprocessableEntity, errorBody("invalid_method", "method must be browser or device"))
			return
		}
		sess, err := s.Codex.StartLogin(r.Context(), a.ID, method)
		if err != nil {
			codexError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, sess)
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.Codex.GetLogin(a.ID))
	case http.MethodDelete:
		var req struct {
			LoginID string `json:"login_id"`
		}
		// Cancel carries an optional body; empty body cancels the
		// running session for the account.
		if r.ContentLength != 0 {
			if !decodeStrict(w, r, &req) {
				return
			}
		}
		if req.LoginID == "" {
			cur := s.Codex.GetLogin(a.ID)
			req.LoginID = cur.LoginID
		}
		sess, err := s.Codex.CancelLogin(r.Context(), a.ID, req.LoginID)
		if err != nil {
			codexError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sess)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
	}
}

// handleCodexStatus returns live account info plus quota.
func (s *Server) handleCodexStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
		return
	}
	a, ok, h := s.requireCodexAccount(r)
	if !ok {
		h.ServeHTTP(w, r)
		return
	}
	info, err := s.Codex.ReadAccount(r.Context(), a.ID)
	if err != nil {
		codexError(w, err)
		return
	}
	out := codexAccountStatusDTO{
		Connected:          info.Connected,
		AuthMode:           info.AuthMode,
		Email:              info.Email,
		PlanType:           info.PlanType,
		RequiresOpenaiAuth: info.RequiresOpenaiAuth,
		ObservedAt:         info.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	if info.Connected {
		if q, err := s.Codex.ReadRateLimits(r.Context(), a.ID); err == nil {
			out.Quota = toCodexQuotaDTO(q)
		}
		// Quota failure does not fail status: the account read is the
		// authoritative part; quota has its own refresh route.
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCodexRefresh forces a live quota read, bypassing the snapshot
// cache that GET status uses.
func (s *Server) handleCodexRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
		return
	}
	a, ok, h := s.requireCodexAccount(r)
	if !ok {
		h.ServeHTTP(w, r)
		return
	}
	q, err := s.Codex.RefreshRateLimits(r.Context(), a.ID)
	if err != nil {
		codexError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCodexQuotaDTO(q))
}

// handleCodexLogout disconnects with verification; the binding stays on
// any failure (fail closed).
func (s *Server) handleCodexLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
		return
	}
	a, ok, h := s.requireCodexAccount(r)
	if !ok {
		h.ServeHTTP(w, r)
		return
	}
	if err := s.Codex.Logout(r.Context(), a.ID); err != nil {
		codexError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}
