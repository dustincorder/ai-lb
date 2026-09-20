package secrets

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()

	secret := []byte("oauth-material")
	if err := m.Put(ctx, "ref", secret); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Mutating the caller's slice must not affect storage.
	secret[0] = 'X'

	got, err := m.Get(ctx, "ref")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "oauth-material" {
		t.Errorf("stored bytes changed: %q", got)
	}
	// Mutating the returned slice must not affect storage either.
	got[0] = 'Y'
	again, err := m.Get(ctx, "ref")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(again) != "oauth-material" {
		t.Errorf("store aliases returned slices: %q", again)
	}

	if err := m.Delete(ctx, "ref"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, "ref"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := m.Delete(ctx, "ref"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ref := string(rune('a' + i))
			_ = m.Put(ctx, ref, []byte{byte(i)})
			_, _ = m.Get(ctx, ref)
			_ = m.Delete(ctx, ref)
		}(i)
	}
	wg.Wait()
}

func TestUnavailableStore(t *testing.T) {
	ctx := context.Background()
	var s Store = UnavailableStore{}
	if err := s.Put(ctx, "ref", []byte("x")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Put = %v, want ErrUnavailable", err)
	}
	if _, err := s.Get(ctx, "ref"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(ctx, "ref"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Delete = %v, want ErrUnavailable", err)
	}
}
