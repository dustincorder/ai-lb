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
	// Implemented is false for every provider until a real integration
	// lands. It exists so the UI can be honest about what works.
	Implemented bool `json:"implemented"`
}

// Registry is the set of known provider types.
type Registry struct {
	providers map[string]Descriptor
}

// Default returns the registry of providers ai-lb plans to support.
func Default() *Registry {
	return New(Codex, "Codex", Antigravity, "Antigravity")
}

// New builds a registry from id/display-name pairs.
func New(pairs ...string) *Registry {
	r := &Registry{providers: map[string]Descriptor{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		r.providers[pairs[i]] = Descriptor{
			ID:          pairs[i],
			DisplayName: pairs[i+1],
			Implemented: false,
		}
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
