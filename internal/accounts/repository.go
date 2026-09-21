package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Typed repository errors.
var (
	// ErrNotFound means no account exists for the ID.
	ErrNotFound = errors.New("account not found")
	// ErrConflict means the account could not be created as requested
	// (e.g. an ID collision).
	ErrConflict = errors.New("account conflict")
	// ErrInvalid means the record violates storage constraints.
	ErrInvalid = errors.New("invalid account")
)

// timeFormat stores UTC timestamps as sortable text.
const timeFormat = time.RFC3339Nano

// Repository is the SQLite data-access layer for accounts. It owns SQL
// and constraint mapping only; validation and business rules live in
// Service, and HTTP handlers must never touch it directly.
type Repository struct {
	db *sql.DB
}

// NewRepository builds a repository over an open database connection.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func scanAccount(row interface {
	Scan(dest ...any) error
}) (Account, error) {
	var a Account
	var enabled int
	var created, updated string
	if err := row.Scan(
		&a.ID, &a.Provider, &a.Label, &a.Identity,
		&enabled, &a.CredentialsRef, &a.ProviderAccountID, &created, &updated,
	); err != nil {
		return Account{}, err
	}
	a.Enabled = enabled == 1
	var err error
	if a.CreatedAt, err = time.Parse(timeFormat, created); err != nil {
		return Account{}, fmt.Errorf("%w: bad created_at", ErrInvalid)
	}
	if a.UpdatedAt, err = time.Parse(timeFormat, updated); err != nil {
		return Account{}, fmt.Errorf("%w: bad updated_at", ErrInvalid)
	}
	return a, nil
}

// Create inserts a new account profile.
func (r *Repository) Create(ctx context.Context, a Account) (Account, error) {
	if a.ID == "" || a.Provider == "" {
		return Account{}, fmt.Errorf("%w: id and provider are required", ErrInvalid)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO accounts(id, provider, label, identity, enabled, credentials_ref, provider_account_id, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Provider, a.Label, a.Identity, boolToInt(a.Enabled),
		a.CredentialsRef, a.ProviderAccountID, a.CreatedAt.UTC().Format(timeFormat), a.UpdatedAt.UTC().Format(timeFormat),
	)
	if err != nil {
		if isConstraintError(err) {
			return Account{}, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return Account{}, err
	}
	return a, nil
}

// Get returns one account by ID or ErrNotFound.
func (r *Repository) Get(ctx context.Context, id string) (Account, error) {
	a, err := scanAccount(r.db.QueryRowContext(ctx,
		`SELECT id, provider, label, identity, enabled, credentials_ref, provider_account_id, created_at, updated_at
		 FROM accounts WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	return a, nil
}

// List returns all accounts ordered by creation time.
func (r *Repository) List(ctx context.Context) ([]Account, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, provider, label, identity, enabled, credentials_ref, provider_account_id, created_at, updated_at
		 FROM accounts ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Update writes back mutable fields (label, identity, enabled) plus the
// updated timestamp. Provider and ID are immutable; credentials_ref is
// managed exclusively through SetCredentialsRef.
func (r *Repository) Update(ctx context.Context, a Account) (Account, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE accounts SET label = ?, identity = ?, enabled = ?, updated_at = ?
		 WHERE id = ? AND provider = ?`,
		a.Label, a.Identity, boolToInt(a.Enabled),
		a.UpdatedAt.UTC().Format(timeFormat), a.ID, a.Provider,
	)
	if err != nil {
		if isConstraintError(err) {
			return Account{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return Account{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Account{}, err
	}
	if n == 0 {
		return Account{}, ErrNotFound
	}
	return a, nil
}

// SetCredentialsRef links (or unlinks, with ref == "") the opaque
// credential binding. Only provider integrations use this; the public
// management API has no path to it.
func (r *Repository) SetCredentialsRef(ctx context.Context, id, ref string, updated time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE accounts SET credentials_ref = ?, updated_at = ? WHERE id = ?`,
		ref, updated.UTC().Format(timeFormat), id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetConnection links (or unlinks, with ref == "") the credential
// binding and adopts the provider identity in one statement, so the
// two metadata changes commit atomically — a failed connection never
// leaves a binding without its identity or vice versa. Empty identity
// leaves the stored value unchanged. Only provider integrations use
// this; the public management API has no path to it.
func (r *Repository) SetConnection(ctx context.Context, id, ref, identity, providerAccountID string, updated time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE accounts
		 SET credentials_ref = ?,
		     identity = CASE WHEN ? <> '' THEN ? ELSE identity END,
		     provider_account_id = ?,
		     updated_at = ?
		 WHERE id = ?`,
		ref, identity, identity, providerAccountID, updated.UTC().Format(timeFormat), id,
	)
	if err != nil {
		if isDuplicateProviderAccount(err) {
			return ErrProviderAccountAlreadyConnected
		}
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindByProviderAccountID returns the profile binding an upstream
// account identity, if any. Used for duplicate-conflict reporting;
// the opaque ID itself never leaves the backend.
func (r *Repository) FindByProviderAccountID(ctx context.Context, provider, providerAccountID string) (Account, error) {
	if providerAccountID == "" {
		return Account{}, ErrNotFound
	}
	a, err := scanAccount(r.db.QueryRowContext(ctx,
		`SELECT id, provider, label, identity, enabled, credentials_ref, provider_account_id, created_at, updated_at
		 FROM accounts WHERE provider = ? AND provider_account_id = ?`, provider, providerAccountID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	return a, nil
}

// isDuplicateProviderAccount detects the partial unique index
// violation for (provider, provider_account_id) without importing
// driver-specific types: the conflicting columns travel in the message
// (SQLite reports columns, not the index name).
func isDuplicateProviderAccount(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "accounts.provider_account_id")
}

// Delete removes the metadata row. Deletion of real secret material,
// once auth exists, is orchestrated by AccountService, not by this
// method: SQLite and SecretStore deletes are not one atomic transaction
// and must never be presented as such.
func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isConstraintError detects SQLite constraint violations without
// importing driver-specific types into this package: modernc errors
// carry SQLITE_CONSTRAINT details in their message.
func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, code := range []string{"constraint failed", "unique constraint", "check constraint", "not null constraint"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}
