// Package invitation is the resource-invitation domain (design §6): an invite
// to grant a subject a relation on a resource, delivered by a single-use secret
// mailed to the invitee. It is deliberately DECOUPLED from ReBAC (ratified AV4):
// the grant on acceptance rides a host-supplied Granter seam, and invitation
// VISIBILITY rides this entity's own table columns (Identifier, InvitedBy,
// ResolvedSubjectID) — never authorization tuples. A host with no ReBAC has no
// "invitation" resource type, and this domain never pretends otherwise.
//
// Relation is an OPAQUE string the Granter interprets — a ReBAC host maps it to
// a relation, a role-column host to a role. The plaintext token is held only by
// the service and mailed to the invitee; this entity carries just its SHA-256
// hash (TokenHash).
package invitation

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Status values for an invitation's lifecycle. An invitation is created
// StatusPending; acceptance/resolution moves it to StatusAccepted, and the
// invitee/owner may move it to StatusDeclined/StatusCancelled. StatusExpired is
// surfaced on a token-hash read past ExpiresAt (a read-time state; no writer is
// required to persist it).
const (
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusDeclined  = "declined"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

// Invitation is one invite record. Identifier is the invitee email (stored
// normalized by the service). ResolvedSubjectID is the subject the invite
// resolved to on acceptance (empty while pending for an unknown invitee).
// InvitedBy is the user that created it — the ONLY authorization anchor for
// cancel/resend (a plain ownership column, never a tuple). AutoAccept marks an
// invite that grants automatically when its invitee registers/verifies a
// matching email (resolve-on-registration). AcceptedAt is zero until accepted.
type Invitation struct {
	ID                string
	ResourceType      string
	ResourceID        string
	Relation          string
	Identifier        string // invitee email (normalized)
	ResolvedSubjectID string // set on acceptance; empty while pending-unresolved
	InvitedBy         string
	TokenHash         string
	AutoAccept        bool
	Status            string
	ExpiresAt         time.Time
	AcceptedAt        time.Time // zero → not accepted
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// New builds a StatusPending invitation from an already-minted tokenHash (the
// service mints and hashes the secret; only it ever holds the plaintext),
// minting its record ID from ids (empty under cryptids.Database — the store
// then assigns the key). ttl sets ExpiresAt from now. A blank resourceType/
// resourceID/relation/identifier/invitedBy/tokenHash wraps errs.ErrInvalidInput.
// The identifier is stored verbatim — the service normalizes it (email) before
// calling New so it matches the value resolve-on-registration and "mine" look
// it up by.
func New(ids cryptids.IDGenerator, resourceType, resourceID, relation, identifier, invitedBy, tokenHash string, autoAccept bool, ttl time.Duration, now time.Time) (Invitation, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	relation = strings.TrimSpace(relation)
	identifier = strings.TrimSpace(identifier)
	invitedBy = strings.TrimSpace(invitedBy)
	tokenHash = strings.TrimSpace(tokenHash)
	switch {
	case resourceType == "":
		return Invitation{}, fmt.Errorf("resource type is required: %w", errs.ErrInvalidInput)
	case resourceID == "":
		return Invitation{}, fmt.Errorf("resource id is required: %w", errs.ErrInvalidInput)
	case relation == "":
		return Invitation{}, fmt.Errorf("relation is required: %w", errs.ErrInvalidInput)
	case identifier == "":
		return Invitation{}, fmt.Errorf("identifier is required: %w", errs.ErrInvalidInput)
	case invitedBy == "":
		return Invitation{}, fmt.Errorf("invited-by is required: %w", errs.ErrInvalidInput)
	case tokenHash == "":
		return Invitation{}, fmt.Errorf("token hash is required: %w", errs.ErrInvalidInput)
	}
	now = now.UTC()
	return Invitation{
		ID:           ids.MustGenerate(),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     relation,
		Identifier:   identifier,
		InvitedBy:    invitedBy,
		TokenHash:    tokenHash,
		AutoAccept:   autoAccept,
		Status:       StatusPending,
		ExpiresAt:    now.Add(ttl),
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Expired reports whether the invitation is at or past its expiry at now.
func (i Invitation) Expired(now time.Time) bool {
	return !now.Before(i.ExpiresAt)
}
