// This file wires authmem's v3 atomic-rail ports (AV3-1.4): the challenge secret
// rail, the pending contact-change flow state, recent-authentication (step-up)
// grants, the revision-serialized credential-mutation rail, and the durable
// delivery-job outbox. Each is a thin view over the one shared *data holder and
// its single mutex, so every promised atomic operation runs inside ONE mutex-held
// critical section — no fake cross-repository transaction. The behavior mirrors
// the storetest reference (features/authentication/storetest/reference_test.go),
// which the exported conformance suite proves against this store; authmem's one
// intentional divergence is that auth_revision rides the user row (its
// single-anchor model) rather than a separate revision map.
package authmem

import (
	"context"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// Compile-time proof that each thin view fills its exact port.
var (
	_ challenge.Repository          = challengeRepo{}
	_ passwordreset.Repository      = passwordResetRepo{}
	_ contactchange.Repository      = contactChangeRepo{}
	_ authgrant.Repository          = authGrantRepo{}
	_ credential.MutationRepository = credentialMutationRepo{}
)

// --- challenge.Repository ---

// challengeRepo keys challenges by ID and hand-enforces the atomic-secret
// invariants a SQL store gets from its indexes and transactional consume: one
// active row per (user, purpose), a unique (purpose, secret_digest) claim, and a
// consume that decides expiry, digest comparison, attempt counting, lockout, and
// deletion inside ONE mutex-held critical section — the "exactly one winner"
// contract. Digest comparison routes through auth.ConstantTimeDigestEqual, whose
// empty-hash guard makes an empty candidate never match.
type challengeRepo struct{ *data }

func (r challengeRepo) Replace(_ context.Context, c challenge.Challenge) (challenge.Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Delete the prior (user, purpose) row (the single-active claim).
	for id, ex := range r.challenges {
		if ex.UserID == c.UserID && ex.Purpose == c.Purpose {
			delete(r.challenges, id)
		}
	}
	// Enforce the (purpose, secret_digest) unique index against the remainder.
	for _, ex := range r.challenges {
		if ex.Purpose == c.Purpose && ex.SecretDigest == c.SecretDigest {
			return challenge.Challenge{}, sdk.ErrAlreadyExists
		}
	}
	if c.ID == "" {
		c.ID = ids.MustGenerate()
	}
	if c.Version == 0 {
		c.Version = 1
	}
	r.challenges[c.ID] = c
	return c, nil
}

func (r challengeRepo) ConsumeCode(_ context.Context, userID, purpose string, candidates []challenge.DigestCandidate,
	expectedContextDigest string, maxAttempts int, now time.Time) (challenge.Consumed, challenge.ConsumeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id, row, found := r.findChallengeByUserPurpose(userID, purpose)
	if !found {
		return challenge.Consumed{}, challenge.OutcomeNotFound, nil
	}
	if row.Expired(now) {
		delete(r.challenges, id)
		return challenge.Consumed{}, challenge.OutcomeExpired, nil
	}
	// Select the candidate naming the row's key, then compare in constant time.
	matched := false
	for _, cand := range candidates {
		if cand.KeyID == row.ProtectorKeyID && auth.ConstantTimeDigestEqual(cand.Digest, row.SecretDigest) {
			matched = true
			break
		}
	}
	if !matched {
		newCount := row.AttemptCount + 1
		if newCount >= maxAttempts {
			delete(r.challenges, id)
			return challenge.Consumed{}, challenge.OutcomeLockedOut, nil
		}
		row.AttemptCount = newCount
		r.challenges[id] = row
		return challenge.Consumed{}, challenge.OutcomeRejected, nil
	}
	// Correct code — the row is consumed regardless of context (anti-probing).
	delete(r.challenges, id)
	if expectedContextDigest != "" && string(row.Context) != expectedContextDigest {
		return consumedOf(row, now), challenge.OutcomeContextMismatch, nil
	}
	return consumedOf(row, now), challenge.OutcomeRedeemed, nil
}

func (r challengeRepo) ConsumeToken(_ context.Context, purpose, presentedDigest string, now time.Time) (challenge.Consumed, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if presentedDigest == "" {
		return challenge.Consumed{}, sdk.ErrNotFound
	}
	for id, ex := range r.challenges {
		if ex.Purpose == purpose && ex.SecretDigest == presentedDigest {
			delete(r.challenges, id) // delete-returning regardless of expiry
			if ex.Expired(now) {
				return challenge.Consumed{}, sdk.ErrExpired
			}
			return consumedOf(ex, now), nil
		}
	}
	return challenge.Consumed{}, sdk.ErrNotFound
}

func (r challengeRepo) PurgeExpired(_ context.Context, before time.Time, limit int) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, ex := range r.challenges {
		if limit > 0 && n >= limit {
			break
		}
		if !ex.ExpiresAt.After(before) { // expires_at <= before
			delete(r.challenges, id)
			n++
		}
	}
	return n, nil
}

