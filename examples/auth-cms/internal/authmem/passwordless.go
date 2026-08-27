package authmem

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// The atomic magic-link redemption (CHAU-6.2). It is the host-store twin of the
// storetest reference implementation, held to the same shared conformance suite:
// one critical section, a snapshot before any mutation, and an explicit restore on
// every rejection so a refused redemption leaves the store byte-identical.

// --- passwordless.Repository ---

// passwordlessRepo is this host store's atomic magic-link redemption (CHAU-6.2),
// held to the SAME shared conformance suite as the two SQL implementations: one
// critical section, a snapshot taken before
// any mutation, and an explicit restore on every rejection path so a refused
// redemption leaves the store byte-identical — including the token, which stays
// redeemable after an INFRASTRUCTURE failure but is consumed by a committed
// outcome.
type passwordlessRepo struct{ *data }

// passwordlessSnap copies every collection the redemption can mutate.
type passwordlessSnap struct {
	users       map[string]user.User
	identifiers map[string]identifier.Identifier
	passwords   map[string]string
	sessions    map[string]session.Session
	grants      map[string]authgrant.Grant
	challenges  map[string]challenge.Challenge
}

func (r passwordlessRepo) snapshot() passwordlessSnap {
	snap := passwordlessSnap{
		users:       make(map[string]user.User, len(r.users)),
		identifiers: make(map[string]identifier.Identifier, len(r.identifiers)),
		passwords:   make(map[string]string, len(r.passwords)),
		sessions:    make(map[string]session.Session, len(r.sessions)),
		grants:      make(map[string]authgrant.Grant, len(r.authGrants)),
		challenges:  make(map[string]challenge.Challenge, len(r.challenges)),
	}
	maps.Copy(snap.users, r.users)
	maps.Copy(snap.identifiers, r.identifiers)
	maps.Copy(snap.passwords, r.passwords)
	maps.Copy(snap.sessions, r.sessions)
	maps.Copy(snap.grants, r.authGrants)
	maps.Copy(snap.challenges, r.challenges)
	return snap
}

func (r passwordlessRepo) restore(snap passwordlessSnap) {
	r.users = snap.users
	r.identifiers = snap.identifiers
	r.passwords = snap.passwords
	r.sessions = snap.sessions
	r.authGrants = snap.grants
	r.challenges = snap.challenges
}

