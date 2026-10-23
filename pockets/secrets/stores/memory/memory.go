// Package memory is the secrets pocket's stdlib-only reference store. One
// mutex serializes every call, so each mutation and its audit records land
// together or not at all, which is exactly what the Repository contract asks
// of a SQL store's transaction.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ vault.Repository = (*Store)(nil)

// Store is the in-memory vault.Repository.
type Store struct {
	mu      sync.Mutex
	keys    map[string]vault.OwnerKey     // by Owner.String()
	secrets map[string]vault.SealedSecret // by Ref.String()
	audit   []vault.AuditRecord           // in (OccurredAt, ID) order
	auditID map[string]bool
}

// New returns an empty store.
func New() *Store {
	return &Store{keys: map[string]vault.OwnerKey{}, secrets: map[string]vault.SealedSecret{}, auditID: map[string]bool{}}
}

func (s *Store) GetOwnerKey(ctx context.Context, owner vault.Owner) (vault.OwnerKey, error) {
	if err := ctx.Err(); err != nil {
		return vault.OwnerKey{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keys[owner.String()]
	if !ok {
		return vault.OwnerKey{}, sdk.ErrNotFound
	}
	return key, nil
}

func (s *Store) CreateOwnerKey(ctx context.Context, key vault.OwnerKey, audit []vault.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[key.Owner.String()]; ok {
		return sdk.ErrAlreadyExists
	}
	if err := s.checkAudit(audit); err != nil {
		return err
	}
	s.keys[key.Owner.String()] = normalizeKey(key)
	s.appendAudit(audit)
	return nil
}

func (s *Store) ReplaceOwnerKey(ctx context.Context, key vault.OwnerKey, expectedKeyID string, resealed []vault.SealedSecret, audit []vault.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.keys[key.Owner.String()]
	if !ok {
		return sdk.ErrNotFound
	}
	if current.KeyID != expectedKeyID {
		return fmt.Errorf("secrets memory: owner key is %q, expected %q: %w", current.KeyID, expectedKeyID, sdk.ErrConflict)
	}
	for _, sec := range resealed {
		if sec.Ref.Owner != key.Owner {
			return fmt.Errorf("secrets memory: resealed %s belongs to another owner: %w", sec.Ref, sdk.ErrInvalidInput)
		}
		if _, ok := s.secrets[sec.Ref.String()]; !ok {
			return fmt.Errorf("secrets memory: resealed %s does not exist: %w", sec.Ref, sdk.ErrNotFound)
		}
	}
	if err := s.checkAudit(audit); err != nil {
		return err
	}
	s.keys[key.Owner.String()] = normalizeKey(key)
	for _, sec := range resealed {
		stored := s.secrets[sec.Ref.String()]
		stored.Ciphertext, stored.KeyID, stored.UpdatedAt = sec.Ciphertext, sec.KeyID, utc(sec.UpdatedAt)
		s.secrets[sec.Ref.String()] = stored
	}
	s.appendAudit(audit)
	return nil
}

func (s *Store) ListOwnerKeys(ctx context.Context, after string, limit int) ([]vault.OwnerKey, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if limit <= 0 {
		return nil, "", fmt.Errorf("secrets memory: limit must be positive: %w", sdk.ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	owners := make([]string, 0, len(s.keys))
	for o := range s.keys {
		if o > after {
			owners = append(owners, o)
		}
	}
	sort.Strings(owners)
	out := []vault.OwnerKey{}
	for _, o := range owners {
		if len(out) == limit {
			return out, out[len(out)-1].Owner.String(), nil
		}
		out = append(out, s.keys[o])
	}
	return out, "", nil
}

func (s *Store) GetSecret(ctx context.Context, ref vault.Ref, audit []vault.AuditRecord) (vault.SealedSecret, error) {
	if err := ctx.Err(); err != nil {
		return vault.SealedSecret{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sec, ok := s.secrets[ref.String()]
	if !ok {
		return vault.SealedSecret{}, sdk.ErrNotFound
	}
	audit = stamped(audit, vault.ActionSecretRead, sec)
	if err := s.checkAudit(audit); err != nil {
		return vault.SealedSecret{}, err
	}
	s.appendAudit(audit)
	return sec, nil
}

func (s *Store) PutSecret(ctx context.Context, sealed vault.SealedSecret, audit vault.AuditRecord) (vault.SealedSecret, error) {
	if err := ctx.Err(); err != nil {
		return vault.SealedSecret{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[sealed.Ref.Owner.String()]; !ok {
		return vault.SealedSecret{}, fmt.Errorf("secrets memory: owner %s has no key: %w", sealed.Ref.Owner, sdk.ErrInvalidReference)
	}
	now := utc(sealed.UpdatedAt)
	stored := vault.SealedSecret{Ref: sealed.Ref, Version: 1, KeyID: sealed.KeyID, Ciphertext: sealed.Ciphertext, CreatedAt: utc(sealed.CreatedAt), UpdatedAt: now}
	if prev, ok := s.secrets[sealed.Ref.String()]; ok {
		stored.Version, stored.CreatedAt = prev.Version+1, prev.CreatedAt
	}
	audit.Version = stored.Version
	if err := s.checkAudit([]vault.AuditRecord{audit}); err != nil {
		return vault.SealedSecret{}, err
	}
	s.secrets[sealed.Ref.String()] = stored
	s.appendAudit([]vault.AuditRecord{audit})
	return stored, nil
}

func (s *Store) DeleteSecret(ctx context.Context, ref vault.Ref, audit []vault.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sec, ok := s.secrets[ref.String()]
	if !ok {
		return sdk.ErrNotFound
	}
	audit = stamped(audit, vault.ActionSecretDeleted, sec)
	if err := s.checkAudit(audit); err != nil {
		return err
	}
	delete(s.secrets, ref.String())
	s.appendAudit(audit)
	return nil
}

func (s *Store) DeleteOwner(ctx context.Context, owner vault.Owner, expected []string, audit []vault.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[owner.String()]; !ok {
		return sdk.ErrNotFound
	}
	removed := map[string]vault.SealedSecret{}
	for _, sec := range s.secrets {
		if sec.Ref.Owner == owner {
			removed[sec.Ref.Name] = sec
		}
	}
	want := map[string]bool{}
	for _, name := range expected {
		want[name] = true
	}
	if len(want) != len(removed) {
		return fmt.Errorf("secrets memory: owner %s holds %d secrets, %d expected: %w", owner, len(removed), len(want), sdk.ErrConflict)
	}
	for name := range want {
		if _, ok := removed[name]; !ok {
			return fmt.Errorf("secrets memory: owner %s holds no secret %q: %w", owner, name, sdk.ErrConflict)
		}
	}
	for _, sec := range removed {
		audit = stamped(audit, vault.ActionSecretDeleted, sec)
	}
	if err := s.checkAudit(audit); err != nil {
		return err
	}
	for k, sec := range s.secrets {
		if sec.Ref.Owner == owner {
			delete(s.secrets, k)
		}
	}
	delete(s.keys, owner.String())
	s.appendAudit(audit)
	return nil
}

func (s *Store) ListSecrets(ctx context.Context, owner vault.Owner) ([]vault.Secret, error) {
	sealed, err := s.ListSealedSecrets(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]vault.Secret, len(sealed))
	for i, sec := range sealed {
		out[i] = vault.Secret{Ref: sec.Ref, Version: sec.Version, KeyID: sec.KeyID, CreatedAt: sec.CreatedAt, UpdatedAt: sec.UpdatedAt}
	}
	return out, nil
}

func (s *Store) ListSealedSecrets(ctx context.Context, owner vault.Owner) ([]vault.SealedSecret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []vault.SealedSecret{}
	for _, sec := range s.secrets {
		if sec.Ref.Owner == owner {
			out = append(out, sec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.Name < out[j].Ref.Name })
	return out, nil
}

func (s *Store) AppendAudit(ctx context.Context, records []vault.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAudit(records); err != nil {
		return err
	}
	s.appendAudit(records)
	return nil
}

func (s *Store) ListAudit(ctx context.Context, filter vault.AuditFilter, after string, limit int) ([]vault.AuditRecord, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if limit <= 0 {
		return nil, "", fmt.Errorf("secrets memory: limit must be positive: %w", sdk.ErrInvalidInput)
	}
	if err := filter.Validate(); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []vault.AuditRecord{}
	for _, r := range s.audit {
		if r.ID <= after || !filter.Matches(r) {
			continue
		}
		if len(out) == limit {
			return out, out[len(out)-1].ID, nil
		}
		out = append(out, r)
	}
	return out, "", nil
}

// checkAudit refuses, before any mutation, every record a SQL store's
// secrets_audit constraints refuse: no id, an action or outcome outside the
// vocabulary, or an id already recorded. The closed sets are the ones
// vault.AuditFilter validates.
func (s *Store) checkAudit(records []vault.AuditRecord) error {
	seen := map[string]bool{}
	for _, r := range records {
		if r.ID == "" {
			return fmt.Errorf("secrets memory: audit record without id: %w", sdk.ErrInvalidInput)
		}
		if r.Action == "" || r.Outcome == "" || (vault.AuditFilter{Action: r.Action, Outcome: r.Outcome}).Validate() != nil {
			return fmt.Errorf("secrets memory: audit record %q has action %q and outcome %q: %w", r.ID, r.Action, r.Outcome, sdk.ErrInvalidInput)
		}
		if s.auditID[r.ID] || seen[r.ID] {
			return fmt.Errorf("secrets memory: audit record %q already recorded: %w", r.ID, sdk.ErrAlreadyExists)
		}
		seen[r.ID] = true
	}
	return nil
}

func (s *Store) appendAudit(records []vault.AuditRecord) {
	for _, r := range records {
		r.OccurredAt = utc(r.OccurredAt)
		s.auditID[r.ID] = true
		s.audit = append(s.audit, r)
	}
	sort.SliceStable(s.audit, func(i, j int) bool {
		a, b := s.audit[i], s.audit[j]
		if !a.OccurredAt.Equal(b.OccurredAt) {
			return a.OccurredAt.Before(b.OccurredAt)
		}
		return a.ID < b.ID
	})
}

// stamped returns audit with every action record naming sec given its
// Version and KeyID: what the store read or removed, not what the caller
// expected.
func stamped(audit []vault.AuditRecord, action vault.Action, sec vault.SealedSecret) []vault.AuditRecord {
	out := make([]vault.AuditRecord, len(audit))
	for i, r := range audit {
		if r.Action == action && r.Owner == sec.Ref.Owner && r.Name == sec.Ref.Name {
			r.Version, r.KeyID = sec.Version, sec.KeyID
		}
		out[i] = r
	}
	return out
}

func normalizeKey(key vault.OwnerKey) vault.OwnerKey {
	key.CreatedAt, key.RotatedAt = utc(key.CreatedAt), utc(key.RotatedAt)
	return key
}

func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