// findChallengeByUserPurpose returns the single (user, purpose) row; callers hold d.mu.
func (r challengeRepo) findChallengeByUserPurpose(userID, purpose string) (string, challenge.Challenge, bool) {
	for id, ex := range r.challenges {
		if ex.UserID == userID && ex.Purpose == purpose {
			return id, ex, true
		}
	}
	return "", challenge.Challenge{}, false
}

func consumedOf(c challenge.Challenge, now time.Time) challenge.Consumed {
	return challenge.Consumed{
		ID:             c.ID,
		UserID:         c.UserID,
		Purpose:        c.Purpose,
		Context:        c.Context,
		ProtectorKeyID: c.ProtectorKeyID,
		ConsumedAt:     now.UTC(),
	}
}

// --- passwordreset.Repository ---

// passwordResetRepo performs the atomic reset composition (design §5.9) inside ONE
// mutex-held critical section: a guarded resolve of the live (purpose, digest)
// token (unknown/consumed/expired are all not-live → sdk.ErrNotFound), then the
// password upsert and the revocation of every session, recent-authentication
// grant, and outstanding password/reset challenge for the resolved user. A SQL
// store gets the all-or-nothing guarantee from its transaction; authmem does the
// whole thing under one lock so no partial state is observable, and two
// simultaneous resets of one token yield exactly one success.
type passwordResetRepo struct{ *data }

func (r passwordResetRepo) Redeem(_ context.Context, in passwordreset.RedeemInput) (passwordreset.RedeemResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.TokenDigest == "" {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	var (
		chID   string
		userID string
	)
	found := false
	for id, ex := range r.challenges {
		if ex.Purpose == in.Purpose && ex.SecretDigest == in.TokenDigest {
			if ex.Expired(in.Now) {
				return passwordreset.RedeemResult{}, sdk.ErrNotFound // expired is not live (left for purge)
			}
			chID, userID, found = id, ex.UserID, true
			break
		}
	}
	if !found {
		return passwordreset.RedeemResult{}, sdk.ErrNotFound
	}
	delete(r.challenges, chID)
	r.passwords[userID] = in.NewPasswordHash
	for id, s := range r.sessions {
		if s.UserID == userID {
			delete(r.sessions, id)
		}
	}
	for id, g := range r.authGrants {
		if g.UserID == userID {
			delete(r.authGrants, id)
		}
	}
	for id, ex := range r.challenges {
		if ex.UserID == userID && containsPurpose(in.PurgeChallengePurposes, ex.Purpose) {
			delete(r.challenges, id)
		}
	}
	return passwordreset.RedeemResult{UserID: userID}, nil
}

func containsPurpose(ps []string, want string) bool {
	for _, p := range ps {
		if p == want {
			return true
		}
	}
	return false
}

// --- contactchange.Repository ---

// contactChangeRepo keys pending changes by (user, kind) so Create is an atomic
// replace-per-pair and Consume is single-use get-and-delete regardless of expiry —
// all under the shared store mutex (design §2.4).
type contactChangeRepo struct{ *data }

func contactChangeKey(userID string, kind identifier.Kind) string {
	return userID + "\x00" + string(kind)
}

func (r contactChangeRepo) Create(_ context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Empty ID → mimic a schema default (the greenfield DB-generated convention).
	if p.ID == "" {
		p.ID = ids.MustGenerate()
	}
	// Replace-per-(user, kind): the composite key overwrites any prior pending row.
	r.contactChanges[contactChangeKey(p.UserID, p.Kind)] = p
	return p, nil
}

// Consume is get-and-delete: the row is removed regardless of expiry, so an
// expired Consume deletes and reports ErrExpired, and any second Consume →
// ErrNotFound (design §2.4's pinned contract, the oauthstate.Consume precedent).
func (r contactChangeRepo) Consume(_ context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := contactChangeKey(userID, kind)
	p, ok := r.contactChanges[key]
	if !ok {
		return contactchange.PendingChange{}, sdk.ErrNotFound
	}
	delete(r.contactChanges, key)
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}

// --- authgrant.Repository ---

// authGrantRepo keys grants by ID and hand-enforces the single-use, session-bound
// consume: the atomic operation matches (session, purpose, context) among
// unconsumed rows, decides expiry, and marks the row consumed — so a second
// consume, a context mismatch, and an expired grant all behave as the port
// promises.
type authGrantRepo struct{ *data }

func (r authGrantRepo) Create(_ context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g.ID == "" {
		g.ID = ids.MustGenerate()
	}
	r.authGrants[g.ID] = g
	return g, nil
}

func (r authGrantRepo) Consume(_ context.Context, sessionID, purpose, contextDigest string, now time.Time) (authgrant.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, g := range r.authGrants {
		if g.Consumed() || g.SessionID != sessionID || g.Purpose != purpose || g.ContextDigest != contextDigest {
			continue
		}
		g.ConsumedAt = now.UTC() // single-use: mark before returning
		r.authGrants[id] = g
		if g.Expired(now) {
			return authgrant.Grant{}, sdk.ErrExpired
		}
		return g, nil
	}
	return authgrant.Grant{}, sdk.ErrNotFound
}

func (r authGrantRepo) DeleteBySession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, g := range r.authGrants {
		if g.SessionID == sessionID {
			delete(r.authGrants, id)
		}
	}
	return nil
}

