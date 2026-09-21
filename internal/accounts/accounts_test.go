package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dustincorder/ai-lb/internal/db"
	"github.com/dustincorder/ai-lb/internal/providers"
)

func testService(t *testing.T) (*Service, *Repository, func()) {
	t.Helper()
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	repo := NewRepository(database.Conn)
	svc := NewService(providers.Default(), repo)
	svc.Now = func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) }
	return svc, repo, func() { database.Close() }
}

func TestCreateGetListDelete(t *testing.T) {
	svc, _, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "Personal", Identity: "me@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" || a.Provider != providers.Codex || !a.Enabled {
		t.Errorf("unexpected created account: %+v", a)
	}
	if a.Connected() {
		t.Error("new profile must not be connected")
	}
	if a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		t.Error("timestamps must be populated")
	}
	if a.CreatedAt.Location() != time.UTC {
		t.Error("timestamps must be UTC")
	}

	got, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != a {
		t.Errorf("Get returned %+v, want %+v", got, a)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Errorf("List = %+v, want one account", list)
	}

	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete = %v, want ErrNotFound", err)
	}
}

func TestUpdateAllowedFieldsProviderImmutable(t *testing.T) {
	svc, _, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Antigravity, Label: "Work"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	label, identity, enabled := "Renamed", "id@example.com", false
	updated, err := svc.Update(ctx, a.ID, Patch{Label: &label, Identity: &identity, Enabled: &enabled})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Label != "Renamed" || updated.Identity != "id@example.com" || updated.Enabled {
		t.Errorf("patch not applied: %+v", updated)
	}
	if updated.Provider != providers.Antigravity {
		t.Errorf("provider must stay %q, got %q", providers.Antigravity, updated.Provider)
	}
	if updated.CredentialsRef != "" {
		t.Error("update must not touch credentials_ref")
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _, done := testService(t)
	defer done()
	ctx := context.Background()

	if _, err := svc.Create(ctx, CreateInput{Provider: "banana-ai", Label: "x"}); err == nil {
		t.Error("unknown provider must be rejected")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "provider" {
			t.Errorf("expected provider ValidationError, got %v", err)
		}
	}
	for _, tc := range []struct {
		name  string
		label string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"too long", strings.Repeat("x", MaxLabelLength+1)},
	} {
		if _, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: tc.label}); err == nil {
			t.Errorf("label %q must be rejected", tc.name)
		}
	}
	// Label is trimmed; identity is optional and not email-validated.
	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "  Padded  ", Identity: "not-an-email"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Label != "Padded" || a.Identity != "not-an-email" {
		t.Errorf("unexpected normalization: %+v", a)
	}
	if _, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "ok", Identity: strings.Repeat("y", MaxIdentityLength+1)}); err == nil {
		t.Error("oversized identity must be rejected")
	}
	disabled := false
	d, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "off", Enabled: &disabled})
	if err != nil {
		t.Fatalf("Create disabled: %v", err)
	}
	if d.Enabled {
		t.Error("explicit enabled=false must be honored")
	}
}

func TestCredentialsRefInternalOnly(t *testing.T) {
	svc, repo, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "creds"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate the future auth layer linking a secret, then confirm the
	// public read path only exposes the derived boolean.
	if err := repo.SetCredentialsRef(ctx, a.ID, "codex:test:oauth", svc.Now()); err != nil {
		t.Fatalf("SetCredentialsRef: %v", err)
	}
	got, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Connected() {
		t.Error("linked profile must report connected")
	}
	if err := repo.SetCredentialsRef(ctx, a.ID, "", svc.Now()); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	got, err = svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Connected() {
		t.Error("unlinked profile must report not connected")
	}
}

func TestEmptyPatchRejectedWithoutTouchingRow(t *testing.T) {
	svc, _, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "x"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Update(ctx, a.ID, Patch{}); err == nil {
		t.Fatal("empty patch must be rejected")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
	}
	got, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.UpdatedAt.Equal(a.UpdatedAt) {
		t.Error("rejected empty patch must not bump updated_at")
	}
}

func TestConnectedDeleteRefused(t *testing.T) {
	svc, repo, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "linked"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetCredentialsRef(ctx, a.ID, "codex:test:oauth", svc.Now()); err != nil {
		t.Fatalf("SetCredentialsRef: %v", err)
	}
	if err := svc.Delete(ctx, a.ID); !errors.Is(err, ErrConnected) {
		t.Fatalf("connected delete = %v, want ErrConnected", err)
	}
	// Row must remain.
	if _, err := svc.Get(ctx, a.ID); err != nil {
		t.Errorf("connected row must survive refused delete: %v", err)
	}
	// Unconnected profiles still delete normally.
	b, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "plain"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, b.ID); err != nil {
		t.Errorf("unconnected delete = %v, want nil", err)
	}
}

func TestDuplicateProviderAccountRejected(t *testing.T) {
	svc, _, done := testService(t)
	defer done()
	ctx := context.Background()

	a, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "A"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "B"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.CompleteProviderConnection(ctx, a.ID, "ref-a", "a@e.com", "acct-X"); err != nil {
		t.Fatalf("bind A: %v", err)
	}
	// Same upstream identity on the second profile must fail atomically.
	if _, err := svc.CompleteProviderConnection(ctx, b.ID, "ref-b", "b@e.com", "acct-X"); !errors.Is(err, ErrProviderAccountAlreadyConnected) {
		t.Fatalf("duplicate must map to typed error, got %v", err)
	}
	// B stays fully unbound: no ref, no pid, original identity kept.
	bb, err := svc.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if bb.Connected() || bb.ProviderAccountID != "" || bb.Identity != "" {
		t.Errorf("failed duplicate must leave B untouched: %+v", bb)
	}
	// A untouched.
	aa, err := svc.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if !aa.Connected() || aa.ProviderAccountID != "acct-X" || aa.Identity != "a@e.com" {
		t.Errorf("A must stay intact: %+v", aa)
	}
	// Same opaque ID under a different provider is allowed.
	c, err := svc.Create(ctx, CreateInput{Provider: providers.Antigravity, Label: "C"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.CompleteProviderConnection(ctx, c.ID, "ref-c", "", "acct-X"); err != nil {
		t.Errorf("cross-provider same pid must be allowed: %v", err)
	}
	// Empty pid never collides.
	d, err := svc.Create(ctx, CreateInput{Provider: providers.Codex, Label: "D"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.CompleteProviderConnection(ctx, d.ID, "ref-d", "", ""); err != nil {
		t.Errorf("empty pid must be allowed: %v", err)
	}
	// Finder resolves the bound profile for conflict reporting.
	found, err := svc.ConnectedLabel(ctx, providers.Codex, "acct-X")
	if err != nil {
		t.Fatalf("ConnectedLabel: %v", err)
	}
	if found != "A" {
		t.Errorf("ConnectedLabel = %q, want A", found)
	}
	if _, err := svc.ConnectedLabel(ctx, providers.Codex, "acct-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown pid must be ErrNotFound, got %v", err)
	}
}
