package secrets

import "context"

// UnavailableStore is the production store until OS keychain backends
// land. Every operation fails closed with ErrUnavailable so no flow can
// silently proceed without secret persistence.
type UnavailableStore struct{}

// Put always fails closed.
func (UnavailableStore) Put(_ context.Context, _ string, _ []byte) error {
	return ErrUnavailable
}

// Get always fails closed.
func (UnavailableStore) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, ErrUnavailable
}

// Delete always fails closed.
func (UnavailableStore) Delete(_ context.Context, _ string) error {
	return ErrUnavailable
}
