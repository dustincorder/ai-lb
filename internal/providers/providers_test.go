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
		if d.Implemented {
			t.Errorf("provider %q must report implemented=false until a real integration lands", id)
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
