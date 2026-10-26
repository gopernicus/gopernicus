package serviceaccount

import (
	"context"
	"time"
)

// The service-account lifecycle statuses. An account with no status row is
// StatusActive.
const (
	// StatusActive is the default: the account's keys authenticate and new
	// keys may be minted.
	StatusActive Status = "active"
	// StatusDisabled is a reversible pause: no key of the account
	// authenticates and no key is minted. Disabling revokes every unrevoked
	// key, so enabling restores minting only.
	StatusDisabled Status = "disabled"
)

// Status is a service account's lifecycle status.
type Status string

// StatusRepository is the OPTIONAL service-account status capability (SOC 2
// S4d), backed by its own table (migration 0027), so the
// ServiceAccountRepository port is unchanged. A host reads it only when it sets
// AdministrationConfig.ServiceAccountStatus.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - GetStatus and Statuses report an id without a row as StatusActive.
//   - Disable and Enable for an unknown account id → sdk.ErrNotFound.
//   - ProbeSchema → sdk.ErrNotFound when the table is absent.
type StatusRepository interface {
	// GetStatus returns the account's status; an absent row is StatusActive.
	GetStatus(ctx context.Context, id string) (Status, error)
	// Statuses returns the statuses of ids in one read; an id without a row
	// is StatusActive.
	Statuses(ctx context.Context, ids []string) (map[string]Status, error)
	// changedBy is the acting principal as "<type>:<id>", kept for the record.
	//
	// Disable upserts StatusDisabled and revokes every unrevoked key of the
	// account as of now, in ONE transaction: a failing revoke leaves no status
	// row. Changed is false (and nothing is revoked) when it was already
	// disabled.
	Disable(ctx context.Context, id, changedBy string, now time.Time) (StatusChange, error)
	// Enable records StatusActive. It restores minting only: revoked keys stay
	// revoked. Changed is false when it was already active.
	Enable(ctx context.Context, id, changedBy string, now time.Time) (StatusChange, error)
	// Delete removes the account's status row; an absent row is not an error.
	Delete(ctx context.Context, id string) error
	// ProbeSchema reports sdk.ErrNotFound when the status table is absent.
	ProbeSchema(ctx context.Context) error
}

// StatusChange reports one Disable or Enable: whether THIS call changed the
// status, and how many keys a disable revoked.
type StatusChange struct {
	Changed     bool
	KeysRevoked int
}