// --- credential.MutationRepository ---

// credentialMutationRepo projects a user's MethodSet from the credential source
// tables the store already holds — the password map, the oauth links, and the
// credential-projection identifiers stand-in — plus the per-user auth_revision,
// which authmem anchors on the user row (its single-anchor model). Apply performs
// one revision-CAS mutation inside a single mutex-held critical section: it
// rejects a stale revision, mutates exactly the targeted typed source, and
// increments the revision exactly once, so a concurrent double-apply produces
// exactly one winner and never a partial mutation (design §5.6). The policy is NOT
// run here — it is the service's job before Apply; this port only serializes.
type credentialMutationRepo struct{ *data }

// credentialUserExistsLocked reports whether the user has any credential state;
// callers hold d.mu.
func (r credentialMutationRepo) credentialUserExistsLocked(userID string) bool {
	if _, ok := r.users[userID]; ok {
		return true
	}
	// A user proven only by its credential state (password/oauth/identifiers) still
	// counts, so a seed that skips Users.Create is snapshottable.
	if r.passwords[userID] != "" {
		return true
	}
	for _, it := range r.identifiers {
		if it.Active() && it.UserID == userID {
			return true
		}
	}
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			return true
		}
	}
	return false
}

// credentialRevisionLocked reads the user's optimistic revision off the user row
// (auth_revision rides the user row); callers hold d.mu.
func (r credentialMutationRepo) credentialRevisionLocked(userID string) int64 {
	return r.users[userID].AuthRevision
}

func (r credentialMutationRepo) snapshotLocked(userID string) credential.MethodSet {
	set := credential.MethodSet{
		AuthRevision: r.credentialRevisionLocked(userID),
		HasPassword:  r.passwords[userID] != "",
	}
	for _, a := range r.oauthAccounts {
		if a.UserID == userID {
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: a.Provider, Assurance: session.AssuranceAAL1})
		}
	}
	// Identifiers project from the authoritative identifier rows — the SAME active
	// rows the identifier rail (identifierRepo) writes at registration and add/change
	// (design §5.6), so the masked inventory and the credential policy read exactly
	// what a pgx/turso store projects from user_identifiers. A registered or added
	// identifier therefore appears in the inventory immediately.
	for _, it := range r.identifiers {
		if it.Active() && it.UserID == userID {
			set.Identifiers = append(set.Identifiers, credential.IdentifierMethod{
				ID:       it.ID,
				Kind:     string(it.Kind),
				Uses:     credential.IdentifierUses{Login: it.LoginEnabled, Recovery: it.RecoveryEnabled, Notification: it.NotificationEnabled},
				Verified: !it.VerifiedAt.IsZero(),
				Primary:  it.IsPrimary,
			})
		}
	}
	return set
}

