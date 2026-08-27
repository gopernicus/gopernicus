package user

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// Status is the account lifecycle posture (coordination-hub-auth-upstream
// CHAU-1.1). The vocabulary is CLOSED to exactly two values in v1:
//
//   - StatusActive — the default posture. Every existing row and every newly
//     constructed user is active.
//   - StatusDeactivated — no new session and no act-as-user authentication may
//     succeed for this subject.
//
// Suspension, deletion, anonymization, lock-until, and arbitrary host-defined
// statuses are deliberately OUT of scope: each needs its own semantics on every
// credential path, and an open string would let a host invent a posture the
// feature has no rules for. Adding a third value is a design decision, not a
// constant.
//
// Status is NOT verification. Whether an address is proven is identifier state
// (identifier.VerifiedAt); whether the subject may authenticate at all is this.
// A verified user can be deactivated and an unverified user can be active.
type Status string

const (
	// StatusActive is the default lifecycle posture: authentication proceeds
	// normally, subject to every other check.
	StatusActive Status = "active"
	// StatusDeactivated denies every new session and act-as-user authentication
	// for the subject. Existing sessions are revoked by the transition itself.
	StatusDeactivated Status = "deactivated"
)

// ErrInvalidStatus is returned for a status value outside the closed vocabulary.
// It wraps sdk.ErrInvalidInput so a transport maps it to 400 and a caller
// distinguishes a malformed request from a missing user (sdk.ErrNotFound) or a
// lost CAS (sdk.ErrConflict). Checked with errors.Is.
var ErrInvalidStatus = fmt.Errorf("user: unknown status: %w", sdk.ErrInvalidInput)

// Valid reports whether s is one of the closed v1 statuses. The empty value is
// NOT valid: it is a legacy-read artifact that NormalizeStatus resolves, never
// something a caller may pass in.
func (s Status) Valid() bool {
	switch s {
	case StatusActive, StatusDeactivated:
		return true
	default:
		return false
	}
}

// Active reports whether s permits authentication.
func (s Status) Active() bool { return s == StatusActive }

// ParseStatus converts a wire or persisted value to a Status, rejecting anything
// outside the closed vocabulary with ErrInvalidStatus. An EMPTY value is
// rejected too — a caller must be explicit; only a store READER may normalize a
// legacy empty column (see NormalizeStatus).
func ParseStatus(s string) (Status, error) {
	st := Status(s)
	if !st.Valid() {
		return "", fmt.Errorf("user: %q: %w", s, ErrInvalidStatus)
	}
	return st, nil
}

// NormalizeStatus resolves a value READ FROM STORAGE: an empty string becomes
// StatusActive, because a row written before the status column existed predates
// the concept and was, by definition, usable. Any other unknown value is returned
// unchanged so a store's own validation can reject it rather than silently
// treating garbage as active.
//
// This is a reader-side convenience only. The bundled migrations backfill and
// CHECK-constrain the column, so a persisted empty or arbitrary value does not
// survive in a store that applied them; the normalization exists for a host store
// that has not, and for the window between deploying a binary and applying the
// migration.
func NormalizeStatus(s Status) Status {
	if s == "" {
		return StatusActive
	}
	return s
}

// Summary is the persistence-free operator-directory projection of a user
// (CHAU-1.1). It is what an administrative console lists, and it deliberately
// carries NO credential material: no password hash, OAuth token, session, API-key
// material, challenge state, recovery inventory, or auth revision appears here.
//
// PrimaryEmail is the normalized value of the user's ACTIVE PRIMARY email
// identifier. It is NOT masked: this projection is reachable only behind an
// explicit host authorization decision, and a masked directory is useless for the
// operator task it exists for. It is empty for the provider-created edge case
// where a subject holds no email identifier at all — an empty value means "no
// address on file", never "hidden".
//
// EmailVerified reports whether that same identifier is proven. It is false when
// PrimaryEmail is empty.
type Summary struct {
	ID              string
	DisplayName     string
	Status          Status
	StatusChangedAt time.Time // zero → never transitioned
	PrimaryEmail    string
	EmailVerified   bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// OrderFields is the allow-list of sortable columns for AdminRepository.List:
// only these vetted column names may reach a store's ORDER BY. V1 offers the one
// indexed spine column every auth port pages by; the id tiebreak is applied by
// the store, not listed here. Fuzzy search and status filtering are deliberately
// absent from the first contract rather than shipped with unstable semantics.
var OrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
}

