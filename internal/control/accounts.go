package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dustincorder/ai-lb/internal/accounts"
)

// maxAccountsBody caps account mutation payloads. Profiles are tiny;
// anything larger is rejected before decoding.
const maxAccountsBody = 64 * 1024

// accountDTO is the only account shape the API exposes. It carries a
// derived connected flag; credentials_ref and all secret material stay
// server-side and never serialize.
type accountDTO struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Identity  string `json:"identity"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toAccountDTO(a accounts.Account) accountDTO {
	return accountDTO{
		ID:        a.ID,
		Provider:  a.Provider,
		Label:     a.Label,
		Identity:  a.Identity,
		Enabled:   a.Enabled,
		Connected: a.Connected(),
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: a.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// createAccountRequest is the public POST body. Enabled defaults to
// true; there is deliberately no credentials_ref field.
type createAccountRequest struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Identity string `json:"identity"`
	Enabled  *bool  `json:"enabled"`
}

// patchAccountRequest carries mutable fields only. Pointers distinguish
// "absent" from zero values; provider/id/timestamps/credentials_ref
// cannot be expressed here and unknown fields are rejected.
type patchAccountRequest struct {
	Label    *string `json:"label"`
	Identity *string `json:"identity"`
	Enabled  *bool   `json:"enabled"`
}

// decodeStrict parses a small JSON body with unknown fields rejected.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAccountsBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			writeJSON(w, http.StatusRequestEntityTooLarge, errorBody("body_too_large", "request body exceeds limit"))
		default:
			writeJSON(w, http.StatusBadRequest, errorBody("invalid_json", "malformed JSON or unknown field"))
		}
		return false
	}
	return true
}

func errorBody(code, message string) map[string]string {
	return map[string]string{"error": code, "message": message}
}

// accountError maps domain errors to the stable management contract.
// Raw storage errors never reach the client.
func accountError(w http.ResponseWriter, err error) {
	var ve *accounts.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, errorBody("invalid_"+ve.Field, ve.Message))
	case errors.Is(err, accounts.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody("account_not_found", "no account with this id"))
	case errors.Is(err, accounts.ErrConflict):
		writeJSON(w, http.StatusConflict, errorBody("account_conflict", "account could not be created"))
	case errors.Is(err, accounts.ErrInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, errorBody("invalid_request", "invalid account data"))
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "unexpected failure"))
	}
}

// handleProviders lists known provider types with honest integration
// flags (all false until real integrations land).
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.Providers.All()})
}

// handleAccounts serves the collection: GET lists, POST creates.
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.Accounts.List(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorBody("internal_error", "unexpected failure"))
			return
		}
		out := make([]accountDTO, 0, len(list))
		for _, a := range list {
			out = append(out, toAccountDTO(a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
	case http.MethodPost:
		var req createAccountRequest
		if !decodeStrict(w, r, &req) {
			return
		}
		created, err := s.Accounts.Create(r.Context(), accounts.CreateInput{
			Provider: req.Provider,
			Label:    req.Label,
			Identity: req.Identity,
			Enabled:  req.Enabled,
		})
		if err != nil {
			accountError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toAccountDTO(created))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
	}
}

// handleAccount serves one profile: GET, PATCH (mutable fields only),
// DELETE (metadata row for unconnected profiles).
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusNotFound, errorBody("account_not_found", "no account with this id"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		a, err := s.Accounts.Get(r.Context(), id)
		if err != nil {
			accountError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toAccountDTO(a))
	case http.MethodPatch:
		var req patchAccountRequest
		if !decodeStrict(w, r, &req) {
			return
		}
		updated, err := s.Accounts.Update(r.Context(), id, accounts.Patch{
			Label:    req.Label,
			Identity: req.Identity,
			Enabled:  req.Enabled,
		})
		if err != nil {
			accountError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toAccountDTO(updated))
	case http.MethodDelete:
		if err := s.Accounts.Delete(r.Context(), id); err != nil {
			accountError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method_not_allowed", "method not allowed"))
	}
}
