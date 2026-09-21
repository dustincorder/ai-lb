// Package accounts owns provider-neutral account profiles: the domain
// model, SQLite repository, and service layer. Profiles are local
// metadata only — no authentication, quota, or provider logic lives
// here. Secrets stay behind the SecretStore contract (internal/secrets)
// and are referenced by CredentialsRef, which never leaves the backend.
package accounts

import (
	"errors"
	"time"
)

// Typed domain errors shared by repository and service.
var (
	// ErrConnected means the profile still references a stored secret
	// and must not be deleted as plain metadata.
	ErrConnected = errors.New("account is connected")
	// ErrProviderAccountAlreadyConnected means another profile of the
	// same provider already binds the same upstream account identity.
	ErrProviderAccountAlreadyConnected = errors.New("provider account already connected")
)

// MaxLabelLength and MaxIdentityLength bound stored strings; the API
// rejects anything larger before it reaches SQLite.
const (
	MaxLabelLength    = 200
	MaxIdentityLength = 200
)

// Account is a local profile for one provider account.
//
// Enabled means the operator allowed ai-lb to use this account. It says
// nothing about health, authentication, or quota — those are separate
// future runtime concepts, deliberately not folded into a status field.
//
// ProviderAccountID is the opaque provider-owned identity (never email,
// never the local UUID, never a secret) used to deduplicate profiles
// against one upstream quota identity. Empty means unknown. It never
// leaves the backend: no public DTO carries it.
type Account struct {
	ID                string
	Provider          string
	Label             string
	Identity          string
	Enabled           bool
	CredentialsRef    string
	ProviderAccountID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Connected derives whether a secret was ever stored for this profile.
// It is the only credential-related fact the API may expose.
func (a Account) Connected() bool {
	return a.CredentialsRef != ""
}
