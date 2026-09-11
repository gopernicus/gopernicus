package firestore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The document shapes. Field names and semantics are pinned in SCHEMA.md; they
// mirror the SQL column names so the two documentation trees stay shared, plus
// the derived keys Firestore needs where SQL has a multi-column index and the
// two directory-projection fields UserAdmin reads (SCHEMA.md §6).
//
// Three rules hold for every shape here.
//
// Every field is always WRITTEN, never omitted: Firestore's OrderBy drops
// documents that lack the ordered field and an absent field is an absent index
// entry, so "empty" is the empty string, false, or an EXPLICIT null (the
// connector's NullTime helpers) — never an absent field.
//
// A nullable SQL column is typed `any` here and written through
// firestoredb.NullTime / read through firestoredb.ParseNullTime. A `string`
// field cannot hold Firestore's null, and collapsing SQL's NULL to "" is not
// always safe: sessions.previous_refresh_token_hash is the case that proves it
// — every fresh session would share "" and a lookup for an empty hash could
// match an arbitrary one (the cross-session bleed migration 0003 calls out).
//
// Timestamps are written through firestoredb.TruncateTime (microseconds, UTC)
// so a read-back compares equal to what the port handed in.

// userDoc is one users document — the stable human subject (migrations 0001,
// 0014) plus the DIRECTORY PROJECTION (N-D3).
type userDoc struct {
	ID              string    `firestore:"id"`
	DisplayName     string    `firestore:"display_name"`
	AuthRevision    int64     `firestore:"auth_revision"`
	Status          string    `firestore:"status"`
	StatusChangedAt any       `firestore:"status_changed_at"`
	CreatedAt       time.Time `firestore:"created_at"`
	UpdatedAt       time.Time `firestore:"updated_at"`

	// PrimaryEmail and EmailVerified are the directory projection: the
	// normalized value of the user's ACTIVE PRIMARY email identifier and
	// whether it is proven. The SQL adapters resolve them with a LEFT JOIN in
	// the same statement as the page; Firestore has no join, and one identifier
	// read per listed user would make the operator directory O(page) round
	// trips. user_identifiers stays AUTHORITATIVE — this is a projection, and
	// every writer that can change it maintains it in the SAME transaction as
	// the identifier row and its claims (SCHEMA.md §6, the writer table).
	PrimaryEmail  string `firestore:"primary_email"`
	EmailVerified bool   `firestore:"email_verified"`
}

// passwordDoc is one user_passwords document (migration 0002). Its id is the
// user id, so Set IS the port's upsert.
type passwordDoc struct {
	UserID string `firestore:"user_id"`
	Hash   string `firestore:"hash"`
}

// identifierDoc is one user_identifiers document (migration 0010): an address
// the subject is found or contacted by. Retirement is history-preserving, so a
// replaced row stays and Active is what every "current" read filters on.
type identifierDoc struct {
	ID                  string    `firestore:"id"`
	UserID              string    `firestore:"user_id"`
	Kind                string    `firestore:"kind"`
	NormalizedValue     string    `firestore:"normalized_value"`
	VerifiedAt          any       `firestore:"verified_at"`
	LoginEnabled        bool      `firestore:"login_enabled"`
	RecoveryEnabled     bool      `firestore:"recovery_enabled"`
	NotificationEnabled bool      `firestore:"notification_enabled"`
	IsPrimary           bool      `firestore:"is_primary"`
	CreatedAt           time.Time `firestore:"created_at"`
	UpdatedAt           time.Time `firestore:"updated_at"`
	ReplacedAt          any       `firestore:"replaced_at"`

	// Active is the indexable spelling of `replaced_at IS NULL`, the leading
	// conjunct of BOTH partial unique indexes and of every active-only read.
	// It is derived from ReplacedAt and written with it, never separately.
	Active bool `firestore:"active"`
}

