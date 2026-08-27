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
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// Metadata bounds. Metadata is opaque, host-owned routing data an invitation
// carries from create to the Granter seam — small routing facts (a firm id, a
// plan tier), never a document store, so it is bounded. Limits are measured in
// UTF-8 bytes; empty keys are rejected, values may be empty, invalid UTF-8 is
// rejected, and MetadataMaxTotalBytes bounds the JSON-encoded whole. Every
// violation wraps sdk.ErrInvalidInput.
const (
	MetadataMaxEntries    = 32
	MetadataMaxKeyBytes   = 64
	MetadataMaxValueBytes = 256
	MetadataMaxTotalBytes = 4 << 10 // 4 KiB, JSON-encoded
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

// Invitation is one invite record. Identifier is the invitee address (stored
// normalized by the service — email lowercased, every other kind trimmed only).
// IdentifierKind is the address kind the identifier is (identity.KindEmail,
// identity.KindPhone, or any open string a wired notifier declares); it is part
// of the pending-tuple uniqueness key so the same value can be invited across
// kinds. ResolvedSubjectID is the subject the invite resolved to on acceptance
// (empty while pending for an unknown invitee). InvitedBy is the user that
// created it — the ONLY authorization anchor for cancel/resend (a plain ownership
// column, never a tuple). AutoAccept marks an invite that grants automatically
// when its invitee registers/verifies a matching email (resolve-on-registration;
// email-kind only). AcceptedAt is zero until accepted.
type Invitation struct {
	ID                string
	ResourceType      string
	ResourceID        string
	Relation          string
	Identifier        string // invitee address (normalized by the service)
	IdentifierKind    string // identity.KindEmail (default), identity.KindPhone, …
	ResolvedSubjectID string // set on acceptance; empty while pending-unresolved
	InvitedBy         string
	TokenHash         string
	AutoAccept        bool
	Status            string
	// Metadata is opaque, host-owned routing data supplied at create, persisted,
	// and echoed into the Granter seam on every grant path. The pocket never
	// interprets it (validating only shape/size); a nil or empty map is the
	// no-metadata case. See NewWithMetadata / ValidateMetadata for the bounds.
	Metadata   map[string]string
	ExpiresAt  time.Time
	AcceptedAt time.Time // zero → not accepted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// New builds a StatusPending invitation from an already-minted tokenHash (the
// service mints and hashes the secret; only it ever holds the plaintext),
// minting its record ID from ids (empty under cryptids.Database — the store
// then assigns the key). ttl sets ExpiresAt from now. A blank resourceType/
// resourceID/relation/identifier/invitedBy/tokenHash wraps sdk.ErrInvalidInput.
// A blank identifierKind defaults to identity.KindEmail. The identifier is
// stored verbatim — the service normalizes it (kind-aware) before calling New so
// it matches the value resolve-on-registration and "mine" look it up by.
func New(ids cryptids.IDGenerator, resourceType, resourceID, relation, identifier, identifierKind, invitedBy, tokenHash string, autoAccept bool, ttl time.Duration, now time.Time) (Invitation, error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	relation = strings.TrimSpace(relation)
	identifier = strings.TrimSpace(identifier)
	identifierKind = strings.TrimSpace(identifierKind)
	invitedBy = strings.TrimSpace(invitedBy)
	tokenHash = strings.TrimSpace(tokenHash)
	if identifierKind == "" {
		identifierKind = identity.KindEmail
	}
	switch {
	case resourceType == "":
		return Invitation{}, fmt.Errorf("resource type is required: %w", sdk.ErrInvalidInput)
	case resourceID == "":
		return Invitation{}, fmt.Errorf("resource id is required: %w", sdk.ErrInvalidInput)
	case relation == "":
		return Invitation{}, fmt.Errorf("relation is required: %w", sdk.ErrInvalidInput)
	case identifier == "":
		return Invitation{}, fmt.Errorf("identifier is required: %w", sdk.ErrInvalidInput)
	case invitedBy == "":
		return Invitation{}, fmt.Errorf("invited-by is required: %w", sdk.ErrInvalidInput)
	case tokenHash == "":
		return Invitation{}, fmt.Errorf("token hash is required: %w", sdk.ErrInvalidInput)
	}
	now = now.UTC()
	return Invitation{
		ID:             ids.MustGenerate(),
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		Relation:       relation,
		Identifier:     identifier,
		IdentifierKind: identifierKind,
		InvitedBy:      invitedBy,
		TokenHash:      tokenHash,
		AutoAccept:     autoAccept,
		Status:         StatusPending,
		ExpiresAt:      now.Add(ttl),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// NewWithMetadata builds a StatusPending invitation exactly as New does, then
// validates and attaches opaque host metadata. The metadata is bounded (see
// ValidateMetadata) and stored as a defensive copy — nil/empty yields a
// nil-metadata invitation. A bounds violation wraps sdk.ErrInvalidInput and no
// record is minted. Prefer this over threading a new positional argument through
// New so existing callers stay unchanged.
func NewWithMetadata(ids cryptids.IDGenerator, resourceType, resourceID, relation, identifier, identifierKind, invitedBy, tokenHash string, autoAccept bool, ttl time.Duration, now time.Time, metadata map[string]string) (Invitation, error) {
	md, err := ValidateMetadata(metadata)
	if err != nil {
		return Invitation{}, err
	}
	inv, err := New(ids, resourceType, resourceID, relation, identifier, identifierKind, invitedBy, tokenHash, autoAccept, ttl, now)
	if err != nil {
		return Invitation{}, err
	}
	inv.Metadata = md
	return inv, nil
}

// ValidateMetadata checks host-supplied invitation metadata against the bounded
// routing-data limits (MetadataMax*) and returns a DEFENSIVE COPY the caller owns
// — a nil or empty input yields a nil map (the no-metadata case). Both
// NewWithMetadata and the service's direct-add path validate through here so the
// two share one rule set. Every violation wraps sdk.ErrInvalidInput.
func ValidateMetadata(metadata map[string]string) (map[string]string, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	if len(metadata) > MetadataMaxEntries {
		return nil, fmt.Errorf("metadata has %d entries, max %d: %w", len(metadata), MetadataMaxEntries, sdk.ErrInvalidInput)
	}
	out := make(map[string]string, len(metadata))
	for k, v := range metadata {
		switch {
		case k == "":
			return nil, fmt.Errorf("metadata key is empty: %w", sdk.ErrInvalidInput)
		case !utf8.ValidString(k):
			return nil, fmt.Errorf("metadata key is not valid UTF-8: %w", sdk.ErrInvalidInput)
		case !utf8.ValidString(v):
			return nil, fmt.Errorf("metadata value for key %q is not valid UTF-8: %w", k, sdk.ErrInvalidInput)
		case len(k) > MetadataMaxKeyBytes:
			return nil, fmt.Errorf("metadata key %q exceeds %d bytes: %w", k, MetadataMaxKeyBytes, sdk.ErrInvalidInput)
		case len(v) > MetadataMaxValueBytes:
			return nil, fmt.Errorf("metadata value for key %q exceeds %d bytes: %w", k, MetadataMaxValueBytes, sdk.ErrInvalidInput)
		}
		out[k] = v
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode metadata: %w", sdk.ErrInvalidInput)
	}
	if len(encoded) > MetadataMaxTotalBytes {
		return nil, fmt.Errorf("metadata encodes to %d bytes, max %d: %w", len(encoded), MetadataMaxTotalBytes, sdk.ErrInvalidInput)
	}
	return out, nil
}

// CloneMetadata returns an always-non-nil defensive copy of md, so a caller or
// Granter cannot mutate persisted or subsequently delivered state. A nil or empty
// md yields a non-nil empty map — the delivered-as-empty-map contract every grant
// path honors.
func CloneMetadata(md map[string]string) map[string]string {
	out := make(map[string]string, len(md))
	maps.Copy(out, md)
	return out
}

// Expired reports whether the invitation is at or past its expiry at now.
func (i Invitation) Expired(now time.Time) bool {
	return !now.Before(i.ExpiresAt)
}
