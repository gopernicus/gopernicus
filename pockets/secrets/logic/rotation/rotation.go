// Package rotation re-keys the vault without downtime. RewrapOwnerKeys follows
// a master (wrapper) key change: it rewraps each owner's data key under the
// active wrapper key and touches no secret. RotateOwnerKey replaces one
// owner's data key and reseals that owner's secrets in one transaction. Both
// are library calls a host wires into its own command; the pocket ships no CLI.
package rotation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultPage is the ListOwnerKeys page size when RewrapOptions.Page <= 0.
const DefaultPage = 200

// Option configures a Service.
type Option func(*config)

type config struct {
	clock func() time.Time
}

// WithClock replaces the audit clock. Nil selects time.Now in UTC.
func WithClock(clock func() time.Time) Option { return func(c *config) { c.clock = clock } }

// RewrapOptions tunes RewrapOwnerKeys.
type RewrapOptions struct {
	// Page is the owner-key page size; <= 0 selects DefaultPage.
	Page int
	// DryRun counts what would be rewrapped and writes nothing.
	DryRun bool
}

// Report is what a rewrap did. Failed lists owners whose key could not be
// rewrapped, typically because it is under a wrapper key no longer held; the
// run continues past them and the host decides.
type Report struct {
	Scanned   int           `json:"scanned"`
	Rewrapped int           `json:"rewrapped"`
	Skipped   int           `json:"skipped"`
	Failed    []vault.Owner `json:"failed,omitempty"`
}

// Service runs rotations against the same repository and wrapper as the vault.
type Service struct {
	repo    vault.Repository
	wrapper vault.Wrapper
	now     func() time.Time
}

// NewService validates its ports. It touches no storage.
func NewService(repo vault.Repository, wrapper vault.Wrapper, opts ...Option) (*Service, error) {
	if repo == nil {
		return nil, vault.ErrRepositoryRequired
	}
	if wrapper == nil {
		return nil, vault.ErrWrapperRequired
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("secrets rotation: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.clock == nil {
		cfg.clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: repo, wrapper: wrapper, now: cfg.clock}, nil
}

// RewrapOwnerKeys rewraps every owner key not already under the wrapper's
// active key. It is idempotent and safe to rerun; a concurrent change to an
// owner's key is retried once, then counted as failed.
func (s *Service) RewrapOwnerKeys(ctx context.Context, source vault.Source, opts RewrapOptions) (Report, error) {
	if err := source.Validate(); err != nil {
		return Report{}, err
	}
	if opts.Page <= 0 {
		opts.Page = DefaultPage
	}
	var report Report
	active := s.wrapper.ActiveKeyID()
	for after := ""; ; {
		keys, next, err := s.repo.ListOwnerKeys(ctx, after, opts.Page)
		if err != nil {
			return report, err
		}
		for _, key := range keys {
			report.Scanned++
			if key.WrapperKeyID == active {
				report.Skipped++
				continue
			}
			if opts.DryRun {
				report.Rewrapped++
				continue
			}
			err := s.rewrap(ctx, key, source)
			if errors.Is(err, sdk.ErrConflict) {
				if key, err = s.repo.GetOwnerKey(ctx, key.Owner); err == nil {
					err = s.rewrap(ctx, key, source)
				}
			}
			switch {
			case err == nil:
				report.Rewrapped++
			case errors.Is(err, sdk.ErrInvalidInput), errors.Is(err, sdk.ErrConflict), errors.Is(err, sdk.ErrNotFound):
				report.Failed = append(report.Failed, key.Owner)
			default:
				return report, err
			}
		}
		if next == "" {
			return report, nil
		}
		after = next
	}
}

// rewrap unwraps one owner key and stores it wrapped under the active key.
// The data key, its id and every secret stay as they are.
func (s *Service) rewrap(ctx context.Context, key vault.OwnerKey, source vault.Source) error {
	dek, err := s.wrapper.Unwrap(ctx, key.WrappedKey)
	if err != nil {
		return err
	}
	blob, wrapperKeyID, err := s.wrapper.Wrap(ctx, dek)
	if err != nil {
		return err
	}
	record, err := vault.NewAuditRecord(s.now(), vault.ActionOwnerKeyRewrapped, key.Owner, "", 0, key.KeyID, source)
	if err != nil {
		return err
	}
	rewrapped := key
	rewrapped.WrappedKey, rewrapped.WrapperKeyID = blob, wrapperKeyID
	return s.repo.ReplaceOwnerKey(ctx, rewrapped, key.KeyID, nil, []vault.AuditRecord{record})
}

// RotateOwnerKey gives an owner a new data key and reseals its secrets under
// it, atomically. An owner with no key is sdk.ErrNotFound; a concurrent
// change to the key is sdk.ErrConflict and nothing is written.
func (s *Service) RotateOwnerKey(ctx context.Context, owner vault.Owner, source vault.Source) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	if err := source.Validate(); err != nil {
		return err
	}
	old, err := s.repo.GetOwnerKey(ctx, owner)
	if err != nil {
		return err
	}
	oldDEK, err := s.wrapper.Unwrap(ctx, old.WrappedKey)
	if err != nil {
		return err
	}
	sealed, err := s.repo.ListSealedSecrets(ctx, owner)
	if err != nil {
		return err
	}
	newDEK, err := vault.NewDataKey()
	if err != nil {
		return err
	}
	keyID, err := vault.NewOwnerKeyID()
	if err != nil {
		return err
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	for i, sec := range sealed {
		if sec.KeyID != old.KeyID {
			return fmt.Errorf("secrets rotation: %s is sealed under %q, not the owner's key %q: %w", sec.Ref, sec.KeyID, old.KeyID, sdk.ErrConflict)
		}
		value, err := vault.Open(oldDEK, sec.Ref, sec.Ciphertext)
		if err != nil {
			return err
		}
		if sealed[i].Ciphertext, err = vault.Seal(newDEK, sec.Ref, value); err != nil {
			return err
		}
		sealed[i].KeyID = keyID
		sealed[i].UpdatedAt = now
	}
	blob, wrapperKeyID, err := s.wrapper.Wrap(ctx, newDEK)
	if err != nil {
		return err
	}
	record, err := vault.NewAuditRecord(now, vault.ActionOwnerKeyRotated, owner, "", 0, keyID, source)
	if err != nil {
		return err
	}
	rotated := vault.OwnerKey{Owner: owner, KeyID: keyID, WrappedKey: blob, WrapperKeyID: wrapperKeyID, CreatedAt: old.CreatedAt, RotatedAt: now}
	return s.repo.ReplaceOwnerKey(ctx, rotated, old.KeyID, sealed, []vault.AuditRecord{record})
}