// sessionDoc is one sessions document (migration 0003): the revocable anchor
// carrying refresh-rotation state.
type sessionDoc struct {
	ID               string `firestore:"id"`
	UserID           string `firestore:"user_id"`
	RefreshTokenHash string `firestore:"refresh_token_hash"`

	// PreviousRefreshTokenHash is the single rotated-away (grace) slot, an
	// EXPLICIT null while the session has never rotated — see the file comment.
	PreviousRefreshTokenHash any `firestore:"previous_refresh_token_hash"`

	PreviousUsed  bool `firestore:"previous_used"`
	RotationCount int  `firestore:"rotation_count"`

	// AuthenticatedAt is nullable (null ↔ the zero "not recorded" sentinel).
	// AuthenticationMethods keeps the SQL adapters' JSON encoding rather than a
	// native array: the descriptors are a domain type this store must not tag,
	// and a JSON string round-trips them byte-identically across all three
	// families.
	AuthenticatedAt       any    `firestore:"authenticated_at"`
	AuthenticationMethods string `firestore:"authentication_methods"`
	AssuranceLevel        string `firestore:"assurance_level"`

	CreatedAt time.Time `firestore:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at"`
}

// oauthAccountDoc is one oauth_accounts document (migration 0004). Its id IS the
// (provider, provider_user_id) primary key.
type oauthAccountDoc struct {
	Provider              string    `firestore:"provider"`
	ProviderUserID        string    `firestore:"provider_user_id"`
	UserID                string    `firestore:"user_id"`
	ProviderEmail         string    `firestore:"provider_email"`
	ProviderEmailVerified bool      `firestore:"provider_email_verified"`
	AccountVerified       bool      `firestore:"account_verified"`
	LinkedAt              time.Time `firestore:"linked_at"`
	AccessToken           string    `firestore:"access_token"`
	RefreshToken          string    `firestore:"refresh_token"`
	TokenExpiresAt        any       `firestore:"token_expires_at"`
	TokenType             string    `firestore:"token_type"`
	Scope                 string    `firestore:"scope"`
}

// oauthStateDoc is one oauth_states document (migration 0005): a one-time,
// expiring flow secret keyed by its token.
type oauthStateDoc struct {
	Token     string    `firestore:"token"`
	Provider  string    `firestore:"provider"`
	Purpose   string    `firestore:"purpose"`
	Payload   string    `firestore:"payload"`
	ExpiresAt time.Time `firestore:"expires_at"`
}

// serviceAccountDoc is one service_accounts document (migration 0006). name is
// deliberately NOT unique — the audit found no unique index on it, and adding a
// claim would make this store stricter than its SQL siblings.
type serviceAccountDoc struct {
	ID          string    `firestore:"id"`
	Name        string    `firestore:"name"`
	Description string    `firestore:"description"`
	CreatedBy   string    `firestore:"created_by"`
	ActAsUser   bool      `firestore:"act_as_user"`
	OwnerUserID string    `firestore:"owner_user_id"`
	CreatedAt   time.Time `firestore:"created_at"`
	UpdatedAt   time.Time `firestore:"updated_at"`
}

// apiKeyDoc is one api_keys document (migration 0007). Revocation and expiry are
// SERVICE branches, never store filters, so GetByHash returns a revoked or
// expired record verbatim.
type apiKeyDoc struct {
	ID               string    `firestore:"id"`
	ServiceAccountID string    `firestore:"service_account_id"`
	Name             string    `firestore:"name"`
	KeyPrefix        string    `firestore:"key_prefix"`
	KeyHash          string    `firestore:"key_hash"`
	ExpiresAt        any       `firestore:"expires_at"`
	RevokedAt        any       `firestore:"revoked_at"`
	LastUsedAt       any       `firestore:"last_used_at"`
	CreatedAt        time.Time `firestore:"created_at"`
}

// securityEventDoc is one security_events document (migration 0008): the
// append-only audit rail. Details is a NATIVE map where SQL stores JSON text —
// the round-trip contract is uniform (nil or empty in, non-nil empty out), and
// the storage shape is each family's choice.
type securityEventDoc struct {
	ID          string            `firestore:"id"`
	UserID      string            `firestore:"user_id"`
	ActorType   string            `firestore:"actor_type"`
	ActorID     string            `firestore:"actor_id"`
	EventType   string            `firestore:"event_type"`
	EventStatus string            `firestore:"event_status"`
	Details     map[string]string `firestore:"details"`
	IPAddress   string            `firestore:"ip_address"`
	UserAgent   string            `firestore:"user_agent"`
	CreatedAt   time.Time         `firestore:"created_at"`
}