func (r credentialMutationRepo) Snapshot(_ context.Context, userID string) (credential.MethodSet, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.credentialUserExistsLocked(userID) {
		return credential.MethodSet{}, sdk.ErrNotFound
	}
	return r.snapshotLocked(userID), nil
}

func (r credentialMutationRepo) Apply(_ context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.credentialUserExistsLocked(userID) {
		return sdk.ErrNotFound
	}
	if r.credentialRevisionLocked(userID) != expectedAuthRevision {
		return sdk.ErrConflict
	}
	switch m := mutation.(type) {
	case credential.RemovePassword:
		delete(r.passwords, userID)
	case credential.UnlinkOAuth:
		kept := r.oauthAccounts[:0:0]
		for _, a := range r.oauthAccounts {
			if a.UserID == userID && a.Provider == m.Provider {
				continue
			}
			kept = append(kept, a)
		}
		r.oauthAccounts = kept
	case credential.RetireIdentifier:
		r.retireIdentifierRowLocked(userID, m.IdentifierID, m.ReplacementPrimaryID)
	case credential.ChangeIdentifierUses:
		r.changeIdentifierUsesRowLocked(userID, m.IdentifierID, m.Uses, m.MakePrimary)
	}
	// auth_revision rides the user row: bump it there when the row exists.
	if u, ok := r.users[userID]; ok {
		u.AuthRevision = expectedAuthRevision + 1
		r.users[userID] = u
	}
	return nil
}

// retireIdentifierRowLocked retires the credential-mutation-rail identifier on the
// authoritative identifier rows (design §5.6): the targeted row is retired and, when
// it was primary, the named replacement is promoted. Callers hold d.mu. It mutates
// the same rows the identifier rail owns so the credential and identifier views never
// disagree.
func (r credentialMutationRepo) retireIdentifierRowLocked(userID, id, replacementPrimaryID string) {
	now := time.Now().UTC()
	for rowID, it := range r.identifiers {
		if it.UserID != userID {
			continue
		}
		if it.ID == id {
			it.Retire(now)
			r.identifiers[rowID] = it
		} else if replacementPrimaryID != "" && it.ID == replacementPrimaryID {
			it.IsPrimary = true
			it.UpdatedAt = now
			r.identifiers[rowID] = it
		}
	}
}

// changeIdentifierUsesRowLocked applies a uses/primary change to the authoritative
// identifier rows (design §5.6). When MakePrimary is set the target is promoted and
// any other active same-kind primary is demoted, mirroring the identifier rail's
// one-active-primary-per-(user,kind) invariant. Callers hold d.mu.
func (r credentialMutationRepo) changeIdentifierUsesRowLocked(userID, id string, uses credential.IdentifierUses, makePrimary bool) {
	now := time.Now().UTC()
	target, ok := r.activeIdentifierByIDLocked(userID, id)
	if !ok {
		return
	}
	if makePrimary {
		for rowID, it := range r.identifiers {
			if it.Active() && it.UserID == userID && it.Kind == target.Kind && it.IsPrimary && it.ID != id {
				it.IsPrimary = false
				it.UpdatedAt = now
				r.identifiers[rowID] = it
			}
		}
	}
	target.LoginEnabled = uses.Login
	target.RecoveryEnabled = uses.Recovery
	target.NotificationEnabled = uses.Notification
	if makePrimary {
		target.IsPrimary = true
	}
	target.UpdatedAt = now
	r.identifiers[target.ID] = target
}

// activeIdentifierByIDLocked returns the caller's active identifier row by id; callers
// hold d.mu.
func (r credentialMutationRepo) activeIdentifierByIDLocked(userID, id string) (identifier.Identifier, bool) {
	it, ok := r.identifiers[id]
	if !ok || !it.Active() || it.UserID != userID {
		return identifier.Identifier{}, false
	}
	return it, true
}
