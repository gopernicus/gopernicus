// Package vault stores owner-scoped secrets encrypted at rest, with every
// read, write, delete and key operation recorded in an append-only audit log.
//
// Encryption is two layers. Each owner (a tenant, a site, whatever the host
// scopes by) has its own data key; secrets are sealed under it with AES-256-GCM
// and the secret's reference as additional authenticated data. The data key is
// stored wrapped by a Wrapper, bundled as a cryptids key ring held in the host's
// environment, so rotating the master key rewraps one row per owner rather than
// every secret, and destroying an owner's key makes its secrets unreadable in any
// backup that holds the secrets rows but not the owner-key row. A backup that also
// holds secrets_owner_keys stays readable until every ring key the owner key was
// wrapped under has been retired from the host's ring (README, Rotation).
//
// Plaintext never reaches a log line, an error or an audit row.
package vault

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// MaxValueBytes is the largest secret value Put accepts: room for a PEM
// private key, not a file store.
const MaxValueBytes = 64 << 10

// MaxReasonLen bounds Source.Reason in bytes.
const MaxReasonLen = 512

// Action names what an audit record describes.
type Action string

const (
	ActionOwnerKeyCreated   Action = "owner_key_created"
	ActionOwnerKeyRewrapped Action = "owner_key_rewrapped"
	ActionOwnerKeyRotated   Action = "owner_key_rotated"
	ActionOwnerKeyDestroyed Action = "owner_key_destroyed"
	ActionSecretPut         Action = "secret_put"
	ActionSecretRead        Action = "secret_read"
	ActionSecretDeleted     Action = "secret_deleted"
)

// Construction and request errors; all match sdk.ErrInvalidInput.
var (
	ErrRepositoryRequired = fmt.Errorf("secrets: repository is required: %w", sdk.ErrInvalidInput)
	ErrWrapperRequired    = fmt.Errorf("secrets: wrapper is required: %w", sdk.ErrInvalidInput)
	ErrSourceRequired     = fmt.Errorf("secrets: source requires exactly one actor or system: %w", sdk.ErrInvalidInput)
	ErrValueEmpty         = fmt.Errorf("secrets: value is empty: %w", sdk.ErrInvalidInput)
	ErrValueTooLarge      = fmt.Errorf("secrets: value exceeds %d bytes: %w", MaxValueBytes, sdk.ErrInvalidInput)
)

// namePattern bounds owner types, owner ids and secret names. ':' and '/' are
// excluded so Owner.String and Ref.String are unambiguous.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// actorPattern bounds Source fields. Actor ids are whatever the host's
// authentication issues (mixed-case, ULIDs, UUIDs, "svc:deploy"), so the
// alphabet is wider than namePattern's; they are stored, never parsed.
var actorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// actions is the closed set an AuditFilter may name.
var actions = map[Action]bool{
	ActionOwnerKeyCreated: true, ActionOwnerKeyRewrapped: true, ActionOwnerKeyRotated: true, ActionOwnerKeyDestroyed: true,
	ActionSecretPut: true, ActionSecretRead: true, ActionSecretDeleted: true,
}

// Wrapper protects owner data keys. Wrap returns an opaque blob and the id of
// the key that produced it; Unwrap reverses it for any key the wrapper still
// holds. A blob under a key the wrapper no longer holds is an error that
// matches sdk.ErrInvalidInput, never a guess.
type Wrapper interface {
	Wrap(ctx context.Context, dek []byte) (blob string, keyID string, err error)
	Unwrap(ctx context.Context, blob string) ([]byte, error)
	ActiveKeyID() string
}

