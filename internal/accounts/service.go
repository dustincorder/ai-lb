package accounts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dustincorder/ai-lb/internal/providers"
)

// Service orchestrates account profiles: provider validation, input
// validation, ID generation, and timestamps. HTTP handlers translate
// payloads and errors only; all rules live here. The repository and
// registry are intentionally private: external packages must go through
// the service so business rules cannot be bypassed.
type Service struct {
	registry *providers.Registry
	repo     *Repository
	// Now is a test hook for deterministic timestamps.
	Now func() time.Time
}

// NewService builds an account service. A nil Now defaults to UTC time.
func NewService(registry *providers.Registry, repo *Repository) *Service {
	return &Service{registry: registry, repo: repo, Now: func() time.Time {
		return time.Now().UTC()
	}}
}

// CreateInput is the validated shape for new profiles. Enabled defaults
// to true; pass an explicit pointer to disable at creation.
type CreateInput struct {
	Provider string
	Label    string
	Identity string
	Enabled  *bool
}

// Patch carries the mutable fields for PATCH. Nil means "leave alone".
type Patch struct {
	Label    *string
	Identity *string
	Enabled  *bool
}

// ValidationError is a field-scoped input error. Handlers map Field to
// a stable error code (invalid_<field>) with HTTP 422.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// Create validates, stamps, and stores a new unconnected profile.
func (s *Service) Create(ctx context.Context, in CreateInput) (Account, error) {
	if !s.registry.Known(in.Provider) {
		return Account{}, &ValidationError{Field: "provider", Message: providers.ErrUnknownProvider(in.Provider).Error()}
	}
	label, err := cleanLabel(in.Label)
	if err != nil {
		return Account{}, err
	}
	identity, err := cleanIdentity(in.Identity)
	if err != nil {
		return Account{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := s.Now().UTC()
	a := Account{
		ID:        uuid.NewString(),
		Provider:  in.Provider,
		Label:     label,
		Identity:  identity,
		Enabled:   enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	created, err := s.repo.Create(ctx, a)
	if err != nil {
		return Account{}, err
	}
	return created, nil
}

// Update applies an explicit patch to mutable fields only. Provider, ID,
// timestamps, and credentials_ref are never patchable through this path.
// An empty patch (nothing to change) is rejected without touching the
// row: silently bumping updated_at would be a fictitious mutation.
func (s *Service) Update(ctx context.Context, id string, p Patch) (Account, error) {
	if p.Label == nil && p.Identity == nil && p.Enabled == nil {
		return Account{}, &ValidationError{Field: "patch", Message: "empty patch changes nothing"}
	}
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if p.Label != nil {
		label, err := cleanLabel(*p.Label)
		if err != nil {
			return Account{}, err
		}
		a.Label = label
	}
	if p.Identity != nil {
		identity, err := cleanIdentity(*p.Identity)
		if err != nil {
			return Account{}, err
		}
		a.Identity = identity
	}
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	a.UpdatedAt = s.Now().UTC()
	updated, err := s.repo.Update(ctx, a)
	if err != nil {
		return Account{}, err
	}
	return updated, nil
}

// Get returns one profile or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (Account, error) {
	return s.repo.Get(ctx, id)
}

// List returns all profiles.
func (s *Service) List(ctx context.Context) ([]Account, error) {
	return s.repo.List(ctx)
}

// Delete removes the metadata row of an unconnected profile. A connected
// profile (credentials_ref set) is refused with ErrConnected: deleting
// metadata while leaving a credential reference/secret orphaned would be
// a silent data loss. When real secret persistence arrives, this method
// will orchestrate credential deletion/reconciliation first; SQLite and
// SecretStore deletes are not atomic and are not presented as such.
func (s *Service) Delete(ctx context.Context, id string) error {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if a.Connected() {
		return ErrConnected
	}
	return s.repo.Delete(ctx, id)
}

func cleanLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if label == "" {
		return "", &ValidationError{Field: "label", Message: "label is required"}
	}
	if len([]rune(label)) > MaxLabelLength {
		return "", &ValidationError{Field: "label", Message: fmt.Sprintf("label exceeds %d characters", MaxLabelLength)}
	}
	return label, nil
}

func cleanIdentity(raw string) (string, error) {
	identity := strings.TrimSpace(raw)
	// Identity is provider-specific display data, not necessarily an
	// email address, so no format validation applies — only a size bound.
	if len([]rune(identity)) > MaxIdentityLength {
		return "", &ValidationError{Field: "identity", Message: fmt.Sprintf("identity exceeds %d characters", MaxIdentityLength)}
	}
	return identity, nil
}
