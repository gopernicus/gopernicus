// Package identifier is the v3 identity-discovery domain: the addresses by which
// a stable user subject can be found or contacted, separated from the user
// aggregate (design §2). One user may hold multiple identifiers of a kind, each
// carrying explicit login, recovery, and notification uses plus an at-most-one
// primary flag per (user, kind).
//
// The kind vocabulary is deliberately CLOSED to email and phone (design §2.1):
// Kind is a typed value, and ParseKind rejects any other string at a persistence
// boundary. A third kind is a global stop condition, not a silent open-string
// growth.
//
// Verification is an invariant, not decoration (design §2.3): login and recovery
// use require a proven address (VerifiedAt set), enforced by both the New
// constructor and the SetUses transition. The single exception is the initial
// registered email while its registration challenge is pending — created through
// NewRegistrationEmail, which is the only way to obtain an unverified
// login/recovery-enabled identifier.
//
// Normalization (normalize.go) produces the one canonical value used for
// persistence, lookup, invitations, rate-limit keys, and audit details. The
// bundled DefaultNormalizer is strict addr-spec-only email and strict naive
// E.164 phone; a host may inject a custom Normalizer (e.g. one that performs full
// IDNA ToASCII, which the stdlib-only feature core cannot).
package identifier

import (
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Kind is the closed v3 identifier-kind vocabulary (design §2.1). Its constants
// are pinned to the sdk/foundation/identity address-kind names so a Resolver's
// projected addresses and a stored identifier speak one kind string; no third
// kind exists in v3.
type Kind string

const (
	// KindEmail is an email-address identifier.
	KindEmail Kind = identity.KindEmail
	// KindPhone is a phone-number identifier.
	KindPhone Kind = identity.KindPhone
)

// Valid reports whether k is one of the closed v3 kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindEmail, KindPhone:
		return true
	default:
		return false
	}
}

// ParseKind converts a persisted/wire kind string to a Kind, rejecting any value
// outside the closed vocabulary with ErrUnknownKind (wrapping sdk.ErrInvalidInput)
// so an arbitrary string never crosses a persistence boundary (design §2.1).
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if !k.Valid() {
		return "", fmt.Errorf("identifier: %q: %w", s, ErrUnknownKind)
	}
	return k, nil
}

// Stable domain errors. ErrUnknownKind wraps sdk.ErrInvalidInput (a value outside
// the closed kind vocabulary); ErrVerificationRequired wraps sdk.ErrConflict (a
// state transition that would enable login/recovery on an unverified identifier),
// so a caller distinguishes a malformed request from a disallowed state change
// with errors.Is.
var (
	// ErrUnknownKind is returned for a kind outside {email, phone} (design §2.1).
	ErrUnknownKind = fmt.Errorf("identifier: unknown kind: %w", sdk.ErrInvalidInput)
	// ErrVerificationRequired is returned when login or recovery use is requested
	// for an identifier that is not verified (design §2.3). The initial
	// registration-email exception uses NewRegistrationEmail instead.
	ErrVerificationRequired = fmt.Errorf("identifier: login or recovery use requires a verified identifier: %w", sdk.ErrConflict)
)

// Uses records the roles an identifier serves (design §2.1, §2.3). Login and
// recovery are authentication-bearing uses that require verification; notification
// is a contact-only use with no verification requirement.
type Uses struct {
	Login        bool
	Recovery     bool
	Notification bool
}

// requiresVerification reports whether these uses include an authentication-
// bearing role (login or recovery).
func (u Uses) requiresVerification() bool {
	return u.Login || u.Recovery
}

// Identifier is an address by which a user subject can be found or contacted. It
// is history-preserving: a retired row sets ReplacedAt rather than being deleted,
// so recovery/security investigations can see what changed, while normal reads
// return active rows only (design §2.3). VerifiedAt records the proof TIME (not a
// boolean) needed by lifecycle and future risk policy; its zero value means
// unverified. ReplacedAt's zero value means active.
type Identifier struct {
	// ID is the app-minted primary key, empty under the greenfield DB-generated
	// convention (cryptids.Database) so the store assigns it inline (design §2.1).
	ID string
	// UserID is the owning subject. It may be empty at construction for the
	// initial registration identifier, whose owning user is inserted in the same
	// atomic CreateWithPrimaryIdentifier operation (design §2.2).
	UserID              string
	Kind                Kind
	NormalizedValue     string
	VerifiedAt          time.Time // zero → unverified
	LoginEnabled        bool
	RecoveryEnabled     bool
	NotificationEnabled bool
	IsPrimary           bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ReplacedAt          time.Time // zero → active
}