// Repository persists owner keys, sealed secrets and the audit log. Every
// mutating method and GetSecret take the audit records to append and write
// them in the same transaction as the change; a store that cannot do that
// atomically does not conform. Times are stored and returned in UTC at
// microsecond precision. The storetest suite is the executable contract.
type Repository interface {
	// GetOwnerKey returns an owner's wrapped key, or sdk.ErrNotFound.
	GetOwnerKey(ctx context.Context, owner Owner) (OwnerKey, error)
	// CreateOwnerKey stores a first key for an owner; a second is sdk.ErrAlreadyExists.
	CreateOwnerKey(ctx context.Context, key OwnerKey, audit []AuditRecord) error
	// ReplaceOwnerKey swaps an owner's key when the stored KeyID equals
	// expectedKeyID, writing each resealed secret's Ciphertext and KeyID in
	// the same transaction. A different stored KeyID is sdk.ErrConflict; no
	// key is sdk.ErrNotFound.
	ReplaceOwnerKey(ctx context.Context, key OwnerKey, expectedKeyID string, resealed []SealedSecret, audit []AuditRecord) error
	// ListOwnerKeys pages owner keys ordered by owner; after is the previous
	// page's returned cursor, "" for the first page.
	ListOwnerKeys(ctx context.Context, after string, limit int) (keys []OwnerKey, next string, err error)
	// GetSecret returns the sealed secret and records the audit only when it
	// exists; a missing secret is sdk.ErrNotFound with nothing recorded.
	GetSecret(ctx context.Context, ref Ref, audit []AuditRecord) (SealedSecret, error)
	// PutSecret inserts or replaces a secret, assigning Version (1 for a new
	// secret, previous+1 otherwise) and timestamps, and records audit with its
	// Version set to the assigned one. An owner without a key is
	// sdk.ErrInvalidReference.
	PutSecret(ctx context.Context, sealed SealedSecret, audit AuditRecord) (SealedSecret, error)
	// DeleteSecret removes one secret, or sdk.ErrNotFound.
	DeleteSecret(ctx context.Context, ref Ref, audit []AuditRecord) error
	// DeleteOwner removes every secret of an owner and its key, or sdk.ErrNotFound
	// when the owner has no key.
	DeleteOwner(ctx context.Context, owner Owner, audit []AuditRecord) error
	// ListSecrets returns an owner's secrets' metadata ordered by name.
	ListSecrets(ctx context.Context, owner Owner) ([]Secret, error)
	// ListSealedSecrets returns an owner's secrets with ciphertext, ordered by
	// name, for resealing under a new owner key. It is not audited: the rotation
	// that calls it records the key change, and the service never exposes it.
	ListSealedSecrets(ctx context.Context, owner Owner) ([]SealedSecret, error)
	// ListAudit pages records matching filter, oldest first, by (OccurredAt, ID).
	ListAudit(ctx context.Context, filter AuditFilter, after string, limit int) (records []AuditRecord, next string, err error)
}

// Owner scopes secrets and holds one data key: for example {tenant, gps}.
type Owner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Ref names one secret: for example {tenant gps}/cloudflare.api_token.
type Ref struct {
	Owner Owner  `json:"owner"`
	Name  string `json:"name"`
}

