package secrets

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory Store for tests. It is concurrency-safe,
// copies bytes across the API boundary in both directions, and
// best-effort zeroes the stored slice on Delete. It offers no
// cryptographic guarantees beyond process memory isolation.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: map[string][]byte{}}
}

// Put stores a copy of secret under ref, replacing any previous value.
func (m *MemoryStore) Put(_ context.Context, ref string, secret []byte) error {
	cp := make([]byte, len(secret))
	copy(cp, secret)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[ref] = cp
	return nil
}

// Get returns a copy of the stored secret or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, ref string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.data[ref]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(stored))
	copy(cp, stored)
	return cp, nil
}

// Delete removes the secret (zeroing the stored slice best-effort) or
// returns ErrNotFound.
func (m *MemoryStore) Delete(_ context.Context, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.data[ref]
	if !ok {
		return ErrNotFound
	}
	for i := range stored {
		stored[i] = 0
	}
	delete(m.data, ref)
	return nil
}