func (r passwordlessRepo) Redeem(_ context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := r.snapshot()
	reject := func() (passwordless.RedeemResult, error) {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	if in.TokenDigest == "" {
		return reject()
	}

	// 1. Select and consume the live matching challenge. An expired row is deleted
	//    (single-use even when stale) and still rejects.
	var consumed challenge.Challenge
	found := false
	for id, ex := range r.challenges {
		if ex.Purpose == in.Purpose && ex.SecretDigest == in.TokenDigest {
			consumed, found = ex, true
			delete(r.challenges, id)
			break
		}
	}
	if !found || consumed.Expired(in.Now) {
		if !found {
			r.restore(snap) // nothing was consumed; keep the store untouched
		}
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	// 2. Decode and validate the versioned binding.
	var binding passwordless.Binding
	if err := json.Unmarshal(consumed.Context, &binding); err != nil {
		return reject()
	}
	if binding.Version != passwordless.BindingVersion || binding.NormalizedValue == "" {
		return reject()
	}

	now := in.Now.UTC()

	// 3. Re-read the CURRENT active claim for the bound address. The binding's
	//    identifier/user ids are an expectation, not an authority.
	current, hasCurrent := r.findActiveAuthClaim(identifier.Kind(binding.Kind), binding.NormalizedValue, nil)

	switch {
	case !hasCurrent:
		// 3a. Nobody claims the address.
		if !binding.ProvisionIfAbsent {
			// A login-only link whose account vanished between issue and consume.
			return reject()
		}
		return r.provisionLocked(in, binding, now, snap)

	case current.Verified():
		// 3b. A verified claim: ordinary login for its CURRENT owner, which may be a
		//     different user than the one recorded at issue (the address was
		//     registered between send and consume — the now-current owner wins).
		if !current.LoginEnabled {
			return reject()
		}
		return r.loginLocked(in, current, now, snap)

	default:
		// 3c. An UNVERIFIED claim: the click proves possession, so adopt it — after
		//     revoking everything that predates the proof.
		return r.adoptLocked(in, current, now, snap)
	}
}

// provisionLocked creates one active user and one verified primary identifier,
// then inserts the session. Callers hold r.mu.
func (r passwordlessRepo) provisionLocked(in passwordless.RedeemInput, binding passwordless.Binding, now time.Time, snap passwordlessSnap) (passwordless.RedeemResult, error) {
	u := in.NewUser
	if u.ID == "" {
		u.ID = ids.MustGenerate()
	}
	u.Status = user.NormalizeStatus(u.Status)
	if !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	u.CreatedAt, u.UpdatedAt = now, now

	ident := in.NewIdentifier
	if ident.ID == "" {
		ident.ID = ids.MustGenerate()
	}
	ident.UserID = u.ID
	ident.NormalizedValue = binding.NormalizedValue
	ident.Kind = identifier.Kind(binding.Kind)
	ident.VerifiedAt = now
	ident.CreatedAt, ident.UpdatedAt = now, now

	// The authentication claim must still be free at commit time.
	if _, taken := r.findActiveAuthClaim(ident.Kind, ident.NormalizedValue, nil); taken {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	r.users[u.ID] = u
	r.identifiers[ident.ID] = ident

	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:     passwordless.OutcomeProvisionNew,
		User:        u,
		Identifier:  ident,
		Session:     sess,
		Provisioned: true,
	}, nil
}

// loginLocked mints a session for the identifier's current owner.
func (r passwordlessRepo) loginLocked(in passwordless.RedeemInput, ident identifier.Identifier, _ time.Time, snap passwordlessSnap) (passwordless.RedeemResult, error) {
	u, ok := r.users[ident.UserID]
	if !ok || !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:    passwordless.OutcomeLoginExistingVerified,
		User:       u,
		Identifier: ident,
		Session:    sess,
	}, nil
}

// adoptLocked verifies the previously-unverified identifier and revokes every
// credential that predates the proof, BEFORE inserting the session. The ordering
// is the invariant: a completed adoption cannot leave a squatter credential alive,
// because the session is created only after the revocation.
func (r passwordlessRepo) adoptLocked(in passwordless.RedeemInput, ident identifier.Identifier, now time.Time, snap passwordlessSnap) (passwordless.RedeemResult, error) {
	u, ok := r.users[ident.UserID]
	if !ok || !u.Active() {
		r.restore(snap)
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}

	// Revoke first: password, sessions, grants, and the listed challenge purposes.
	delete(r.passwords, u.ID)
	for sid, sv := range r.sessions {
		if sv.UserID == u.ID {
			delete(r.sessions, sid)
		}
	}
	for gid, g := range r.authGrants {
		if g.UserID == u.ID {
			delete(r.authGrants, gid)
		}
	}
	for cid, c := range r.challenges {
		if c.UserID != u.ID {
			continue
		}
		if slices.Contains(in.RevokeChallengePurposes, c.Purpose) {
			delete(r.challenges, cid)
		}
	}

	// Then verify and adopt the identifier, and bump the revision so any in-flight
	// credential mutation loses its CAS.
	ident.VerifiedAt = now
	ident.LoginEnabled = in.AdoptedIdentifierUses.Login
	ident.RecoveryEnabled = in.AdoptedIdentifierUses.Recovery
	ident.NotificationEnabled = in.AdoptedIdentifierUses.Notification
	ident.UpdatedAt = now
	r.identifiers[ident.ID] = ident

	u.AuthRevision++
	u.UpdatedAt = now
	r.users[u.ID] = u

	sess, err := r.insertSessionLocked(in.Session, u.ID, snap)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:    passwordless.OutcomeVerifyAndAdoptExistingUnverified,
		User:       u,
		Identifier: ident,
		Session:    sess,
	}, nil
}

// insertSessionLocked applies the ordinary session uniqueness contract inside the
// transaction. A collision rolls the whole redemption back.
func (r passwordlessRepo) insertSessionLocked(proposed session.Session, userID string, snap passwordlessSnap) (session.Session, error) {
	proposed.UserID = userID
	if proposed.ID == "" {
		proposed.ID = ids.MustGenerate()
	}
	for _, ex := range r.sessions {
		if ex.RefreshTokenHash == proposed.RefreshTokenHash {
			r.restore(snap)
			return session.Session{}, sdk.ErrAlreadyExists
		}
	}
	r.sessions[proposed.ID] = proposed
	return proposed, nil
}
