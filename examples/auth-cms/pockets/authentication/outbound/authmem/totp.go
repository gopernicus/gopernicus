package authmem

import (
	"context"
	"slices"
	"time"

	challenge "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	passkey "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	totp "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/sdk"
)

// --- totp.Repository ---

// totpRepo is the second-factor aggregate (SOC 2 S4c). Confirm and Remove run
// under the one mutex that also serializes the credential-mutation rail —
// this store's user-row lock.
type totpRepo struct{ *data }

var _ totp.Repository = totpRepo{}

func cloneTOTPCredential(c totp.Credential) totp.Credential {
	for _, p := range []**time.Time{&c.ExpiresAt, &c.ConfirmedAt, &c.LockedUntil, &c.LastUsedAt} {
		if *p != nil {
			at := **p
			*p = &at
		}
	}
	return c
}

func (r totpRepo) Begin(_ context.Context, c totp.Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ex, ok := r.totpCreds[c.UserID]; ok && ex.Confirmed() {
		return totp.ErrAlreadyEnrolled
	}
	c.ConfirmedAt, c.LastUsedStep, c.FailedAttempts, c.LockedUntil, c.LastUsedAt = nil, 0, 0, nil, nil
	r.totpCreds[c.UserID] = cloneTOTPCredential(c)
	return nil
}

func (r totpRepo) Get(_ context.Context, userID string) (totp.Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.totpCreds[userID]
	if !ok {
		return totp.Credential{}, sdk.ErrNotFound
	}
	return cloneTOTPCredential(c), nil
}

func (r totpRepo) Confirm(_ context.Context, in totp.Confirmation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[in.UserID]
	if !ok {
		return sdk.ErrNotFound
	}
	if !r.projectLocked(u).Active() {
		return session.ErrUserNotActive
	}
	c, ok := r.totpCreds[in.UserID]
	switch {
	case !ok:
		return sdk.ErrNotFound
	case c.Confirmed():
		return totp.ErrAlreadyEnrolled
	case u.AuthRevision != in.ExpectedAuthRevision:
		return sdk.ErrConflict
	case in.Bootstrap && slices.ContainsFunc(r.passkeysOfLocked(in.UserID), passkey.Passkey.AAL2Capable):
		return totp.ErrBootstrapClosed
	}
	seen := map[string]bool{}
	for _, code := range in.Codes {
		if seen[code.Digest] {
			return sdk.ErrAlreadyExists
		}
		seen[code.Digest] = true
	}
	at := in.ConfirmedAt
	c.ConfirmedAt, c.LastUsedStep = &at, in.Step
	c.SessionID, c.Tier, c.AuthRevision, c.ExpiresAt, c.FailedAttempts, c.LockedUntil = "", "", 0, nil, 0, nil
	r.totpCreds[in.UserID] = c
	r.totpCodes[in.UserID] = slices.Clone(in.Codes)
	r.advanceCredentialRevisionLocked(in.UserID, time.Now())
	return nil
}

func (r totpRepo) RecordUse(_ context.Context, userID string, expectedStep, step int64, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.totpCreds[userID]
	if !ok || !c.Confirmed() || c.LastUsedStep != expectedStep || step <= expectedStep {
		return sdk.ErrConflict
	}
	c.LastUsedStep, c.LastUsedAt, c.FailedAttempts, c.LockedUntil = step, &at, 0, nil
	r.totpCreds[userID] = c
	return nil
}

func (r totpRepo) RecordFailure(_ context.Context, userID string, lockAt int, lockFor time.Duration, now time.Time) (totp.Failure, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.totpCreds[userID]
	if !ok {
		return totp.Failure{}, sdk.ErrNotFound
	}
	c.FailedAttempts++
	out := totp.Failure{Attempts: c.FailedAttempts}
	if c.FailedAttempts >= lockAt {
		until := now.Add(lockFor)
		c.FailedAttempts, c.LockedUntil = 0, &until
		out.LockedUntil = &until
	}
	r.totpCreds[userID] = c
	return out, nil
}

func (r totpRepo) generatedAtLocked(userID string) *time.Time {
	codes := r.totpCodes[userID]
	if len(codes) == 0 {
		return nil
	}
	at := codes[0].GeneratedAt
	return &at
}

func (r totpRepo) ReplaceRecoveryCodes(_ context.Context, userID string, codes []totp.RecoveryCode, expected *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.totpCreds[userID]; !ok || !c.Confirmed() {
		return sdk.ErrNotFound
	}
	if expected != nil {
		if current := r.generatedAtLocked(userID); current == nil || !current.Equal(*expected) {
			return sdk.ErrConflict
		}
	}
	r.totpCodes[userID] = slices.Clone(codes)
	return nil
}

func (r totpRepo) ConsumeRecoveryCode(_ context.Context, userID string, candidates []challenge.DigestCandidate, _ time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	codes := r.totpCodes[userID]
	for i, code := range codes {
		if slices.ContainsFunc(candidates, func(c challenge.DigestCandidate) bool { return c.KeyID == code.KeyID && c.Digest == code.Digest }) {
			r.totpCodes[userID] = slices.Delete(slices.Clone(codes), i, i+1)
			return len(r.totpCodes[userID]), nil
		}
	}
	return 0, sdk.ErrNotFound
}

func (r totpRepo) CountRecoveryCodes(_ context.Context, userID string) (int, *time.Time, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.totpCodes[userID]), r.generatedAtLocked(userID), nil
}

func (r totpRepo) Remove(_ context.Context, userID string, expectedRevision int64) (totp.Removed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[userID]
	if !ok {
		return totp.Removed{}, sdk.ErrNotFound
	}
	if u.AuthRevision != expectedRevision {
		return totp.Removed{}, sdk.ErrConflict
	}
	if _, ok := r.totpCreds[userID]; !ok {
		return totp.Removed{}, nil
	}
	removed := totp.Removed{Credential: true, RecoveryCodes: len(r.totpCodes[userID])}
	delete(r.totpCreds, userID)
	delete(r.totpCodes, userID)
	r.advanceCredentialRevisionLocked(userID, time.Now())
	r.revokeCredentialStateLocked(userID)
	return removed, nil
}

func (r totpRepo) RecoveryCodeKeyIDs(context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for _, codes := range r.totpCodes {
		for _, code := range codes {
			if !slices.Contains(out, code.KeyID) {
				out = append(out, code.KeyID)
			}
		}
	}
	return out, nil
}
