package vault

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Option configures a Service.
type Option func(*config)

type config struct {
	clock func() time.Time
}

// WithClock replaces the audit clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option { return func(c *config) { c.clock = clock } }

// Service is the vault: owner-scoped Put, Get, Delete and DeleteOwner, each
// attributed and audited, plus unaudited metadata listing.
type Service struct {
	repo    Repository
	wrapper Wrapper
	now     func() time.Time
}

// NewService validates its ports. It touches no storage.
func NewService(repo Repository, wrapper Wrapper, opts ...Option) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryRequired
	}
	if wrapper == nil {
		return nil, ErrWrapperRequired
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("secrets: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.clock == nil {
		cfg.clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, wrapper: wrapper, now: cfg.clock}, nil
}

// Put stores value under ref, creating the owner's data key on its first
// secret. It returns the stored metadata; Version is 1 for a new secret.
func (s *Service) Put(ctx context.Context, ref Ref, value []byte, source Source) (Secret, error) {
	if err := validateRequest(ref.Validate, source); err != nil {
		return Secret{}, err
	}
	if len(value) == 0 {
		return Secret{}, ErrValueEmpty
	}
	if len(value) > MaxValueBytes {
		return Secret{}, ErrValueTooLarge
	}
	key, dek, err := s.ownerKey(ctx, ref.Owner, source, true)
	if err != nil {
		return Secret{}, err
	}
	ciphertext, err := Seal(dek, ref, value)
	if err != nil {
		return Secret{}, err
	}
	now := s.now()
	record, err := NewAuditRecord(now, ActionSecretPut, ref.Owner, ref.Name, 0, key.KeyID, source)
	if err != nil {
		return Secret{}, err
	}
	stored, err := s.repo.PutSecret(ctx, SealedSecret{Ref: ref, KeyID: key.KeyID, Ciphertext: ciphertext, CreatedAt: now, UpdatedAt: now}, record)
	if err != nil {
		return Secret{}, err
	}
	return stored.metadata(), nil
}

// Get returns the value stored under ref and records who read it. A missing
// secret is sdk.ErrNotFound and records nothing.
func (s *Service) Get(ctx context.Context, ref Ref, source Source) ([]byte, error) {
	if err := validateRequest(ref.Validate, source); err != nil {
		return nil, err
	}
	key, dek, err := s.ownerKey(ctx, ref.Owner, source, false)
	if err != nil {
		return nil, err
	}
	record, err := NewAuditRecord(s.now(), ActionSecretRead, ref.Owner, ref.Name, 0, key.KeyID, source)
	if err != nil {
		return nil, err
	}
	sealed, err := s.repo.GetSecret(ctx, ref, []AuditRecord{record})
	if err != nil {
		return nil, err
	}
	if sealed.KeyID != key.KeyID {
		return nil, fmt.Errorf("secrets: %s is sealed under owner key %q but the owner holds %q: %w", ref, sealed.KeyID, key.KeyID, sdk.ErrConflict)
	}
	return Open(dek, ref, sealed.Ciphertext)
}

// Delete removes one secret, or sdk.ErrNotFound.
func (s *Service) Delete(ctx context.Context, ref Ref, source Source) error {
	if err := validateRequest(ref.Validate, source); err != nil {
		return err
	}
	record, err := NewAuditRecord(s.now(), ActionSecretDeleted, ref.Owner, ref.Name, 0, "", source)
	if err != nil {
		return err
	}
	return s.repo.DeleteSecret(ctx, ref, []AuditRecord{record})
}

// DeleteOwner removes every secret of an owner and destroys its data key in
// one transaction, recording each secret's deletion and the key's
// destruction. An owner with no key is sdk.ErrNotFound.
func (s *Service) DeleteOwner(ctx context.Context, owner Owner, source Source) error {
	if err := validateRequest(owner.Validate, source); err != nil {
		return err
	}
	key, err := s.repo.GetOwnerKey(ctx, owner)
	if err != nil {
		return err
	}
	secrets, err := s.repo.ListSecrets(ctx, owner)
	if err != nil {
		return err
	}
	now := s.now()
	records := make([]AuditRecord, 0, len(secrets)+1)
	for _, sec := range secrets {
		record, err := NewAuditRecord(now, ActionSecretDeleted, owner, sec.Ref.Name, sec.Version, sec.KeyID, source)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	record, err := NewAuditRecord(now, ActionOwnerKeyDestroyed, owner, "", 0, key.KeyID, source)
	if err != nil {
		return err
	}
	return s.repo.DeleteOwner(ctx, owner, append(records, record))
}

// List returns an owner's secrets' metadata, ordered by name. It is not
// audited: it reveals names, never values.
func (s *Service) List(ctx context.Context, owner Owner) ([]Secret, error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	return s.repo.ListSecrets(ctx, owner)
}

// Audit pages the access log, oldest first. limit <= 0 selects 100.
func (s *Service) Audit(ctx context.Context, filter AuditFilter, after string, limit int) ([]AuditRecord, string, error) {
	if err := filter.Validate(); err != nil {
		return nil, "", err
	}
	if limit <= 0 {
		limit = 100
	}
	return s.repo.ListAudit(ctx, filter, after, limit)
}

// ownerKey loads and unwraps an owner's data key. With create set, an owner
// without one gets a fresh key; a concurrent first Put that wins the insert is
// read back rather than failed.
func (s *Service) ownerKey(ctx context.Context, owner Owner, source Source, create bool) (OwnerKey, []byte, error) {
	key, err := s.repo.GetOwnerKey(ctx, owner)
	if err == nil {
		dek, err := s.wrapper.Unwrap(ctx, key.WrappedKey)
		return key, dek, err
	}
	if !errors.Is(err, sdk.ErrNotFound) || !create {
		return OwnerKey{}, nil, err
	}
	dek, err := NewDataKey()
	if err != nil {
		return OwnerKey{}, nil, err
	}
	key, err = s.wrapKey(ctx, owner, dek)
	if err != nil {
		return OwnerKey{}, nil, err
	}
	record, err := NewAuditRecord(key.CreatedAt, ActionOwnerKeyCreated, owner, "", 0, key.KeyID, source)
	if err != nil {
		return OwnerKey{}, nil, err
	}
	switch err := s.repo.CreateOwnerKey(ctx, key, []AuditRecord{record}); {
	case err == nil:
		return key, dek, nil
	case errors.Is(err, sdk.ErrAlreadyExists):
		return s.ownerKey(ctx, owner, source, false)
	default:
		return OwnerKey{}, nil, err
	}
}

// wrapKey builds a new OwnerKey record for dek, wrapped under the active
// wrapper key.
func (s *Service) wrapKey(ctx context.Context, owner Owner, dek []byte) (OwnerKey, error) {
	keyID, err := NewOwnerKeyID()
	if err != nil {
		return OwnerKey{}, err
	}
	blob, wrapperKeyID, err := s.wrapper.Wrap(ctx, dek)
	if err != nil {
		return OwnerKey{}, err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	return OwnerKey{Owner: owner, KeyID: keyID, WrappedKey: blob, WrapperKeyID: wrapperKeyID, CreatedAt: now, RotatedAt: now}, nil
}

func validateRequest(target func() error, source Source) error {
	if err := target(); err != nil {
		return err
	}
	return source.Validate()
}

func (s SealedSecret) metadata() Secret {
	return Secret{Ref: s.Ref, Version: s.Version, KeyID: s.KeyID, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