// DefaultOrder is the sort applied when a ListRequest carries a zero-value Order:
// created_at DESC (with the store's id DESC tiebreak), matching every other
// paged auth port so cursor collation is uniform across the feature.
var DefaultOrder = crud.NewOrder("created_at", crud.DESC)

// StatusChange is the outcome of an AdminRepository.SetStatus call.
//
// Changed is reported INDEPENDENTLY of Status so that replaying the same desired
// status is idempotent: a second deactivate returns {Status: deactivated,
// Changed: false} rather than a conflict or a not-found, and performs no
// revocation and no second auth_revision increment. A caller that needs to know
// whether it caused the transition reads Changed; a caller that needs the
// resulting state reads Status.
type StatusChange struct {
	// Status is the user's status after the operation — the DESIRED status on
	// success, whether or not this call caused it.
	Status Status
	// Changed reports whether this call performed the transition.
	Changed bool
	// ChangedAt is the transition time when Changed is true, and the previously
	// recorded transition time otherwise (zero if there has never been one).
	ChangedAt time.Time
	// RevokedSessions is how many session rows the transition deleted. It is 0
	// for a no-op replay and for a transition to StatusActive (reactivation
	// fabricates nothing). It is informational: a store that cannot count cheaply
	// may report 0, and callers must not treat it as an authorization signal.
	RevokedSessions int
}

// AdminRepository is the OPTIONAL user-administration capability: the paginated
// operator directory plus the atomic lifecycle transition. It is separate from
// UserRepository so the base port stays source-compatible and a host store can
// leave administration unimplemented.
//
// Presence of this port does NOT enable an HTTP surface. The bundled admin routes
// mount only when the host also supplies Config.UserAdminCheck, so a store
// adapter may return a complete bundle without any authorization surface
// appearing. See the Config.UserAdminCheck documentation.
//
// Sentinel contract (the storetest conformance suite executes these):
//   - GetSummary for an unknown id → sdk.ErrNotFound.
//   - SetStatus for an unknown id → sdk.ErrNotFound.
//   - SetStatus with a status outside the closed vocabulary → ErrInvalidStatus
//     (wrapping sdk.ErrInvalidInput), with NO write performed.
//   - SetStatus to the status the user already has → success with
//     Changed=false, never sdk.ErrNotFound and never sdk.ErrConflict.
//   - A concurrent credential/status CAS loss → sdk.ErrConflict.
//
// List is crud-typed (design §9) with the same pinned ordering as every other
// paged auth port: a zero-value ListRequest.Order means ORDER BY created_at DESC,
// id DESC. The id tiebreak is contractual, so pages stay stable when several
// users share a created_at, and cursors are byte-identical across dialects.
type AdminRepository interface {
	// List returns a cursor- or offset-paginated page of user summaries ordered
	// created_at DESC, id DESC. It resolves each row's active primary email in
	// the SAME query — an implementation must not issue one identifier read per
	// user. crud.ListRequest.WithCount is honored as it is for every other port.
	List(ctx context.Context, req crud.ListRequest) (crud.Page[Summary], error)

	// GetSummary returns one user's directory projection, or sdk.ErrNotFound.
	GetSummary(ctx context.Context, id string) (Summary, error)

	// SetStatus atomically transitions the user's lifecycle status.
	//
	// When the status actually changes it MUST, in ONE transaction:
	//
	//  1. write the new status and its transition time;
	//  2. increment users.auth_revision exactly once, invalidating any in-flight
	//     revision-CAS credential mutation; and
	//  3. delete every session row and every authentication grant for the user.
	//
	// All three commit together or none does. An update-then-best-effort-delete
	// implementation is NOT acceptable: a crash between the two would leave a
	// deactivated user holding live sessions.
	//
	// Replaying the current status is a no-op: no revision increment, no
	// revocation, and Changed=false. Transitioning TO StatusActive changes the
	// status and does not fabricate a session; whether it revokes anything is up
	// to the implementation, but it has nothing to revoke in practice.
	SetStatus(ctx context.Context, id string, status Status, now time.Time) (StatusChange, error)
}
