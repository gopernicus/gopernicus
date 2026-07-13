// Package contactchange is the flow-state domain that holds the PENDING new
// address of an email/phone add-or-change between its start and its confirm
// (design §2.4). It is deliberately its own small domain — à la oauthstate — for
// two standing reasons: the pending value is NOT allowed to ride
// challenge.context (the freeze-transfer clause pins context to a binding
// validator, never a payload channel), and it is NOT allowed to accrete pending_*
// columns onto users (the GoTrue-style column sprawl §2 refuses).
//
// A change flow is one challenge (purpose change_email/change_phone, the secret
// delivered to the NEW address, with full lockout semantics) plus one PendingChange
// row (this domain, carrying the pending value and requested uses). A contactchange
// row carries NO secret and NO lockout state — those ride the challenge domain — so
// challenge.context need only bind this row's ID as a pure validator at confirm.
//
// Pair ordering is pinned (design §2.4): START creates the PendingChange first
// (obtaining its ID) then issues the challenge bound to that ID; CONFIRM consumes
// the CHALLENGE first, then Consume here, then ApplyVerifiedChange — consuming the
// pending row before the challenge would destroy the pending value on a wrong-code
// retry that deliberately keeps the challenge alive.
package contactchange

import (
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
)

// PendingChange is one in-flight email/phone add-or-change (design §2.4). It holds
// the already-normalized new value and the uses/primary/replacement intent the
// confirm step applies through identifier.ApplyVerifiedChange; its field names line
// up one-to-one with identifier.ApplyVerifiedChangeInput so authsvc translates a
// consumed PendingChange into that input with a plain field copy (NewValue maps to
// NormalizedValue). It carries no secret: the code/token and its lockout ride the
// challenge domain.
//
// One pending change is active per (UserID, Kind): the store enforces this as
// delete-before-create plus a DB unique index (the challenge rule). ExpiresAt bounds
// the flow; an expired row is deleted on Consume.
type PendingChange struct {
	// ID is the app-minted primary key, empty under the greenfield DB-generated
	// convention so the store assigns it inline. challenge.context binds this ID as
	// the confirm-step validator.
	ID     string
	UserID string
	Kind   identifier.Kind
	// NewValue is the already-normalized target address (the service normalizes
	// through the injected identifier.Normalizer before Create). It becomes the new
	// identifier's NormalizedValue at confirm.
	NewValue            string
	LoginEnabled        bool
	RecoveryEnabled     bool
	NotificationEnabled bool
	MakePrimary         bool
	// ReplacesIdentifierID names the active identifier retired in the same atomic
	// apply (a change/replacement); empty for a pure add.
	ReplacesIdentifierID string
	ExpiresAt            time.Time
	CreatedAt            time.Time
}

// New builds a PendingChange for (userID, kind) carrying the already-normalized
// newValue, the requested uses, primary/replacement intent, and an expiry of ttl
// after now. The ID is left empty for the store to assign inline (greenfield
// DB-generated convention). Normalization is the caller's responsibility (design
// §2.2: one normalization result for persistence, lookup, and audit).
func New(userID string, kind identifier.Kind, newValue string, uses identifier.Uses, makePrimary bool, replacesIdentifierID string, ttl time.Duration, now time.Time) PendingChange {
	now = now.UTC()
	return PendingChange{
		UserID:               userID,
		Kind:                 kind,
		NewValue:             newValue,
		LoginEnabled:         uses.Login,
		RecoveryEnabled:      uses.Recovery,
		NotificationEnabled:  uses.Notification,
		MakePrimary:          makePrimary,
		ReplacesIdentifierID: replacesIdentifierID,
		ExpiresAt:            now.Add(ttl),
		CreatedAt:            now,
	}
}

// Uses returns the requested role flags as an identifier.Uses value.
func (p PendingChange) Uses() identifier.Uses {
	return identifier.Uses{Login: p.LoginEnabled, Recovery: p.RecoveryEnabled, Notification: p.NotificationEnabled}
}

// Expired reports whether the pending change is at or past its expiry at now.
func (p PendingChange) Expired(now time.Time) bool {
	return !now.Before(p.ExpiresAt)
}