// invitationDoc is one invitations document (migrations 0009, 0016).
type invitationDoc struct {
	ID                  string            `firestore:"id"`
	ResourceType        string            `firestore:"resource_type"`
	ResourceID          string            `firestore:"resource_id"`
	Relation            string            `firestore:"relation"`
	Identifier          string            `firestore:"identifier"`
	IdentifierKind      string            `firestore:"identifier_kind"`
	ResolvedSubjectType string            `firestore:"resolved_subject_type"`
	ResolvedSubjectID   string            `firestore:"resolved_subject_id"`
	InvitedBy           string            `firestore:"invited_by"`
	TokenHash           string            `firestore:"token_hash"`
	AutoAccept          bool              `firestore:"auto_accept"`
	Status              string            `firestore:"status"`
	ExpiresAt           time.Time         `firestore:"expires_at"`
	AcceptedAt          any               `firestore:"accepted_at"`
	CreatedAt           time.Time         `firestore:"created_at"`
	UpdatedAt           time.Time         `firestore:"updated_at"`
	Metadata            map[string]string `firestore:"metadata"`

	// ResourceKey and SubjectKey are the derived equality keys of the two paged
	// listings (SCHEMA.md §4.2): identities, never projections, never sort keys.
	ResourceKey string `firestore:"resource_key"`
	SubjectKey  string `firestore:"subject_key"`
}

// challengeDoc is one challenges document (migrations 0011, 0015): the atomic
// secret rail. The plaintext secret is NEVER persisted — only secret_digest.
type challengeDoc struct {
	ID             string `firestore:"id"`
	SubjectKey     string `firestore:"subject_key"`
	UserID         string `firestore:"user_id"`
	Purpose        string `firestore:"purpose"`
	SecretDigest   string `firestore:"secret_digest"`
	ProtectorKeyID string `firestore:"protector_key_id"`

	// Context is the opaque binding blob (a pure validator, never a payload
	// channel). It is nullable in SQL and the domain distinguishes a nil blob
	// from an empty one, so it is `any`: an explicit null, or the blob's bytes.
	Context any `firestore:"context"`

	AttemptCount int       `firestore:"attempt_count"`
	ExpiresAt    time.Time `firestore:"expires_at"`
	CreatedAt    time.Time `firestore:"created_at"`
	Version      int       `firestore:"version"`
}

// contactChangeDoc is one contact_changes document (migration 0012): the pending
// new address of a change flow. It carries NO secret — the code/token and its
// lockout ride a challenge.
type contactChangeDoc struct {
	ID                   string    `firestore:"id"`
	UserID               string    `firestore:"user_id"`
	Kind                 string    `firestore:"kind"`
	NewValue             string    `firestore:"new_value"`
	LoginEnabled         bool      `firestore:"login_enabled"`
	RecoveryEnabled      bool      `firestore:"recovery_enabled"`
	NotificationEnabled  bool      `firestore:"notification_enabled"`
	MakePrimary          bool      `firestore:"make_primary"`
	ReplacesIdentifierID string    `firestore:"replaces_identifier_id"`
	ExpiresAt            time.Time `firestore:"expires_at"`
	CreatedAt            time.Time `firestore:"created_at"`
}

// authGrantDoc is one authentication_grants document (migration 0013): the
// short-lived, single-use, session-bound proof a sensitive mutation requires.
type authGrantDoc struct {
	ID            string `firestore:"id"`
	SessionID     string `firestore:"session_id"`
	UserID        string `firestore:"user_id"`
	Purpose       string `firestore:"purpose"`
	ContextDigest string `firestore:"context_digest"`

	// Methods keeps the SQL adapters' JSON encoding, for the reason sessionDoc
	// states.
	Methods   string `firestore:"methods"`
	Assurance string `firestore:"assurance"`

	AuthenticatedAt time.Time `firestore:"authenticated_at"`
	ExpiresAt       time.Time `firestore:"expires_at"`
	CreatedAt       time.Time `firestore:"created_at"`
	ConsumedAt      any       `firestore:"consumed_at"`

	// ConsumeKey collapses the (session_id, purpose, context_digest) selection
	// into one equality clause (SCHEMA.md §4.2).
	ConsumeKey string `firestore:"consume_key"`
}