// Secret is a stored secret's metadata. The value is never part of it.
type Secret struct {
	Ref       Ref       `json:"ref"`
	Version   int       `json:"version"`
	KeyID     string    `json:"key_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Source attributes an operation to exactly one actor pair or a system, with
// an optional reason. Every read and write requires one.
type Source struct {
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	System    string `json:"system,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// OwnerKey is an owner's data key, wrapped. KeyID is the owner key version the
// service assigns; WrapperKeyID is the wrapper key that produced WrappedKey.
type OwnerKey struct {
	Owner        Owner
	KeyID        string
	WrappedKey   string
	WrapperKeyID string
	CreatedAt    time.Time
	RotatedAt    time.Time
}

// SealedSecret is a secret as stored: sealed under the owner key KeyID names.
type SealedSecret struct {
	Ref        Ref
	Version    int
	KeyID      string
	Ciphertext string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AuditRecord is one appended line of the access log. Name is empty for
// owner-key actions; Version and KeyID are set where they apply.
type AuditRecord struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Action     Action    `json:"action"`
	Owner      Owner     `json:"owner"`
	Name       string    `json:"name,omitempty"`
	Version    int       `json:"version,omitempty"`
	KeyID      string    `json:"key_id,omitempty"`
	Source     Source    `json:"source"`
}

// AuditFilter selects audit records. Zero fields match everything.
type AuditFilter struct {
	Owner     *Owner
	Name      string
	Action    Action
	Since     time.Time // inclusive
	Until     time.Time // exclusive
	ActorType string
	ActorID   string
	System    string
}

// Validate checks the owner's fields.
func (o Owner) Validate() error {
	if !namePattern.MatchString(o.Type) {
		return fmt.Errorf("secrets: owner type must match %s: %w", namePattern, sdk.ErrInvalidInput)
	}
	if !namePattern.MatchString(o.ID) {
		return fmt.Errorf("secrets: owner id must match %s: %w", namePattern, sdk.ErrInvalidInput)
	}
	return nil
}

// String renders "type:id"; the pattern excludes ':' so it is unambiguous.
func (o Owner) String() string { return o.Type + ":" + o.ID }

// Validate checks the owner and the name.
func (r Ref) Validate() error {
	if err := r.Owner.Validate(); err != nil {
		return err
	}
	if !namePattern.MatchString(r.Name) {
		return fmt.Errorf("secrets: name must match %s: %w", namePattern, sdk.ErrInvalidInput)
	}
	return nil
}

// String renders "type:id/name", the additional authenticated data a value is
// sealed with.
func (r Ref) String() string { return r.Owner.String() + "/" + r.Name }

// Validate requires exactly one complete actor pair or a system, and a
// reasonable reason.
func (s Source) Validate() error {
	actor := s.ActorType != "" || s.ActorID != ""
	if actor == (s.System != "") {
		return ErrSourceRequired
	}
	if actor {
		if !actorPattern.MatchString(s.ActorType) || !actorPattern.MatchString(s.ActorID) {
			return fmt.Errorf("secrets: actor type and id must match %s: %w", actorPattern, sdk.ErrInvalidInput)
		}
	} else if !actorPattern.MatchString(s.System) {
		return fmt.Errorf("secrets: system must match %s: %w", actorPattern, sdk.ErrInvalidInput)
	}
	if len(s.Reason) > MaxReasonLen || !utf8.ValidString(s.Reason) || strings.ContainsRune(s.Reason, 0) {
		return fmt.Errorf("secrets: reason must be valid UTF-8 without NUL and at most %d bytes: %w", MaxReasonLen, sdk.ErrInvalidInput)
	}
	return nil
}

// Validate checks every set field.
func (f AuditFilter) Validate() error {
	if f.Owner != nil {
		if err := f.Owner.Validate(); err != nil {
			return err
		}
	}
	if f.Name != "" && !namePattern.MatchString(f.Name) {
		return fmt.Errorf("secrets: audit name filter must match %s: %w", namePattern, sdk.ErrInvalidInput)
	}
	if f.Action != "" && !actions[f.Action] {
		return fmt.Errorf("secrets: unknown audit action %q: %w", f.Action, sdk.ErrInvalidInput)
	}
	for _, v := range []string{f.ActorType, f.ActorID, f.System} {
		if v != "" && !actorPattern.MatchString(v) {
			return fmt.Errorf("secrets: audit source filter must match %s: %w", actorPattern, sdk.ErrInvalidInput)
		}
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Until.After(f.Since) {
		return fmt.Errorf("secrets: audit until must be after since: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// Matches reports whether a record satisfies the filter; stores without a
// query language use it.
func (f AuditFilter) Matches(r AuditRecord) bool {
	if f.Owner != nil && *f.Owner != r.Owner {
		return false
	}
	if f.Name != "" && f.Name != r.Name {
		return false
	}
	if f.Action != "" && f.Action != r.Action {
		return false
	}
	if !f.Since.IsZero() && r.OccurredAt.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !r.OccurredAt.Before(f.Until) {
		return false
	}
	if f.ActorType != "" && f.ActorType != r.Source.ActorType {
		return false
	}
	if f.ActorID != "" && f.ActorID != r.Source.ActorID {
		return false
	}
	if f.System != "" && f.System != r.Source.System {
		return false
	}
	return true
}
