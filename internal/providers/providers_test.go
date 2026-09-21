package providers

import "testing"

func TestRegistryContents(t *testing.T) {
	r := Default()
	for _, id := range []string{Codex, Antigravity} {
		d, ok := r.Lookup(id)
		if !ok {
			t.Errorf("provider %q must exist", id)
			continue
		}
		if d.ID != id || d.DisplayName == "" {
			t.Errorf("bad descriptor: %+v", d)
		}
		if want := id == Codex; d.Implemented != want {
			t.Errorf("provider %q implemented = %v, want %v", id, d.Implemented, want)
		}
	}
	if r.Known("banana-ai") {
		t.Error("unknown provider must be rejected")
	}
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(all))
	}
	if all[0].ID == all[1].ID {
		t.Error("provider IDs must be unique")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(
		Descriptor{ID: Codex, DisplayName: "Codex"},
		Descriptor{ID: Codex, DisplayName: "Codex Duplicate"},
	); err == nil {
		t.Error("duplicate provider id must be rejected")
	}
	if _, err := New(Descriptor{ID: "", DisplayName: "Nameless"}); err == nil {
		t.Error("empty provider id must be rejected")
	}
	if _, err := New(Descriptor{ID: "x", DisplayName: ""}); err == nil {
		t.Error("empty display name must be rejected")
	}
	if _, err := New(Descriptor{ID: "x", DisplayName: "X"}); err != nil {
		t.Errorf("valid descriptor must be accepted: %v", err)
	}
}