// The CLAIM documents (SCHEMA.md §5). Each one's document id IS the unique key
// of the SQL index it reproduces, so the id carries the constraint and the
// FIELDS carry only what a reader needs to reach the owning row: DocID is the
// owning document's id (so a drop path never has to re-derive it) and the
// remaining fields are the row's domain identity.

// identifierClaimDoc reproduces idx_user_identifiers_auth_claim — the
// authentication claim on (kind, normalized_value). It also IS the access path
// for GetLogin/GetRecovery: reading it by id resolves the claim without an
// equality filter on unbounded address text.
type identifierClaimDoc struct {
	DocID        string `firestore:"doc_id"`
	IdentifierID string `firestore:"identifier_id"`
	UserID       string `firestore:"user_id"`
}

// identifierPrimaryDoc reproduces idx_user_identifiers_primary — at most one
// ACTIVE primary identifier per (user, kind).
type identifierPrimaryDoc struct {
	DocID        string `firestore:"doc_id"`
	IdentifierID string `firestore:"identifier_id"`
}

// refreshHashClaimDoc reproduces idx_sessions_refresh_token_hash — the CURRENT
// refresh credential is unique. The grace slot takes no claim (keys.go).
type refreshHashClaimDoc struct {
	DocID     string `firestore:"doc_id"`
	SessionID string `firestore:"session_id"`
}

// apiKeyHashClaimDoc reproduces idx_api_keys_key_hash and is GetByHash's access
// path.
type apiKeyHashClaimDoc struct {
	DocID    string `firestore:"doc_id"`
	APIKeyID string `firestore:"api_key_id"`
}

// invitationTokenClaimDoc reproduces idx_invitations_token_hash and is
// GetByTokenHash's access path.
type invitationTokenClaimDoc struct {
	DocID        string `firestore:"doc_id"`
	InvitationID string `firestore:"invitation_id"`
}

// invitationPendingClaimDoc reproduces idx_invitations_pending_tuple, the
// PARTIAL unique index over pending rows only. It exists exactly while the
// invitation's STORED status is pending.
type invitationPendingClaimDoc struct {
	DocID        string `firestore:"doc_id"`
	InvitationID string `firestore:"invitation_id"`
}

// challengeDigestClaimDoc reproduces idx_challenges_purpose_secret_digest and is
// ConsumeToken's access path: the claim resolves (purpose, digest) to the
// owning challenge document without a second index on the digest.
type challengeDigestClaimDoc struct {
	DocID       string `firestore:"doc_id"`
	ChallengeID string `firestore:"challenge_id"`
}

// The shared field encodings. authentication_methods on a session and methods
// on a grant are the SAME domain value — []session.AuthenticationMethod — and
// both SQL adapters persist it as JSON text. This store keeps that encoding
// rather than a native Firestore array: the descriptors are a domain type this
// store must not tag with firestore struct tags, and a JSON string round-trips
// them byte-identically across all three families.

// encodeMethods marshals the honest method descriptors; an empty set encodes as
// the empty string, which is the SQL column's DEFAULT.
func encodeMethods(methods []session.AuthenticationMethod) (string, error) {
	if len(methods) == 0 {
		return "", nil
	}
	b, err := json.Marshal(methods)
	if err != nil {
		return "", fmt.Errorf("authentication firestore store: encoding authentication methods: %s: %w", err, sdk.ErrInvalidInput)
	}
	return string(b), nil
}

// decodeMethods reverses encodeMethods; the empty string reads back as nil.
func decodeMethods(encoded string) ([]session.AuthenticationMethod, error) {
	if encoded == "" {
		return nil, nil
	}
	var out []session.AuthenticationMethod
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return nil, fmt.Errorf("authentication firestore store: decoding authentication methods: %s: %w", err, sdk.ErrInvalidInput)
	}
	return out, nil
}
