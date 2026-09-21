// Package providers holds the provider registry: the known provider
// types (codex, antigravity) with display metadata. Real provider
// integrations (login, quota, switching) do not exist yet and are
// explicitly out of scope here; the full adapter contract will be
// defined alongside the first real integration. The architecture
// direction is recorded in docs/RFC-0001-v0.1.md.
package providers

import (
	"fmt"
	"sort"
)

// Known provider IDs.
const (
	Codex       = "codex"
	Antigravity = "antigravity"
)

// Descriptor describes one provider type for the API and UI.
type Descriptor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// Implemented reports whether ai-lb integrates the provider. It
	// exists so the UI can be honest about what works.
	Implemented bool `json:"implemented"`
}

// Registry is the set of known provider types.
type Registry struct {
	providers map[string]Descriptor
}

// New builds a registry from descriptors. Empty IDs, empty display
// names, and duplicate IDs are rejected: the registry is a source of
// truth and must fail loudly on bad input rather than silently drop it.
func New(descriptors ...Descriptor) (*Registry, error) {
	r := &Registry{providers: map[string]Descriptor{}}
	for _, d := range descriptors {
		if d.ID == "" {
			return nil, fmt.Errorf("provider descriptor has empty id")
		}
		if d.DisplayName == "" {
			return nil, fmt.Errorf("provider %q has empty display name", d.ID)
		}
		if _, dup := r.providers[d.ID]; dup {
			return nil, fmt.Errorf("duplicate provider id %q", d.ID)
		}
		r.providers[d.ID] = d
	}
	return r, nil
}

// Default returns the registry of providers ai-lb plans to support.
// Codex has a real managed-account integration; Antigravity does not
// yet. Implemented means "ai-lb integrates this provider" — not "CLI
// installed", "account connected", or "provider healthy"; those are
// separate runtime states. The descriptors are compile-time known, so
// construction cannot fail.
func Default() *Registry {
	r, err := New(
		Descriptor{ID: Codex, DisplayName: "Codex", Implemented: true},
		Descriptor{ID: Antigravity, DisplayName: "Antigravity"},
	)
	if err != nil {
		panic(err)
	}
	return r
}

// All returns descriptors sorted by ID.
func (r *Registry) All() []Descriptor {
	out := make([]Descriptor, 0, len(r.providers))
	for _, d := range r.providers {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns the descriptor and whether the provider is known.
func (r *Registry) Lookup(id string) (Descriptor, bool) {
	d, ok := r.providers[id]
	return d, ok
}

// Known reports whether id is a registered provider.
func (r *Registry) Known(id string) bool {
	_, ok := r.providers[id]
	return ok
}

// ErrUnknownProvider formats the validation error for account creation.
func ErrUnknownProvider(id string) error {
	return fmt.Errorf("unknown provider %q", id)
}
