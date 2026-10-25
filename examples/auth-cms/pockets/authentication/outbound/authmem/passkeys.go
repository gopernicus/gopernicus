package authmem

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"time"

	passkey "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ passkey.Repository = passkeyRepo{}

// passkeyRepo is the passkey store (SOC 2 S4b). Create runs its guard under the
// shared mutex the credential-mutation rail serializes on — this store's
// user-row lock — so a concurrent removal or a second registration observes it.
type passkeyRepo struct{ *data }

// passkeysOfLocked returns userID's passkeys oldest first; callers hold d.mu.
func (d *data) passkeysOfLocked(userID string) []passkey.Passkey {
	var out []passkey.Passkey
	for _, p := range d.passkeys {
		if p.UserID == userID {
			out = append(out, clonePasskey(p))
		}
	}
	slices.SortFunc(out, func(a, b passkey.Passkey) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func clonePasskey(p passkey.Passkey) passkey.Passkey {
	p.CredentialID = slices.Clone(p.CredentialID)
	p.PublicKey = slices.Clone(p.PublicKey)
	p.AAGUID = slices.Clone(p.AAGUID)
	p.UserHandle = slices.Clone(p.UserHandle)
	p.Transports = slices.Clone(p.Transports)
	if p.LastUsedAt != nil {
		at := *p.LastUsedAt
		p.LastUsedAt = &at
	}
	return p
}

func (r passkeyRepo) Create(ctx context.Context, in passkey.CreateInput) (passkey.Passkey, error) {
	if err := ctx.Err(); err != nil {
		return passkey.Passkey{}, err
	}
	p := in.Passkey
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[p.UserID]
	if !ok {
		return passkey.Passkey{}, sdk.ErrNotFound
	}
	if !r.projectLocked(u).Active() {
		return passkey.Passkey{}, session.ErrUserNotActive
	}
	if u.AuthRevision != in.ExpectedAuthRevision {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	owned := r.passkeysOfLocked(p.UserID)
	if in.Bootstrap && slices.ContainsFunc(owned, passkey.Passkey.AAL2Capable) {
		return passkey.Passkey{}, passkey.ErrBootstrapClosed
	}
	for _, existing := range r.passkeys {
		if bytes.Equal(existing.CredentialID, p.CredentialID) {
			return passkey.Passkey{}, sdk.ErrAlreadyExists
		}
	}
	if len(owned) >= in.MaxPerUser {
		return passkey.Passkey{}, passkey.ErrPasskeyLimit
	}
	if len(owned) > 0 && !bytes.Equal(owned[0].UserHandle, p.UserHandle) {
		return passkey.Passkey{}, sdk.ErrConflict
	}
	if p.ID == "" {
		p.ID = ids.MustGenerate()
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.LastUsedAt = nil
	r.passkeys[p.ID] = clonePasskey(p)
	return clonePasskey(p), nil
}

func (r passkeyRepo) GetByCredentialID(_ context.Context, credentialID []byte) (passkey.Passkey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.passkeys {
		if bytes.Equal(p.CredentialID, credentialID) {
			return clonePasskey(p), nil
		}
	}
	return passkey.Passkey{}, sdk.ErrNotFound
}

func (r passkeyRepo) ListByUser(_ context.Context, userID string) ([]passkey.Passkey, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := r.passkeysOfLocked(userID)
	if out == nil {
		out = []passkey.Passkey{}
	}
	return out, nil
}

func (r passkeyRepo) Rename(_ context.Context, id, userID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passkeys[id]
	if !ok || p.UserID != userID {
		return sdk.ErrNotFound
	}
	p.Name = name
	r.passkeys[id] = p
	return nil
}

func (r passkeyRepo) RecordUse(_ context.Context, id string, expected, count uint32, backupState bool, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.passkeys[id]
	if !ok || p.SignCount != expected {
		return sdk.ErrConflict
	}
	at = at.UTC()
	p.SignCount, p.BackupState, p.LastUsedAt = count, backupState, &at
	r.passkeys[id] = p
	return nil
}