// New constructs an active identifier of kind bearing uses, normalizing value
// through norm. Login or recovery use requires verifiedAt to be non-zero;
// otherwise New returns ErrVerificationRequired (design §2.3). The initial
// registration-email exception is NewRegistrationEmail. An unknown kind or a
// value the normalizer rejects wraps sdk.ErrInvalidInput. verifiedAt's zero value
// creates a notification-only unverified identifier. The ID is minted from ids
// (empty under cryptids.Database — the store then assigns it).
func New(ids cryptids.IDGenerator, norm Normalizer, userID string, kind Kind, value string, uses Uses, primary bool, verifiedAt, now time.Time) (Identifier, error) {
	if !kind.Valid() {
		return Identifier{}, fmt.Errorf("identifier: %q: %w", kind, ErrUnknownKind)
	}
	normalized, err := norm.Normalize(string(kind), value)
	if err != nil {
		return Identifier{}, err
	}
	if uses.requiresVerification() && verifiedAt.IsZero() {
		return Identifier{}, ErrVerificationRequired
	}
	now = now.UTC()
	id := Identifier{
		ID:                  ids.MustGenerate(),
		UserID:              userID,
		Kind:                kind,
		NormalizedValue:     normalized,
		LoginEnabled:        uses.Login,
		RecoveryEnabled:     uses.Recovery,
		NotificationEnabled: uses.Notification,
		IsPrimary:           primary,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if !verifiedAt.IsZero() {
		id.VerifiedAt = verifiedAt.UTC()
	}
	return id, nil
}

// NewRegistrationEmail constructs the initial primary email created by
// registration (design §2.1): login-, recovery-, and notification-enabled, marked
// primary, and deliberately UNVERIFIED while its registration challenge is pending
// — the single exception to the verification invariant (design §2.3). Login
// refuses this identifier until Verify records the proof time. userID may be empty
// when the owning user is inserted in the same atomic operation.
func NewRegistrationEmail(ids cryptids.IDGenerator, norm Normalizer, userID, value string, now time.Time) (Identifier, error) {
	normalized, err := norm.Normalize(string(KindEmail), value)
	if err != nil {
		return Identifier{}, err
	}
	now = now.UTC()
	return Identifier{
		ID:                  ids.MustGenerate(),
		UserID:              userID,
		Kind:                KindEmail,
		NormalizedValue:     normalized,
		LoginEnabled:        true,
		RecoveryEnabled:     true,
		NotificationEnabled: true,
		IsPrimary:           true,
		CreatedAt:           now,
		UpdatedAt:           now,
		// VerifiedAt intentionally zero: the registration challenge is pending.
	}, nil
}

// Verified reports whether the address has been proven (VerifiedAt is set).
func (i Identifier) Verified() bool { return !i.VerifiedAt.IsZero() }

// Active reports whether the identifier is live (not retired/replaced).
func (i Identifier) Active() bool { return i.ReplacedAt.IsZero() }

// CurrentUses returns the identifier's current role flags.
func (i Identifier) CurrentUses() Uses {
	return Uses{Login: i.LoginEnabled, Recovery: i.RecoveryEnabled, Notification: i.NotificationEnabled}
}

// Verify records the proof time, satisfying the verification invariant so login
// and recovery use may be enabled thereafter.
func (i *Identifier) Verify(now time.Time) {
	i.VerifiedAt = now.UTC()
	i.UpdatedAt = i.VerifiedAt
}

// Retire marks the identifier replaced (history-preserving; not a hard delete),
// removing it from active reads (design §2.3).
func (i *Identifier) Retire(now time.Time) {
	i.ReplacedAt = now.UTC()
	i.UpdatedAt = i.ReplacedAt
}

// SetUses updates the role flags, rejecting an authentication-bearing use
// (login/recovery) on an unverified identifier with ErrVerificationRequired
// (design §2.3).
func (i *Identifier) SetUses(uses Uses, now time.Time) error {
	if uses.requiresVerification() && !i.Verified() {
		return ErrVerificationRequired
	}
	i.LoginEnabled = uses.Login
	i.RecoveryEnabled = uses.Recovery
	i.NotificationEnabled = uses.Notification
	i.UpdatedAt = now.UTC()
	return nil
}

// MakePrimary marks the identifier primary. At-most-one active primary per
// (user, kind) is a store-arbitrated invariant (the partial unique index, design
// §2.1); this transition only records the intent.
func (i *Identifier) MakePrimary(now time.Time) {
	i.IsPrimary = true
	i.UpdatedAt = now.UTC()
}
