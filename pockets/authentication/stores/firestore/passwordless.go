package firestore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ passwordless.Repository = (*passwordlessStore)(nil)

// redemptionKind is the branch a staged redemption decided. It is decided ONCE,
// in the read phase, from the CURRENT authentication claim on the bound address
// — never from the binding's recorded ids, which are an expectation the address
// may have outlived.
type redemptionKind int

const (
	// redemptionLogin: the address is claimed by a verified, login-enabled
	// identifier, so the link logs its current owner in.
	redemptionLogin redemptionKind = iota
	// redemptionAdopt: the address is claimed but UNVERIFIED, so clicking the
	// link is the missing proof — and every credential that predates it is
	// revoked before the new session exists.
	redemptionAdopt
	// redemptionProvision: nothing claims the address and the link carried the
	// captured intent, so one account is created with the address verified.
	redemptionProvision
)

// passwordlessStore fills passwordless.Repository: the ONE-transaction
// magic-link redemption that consumes the token, decides login / adopt /
// provision, performs the identity mutation and every revocation the adoption
// branch owes, and inserts the session.
//
// Its rollback policy is the strictest in the pocket, and the two halves are
// deliberately opposite (N-D2, "committed outcomes versus rollback errors"):
//
//   - a STABLE BAD OUTCOME — an unknown, expired or replayed token, a malformed
//     or unknown-version binding, a replaced/removed/login-disabled identifier, a
//     deactivated subject, a would-be provision on a link that never carried the
//     intent — returns passwordless.ErrRedemption FROM the callback, so the
//     transaction aborts with NOTHING written and the token stays redeemable;
//   - an INFRASTRUCTURE ERROR rolls the whole transaction back INCLUDING the
//     token consumption, so a transient failure leaves the link retryable rather
//     than burning it. The connector guarantees that: a callback error rolls back
//     and every queued write is discarded.
//
// One generic sentinel for all of them is the point. Branch-specific errors
// would let a prober distinguish "this address has no account" from "this token
// is stale", which is exactly the enumeration the passwordless design forbids.
//
// Two concurrent redemptions of one link commit exactly one. Both transactions
// READ the token's digest claim and the row it names, so the two read sets
// intersect on documents the winner DELETES: the loser aborts, the vendor
// re-runs its callback, and the re-run finds the claim gone and answers
// ErrRedemption. Nothing about that depends on which branch either attempt had
// chosen.
//
// It writes seven collections it does not own and reaches every one of them
// through that collection's owner file — putUser/advanceUserRevision
// (users_doc.go), putIdentifier/updateIdentifier (identifiers_doc.go),
// dropPassword (passwords_doc.go), putSession/readSessionsForUser/
// dropSessionsForUser (sessions_doc.go), readGrantsForUser/dropAuthGrants
// (grants_doc.go), and the challenge helpers (challenges_doc.go). A composition
// composes owners, it never bypasses them (SCHEMA.md §7.2): each pair is what
// keeps a row and its claims moving together.
type passwordlessStore struct {
	db *firestoredb.DB
}

func newPasswordlessStore(db *firestoredb.DB) *passwordlessStore {
	return &passwordlessStore{db: db}
}

// Redeem executes the atomic redemption. See passwordless.Repository for the
// contract; every stable rejection is passwordless.ErrRedemption with nothing
// written.
func (s *passwordlessStore) Redeem(ctx context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	if err := refuseAmbient(ctx); err != nil {
		return passwordless.RedeemResult{}, err
	}
	// An empty digest claims nothing and can match no live challenge; the SQL
	// adapters refuse it before their statement for the same reason.
	if in.TokenDigest == "" {
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	now := in.Now.UTC()

	var out passwordless.RedeemResult
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.redeem(ctx, in, now, &out)
	})
	if err != nil {
		// A key this redemption tried to TAKE was taken by someone else between
		// its read phase and its commit — the address's authentication or
		// primary claim, the new subject's id, or the proposed session's
		// credentials. Firestore evaluates every Create precondition at COMMIT,
		// so all of them surface here rather than at the call that queued them,
		// and none of them can be told apart afterwards. Every one is a stable
		// bad outcome of THIS redemption with nothing written, which the port
		// spells one way (the turso adapter maps its own lost-claim insert to
		// the same sentinel).
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return passwordless.RedeemResult{}, passwordless.ErrRedemption
		}
		return passwordless.RedeemResult{}, err
	}
	return out, nil
}

// redeem is ONE attempt: the complete read phase, the decision, then the writes.
//
// out is RESET first. A Firestore callback may run more than once, and the
// subject an aborted attempt resolved — or provisioned — is not the subject of
// the transaction that commits.
func (s *passwordlessStore) redeem(ctx context.Context, in passwordless.RedeemInput, now time.Time, out *passwordless.RedeemResult) error {
	*out = passwordless.RedeemResult{}

	// READ PHASE — everything, before the first write, because the vendor
	// refuses any read issued after one. Reading it is also what makes it the
	// CONTENTION set, which is what serializes two simultaneous redemptions.
	staged, err := s.stage(ctx, s.db.ReaderFrom(ctx), in, now)
	if err != nil {
		return err
	}

	// WRITE PHASE. Nothing below reads: staged holds documents, not a reader.
	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()
	res, err := staged.write(ctx, s.db, w, plan, in, now)
	if err != nil {
		return err
	}
	if err := plan.commit(ctx, w); err != nil {
		return err
	}
	*out = res
	return nil
}

// stage performs the whole READ half and decides the branch, returning
// passwordless.ErrRedemption for every stable bad outcome — which aborts the
// transaction with nothing written, because this runs inside the callback.
//
// The order is the contract's own: consume-what-is-live, then decode the
// binding, then re-read the CURRENT claim. The binding's recorded IdentifierID
// and UserID are never trusted as an authority: an address may have gained an
// owner between send and click, and the owner of record at REDEMPTION time wins
// — which is what stops a stale link from provisioning a duplicate subject.
func (s *passwordlessStore) stage(ctx context.Context, r firestoredb.Reader, in passwordless.RedeemInput, now time.Time) (redemptionWrites, error) {
	token, err := readChallengeByDigestClaim(ctx, s.db, r, in.Purpose, in.TokenDigest)
	if errors.Is(err, sdk.ErrNotFound) {
		return redemptionWrites{}, passwordless.ErrRedemption
	}
	if err != nil {
		return redemptionWrites{}, err
	}
	// Unknown, replayed and EXPIRED are one answer, the way the SQL adapters'
	// `expires_at > ?` guard on the consuming DELETE folds them into one no-row
	// outcome.
	if token.expired(now) {
		return redemptionWrites{}, passwordless.ErrRedemption
	}

	binding, err := decodeBinding(token)
	if err != nil {
		return redemptionWrites{}, err
	}
	staged := redemptionWrites{token: token, binding: binding}

	current, err := readIdentifierByAuthClaim(ctx, s.db, r, binding.Kind, binding.NormalizedValue)
	switch {
	case errors.Is(err, sdk.ErrNotFound):
		// Nobody claims the address. Provisioning is allowed only by the intent
		// CAPTURED AT ISSUE: a link mailed while provisioning was disabled must
		// not create an account because the flag was flipped on before it was
		// clicked.
		if !binding.ProvisionIfAbsent {
			return redemptionWrites{}, passwordless.ErrRedemption
		}
		staged.kind = redemptionProvision
		return staged, nil
	case err != nil:
		return redemptionWrites{}, err
	}

	owner, err := s.readActiveUser(ctx, r, current.UserID)
	if err != nil {
		return redemptionWrites{}, err
	}
	staged.identifier, staged.owner = current, owner

	verifiedAt, err := current.verifiedAt()
	if err != nil {
		return redemptionWrites{}, err
	}
	if !verifiedAt.IsZero() {
		// The claim covers the UNION of login and recovery (SCHEMA.md §5.1), so
		// a verified RECOVERY-ONLY address resolves here and must still be
		// refused: it identifies its subject, it does not authenticate one.
		if !current.LoginEnabled {
			return redemptionWrites{}, passwordless.ErrRedemption
		}
		staged.kind = redemptionLogin
		return staged, nil
	}

	// ADOPTION — the anti-takeover branch. An unverified claim means someone
	// registered the address without proving it, so the read set widens to
	// everything that predates the proof: the subject's sessions (each carrying
	// the refresh claim its deletion releases), the grants it owns, and the
	// outstanding challenges of the purposes the caller names. The password
	// needs no read — its document id is derived from the user id and an
	// unconditional delete is idempotent (the posture N2b recorded for
	// RemovePassword).
	staged.kind = redemptionAdopt
	if staged.live, err = readSessionsForUser(ctx, s.db, r, owner.ID); err != nil {
		return redemptionWrites{}, err
	}
	// Grants the subject OWNS, which is exactly `WHERE user_id = ?` — the
	// statement both SQL adapters run here. The wider session-linked cascade
	// belongs to UserAdmin.SetStatus, whose SQL spells that disjunction out;
	// widening it here would be a three-family divergence.
	if staged.grants, err = readGrantsForUser(ctx, s.db, r, owner.ID); err != nil {
		return redemptionWrites{}, err
	}
	if staged.revoked, err = readChallengesForPurposes(ctx, s.db, r, owner.ID, in.RevokeChallengePurposes); err != nil {
		return redemptionWrites{}, err
	}
	return staged, nil
}

// readActiveUser reads the subject an existing claim names and refuses a missing
// or deactivated one. Both are ErrRedemption: a link is a credential, and a
// credential for a subject that cannot log in is simply not honored.
func (s *passwordlessStore) readActiveUser(ctx context.Context, r firestoredb.Reader, userID string) (userDoc, error) {
	row, err := readUser(ctx, s.db, r, userID)
	if errors.Is(err, sdk.ErrNotFound) {
		return userDoc{}, passwordless.ErrRedemption
	}
	if err != nil {
		return userDoc{}, err
	}
	if !user.NormalizeStatus(user.Status(row.Status)).Active() {
		return userDoc{}, passwordless.ErrRedemption
	}
	return row, nil
}

// decodeBinding renders the challenge row's stored context as the versioned
// magic-link binding, refusing anything it cannot read EXACTLY.
//
// An unknown version is rejected rather than guessed at because the binding is
// what carries the provisioning INTENT: misreading a future field layout could
// provision an account nobody asked to create.
func decodeBinding(row challengeDoc) (passwordless.Binding, error) {
	blob := row.binding()
	if len(blob) == 0 {
		return passwordless.Binding{}, passwordless.ErrRedemption
	}
	var binding passwordless.Binding
	if err := json.Unmarshal(blob, &binding); err != nil {
		return passwordless.Binding{}, passwordless.ErrRedemption
	}
	if binding.Version != passwordless.BindingVersion || binding.NormalizedValue == "" {
		return passwordless.Binding{}, passwordless.ErrRedemption
	}
	return binding, nil
}

// redemptionWrites is one staged redemption: the branch, and every document the
// write phase needs. It holds NO reader, so the write phase cannot read — which
// is the vendor's reads-before-writes rule made structural rather than
// remembered (the shape credentials.go established for the same reason).
type redemptionWrites struct {
	kind    redemptionKind
	token   challengeDoc
	binding passwordless.Binding

	// owner and identifier are the EXISTING subject and its claim on the bound
	// address (login and adoption only).
	owner      userDoc
	identifier identifierDoc

	// The adoption's revocation set, as read. The session slice is named for
	// the state it holds rather than for its collection: the collection's NAME
	// belongs to sessions_doc.go, and ownership_test.go enforces that.
	live    []sessionDoc
	grants  []authGrantDoc
	revoked []challengeDoc
}

// write emits every write the staged redemption owes and returns its result.
//
// The token consumption leads, for every branch, and it is enqueued through
// dropChallenges TOGETHER with the adoption's revocation set: a host may name
// the link's own purpose among the purposes an adoption revokes, and two Deletes
// for one document in one commit is not a stronger delete — Firestore refuses a
// document written twice in one transaction. dropChallenges de-duplicates by
// document, which is the same call passwordresets.go makes for the same overlap.
func (c redemptionWrites) write(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan,
	in passwordless.RedeemInput, now time.Time) (passwordless.RedeemResult, error) {

	if err := dropChallenges(ctx, db, w, plan, append([]challengeDoc{c.token}, c.revoked...)); err != nil {
		return passwordless.RedeemResult{}, err
	}
	switch c.kind {
	case redemptionProvision:
		return c.provision(ctx, db, w, plan, in, now)
	case redemptionAdopt:
		return c.adopt(ctx, db, w, plan, in, now)
	default:
		return c.login(ctx, db, w, plan, in)
	}
}

// login mints a session for the verified address's CURRENT owner. Nothing else
// changes: no revision moves, no claim moves, and the directory projection is
// untouched (SCHEMA.md §6.3 row 12).
func (c redemptionWrites) login(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan,
	in passwordless.RedeemInput) (passwordless.RedeemResult, error) {

	u, err := c.owner.user()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	ident, err := c.identifier.toDomain()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	sess, err := insertRedemptionSession(ctx, db, w, plan, in.Session, u.ID)
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

// adopt verifies the previously-unverified claim and revokes everything that
// predates the proof — the password, every session with its refresh claim, the
// grants, and the named challenge purposes with their digest claims — BEFORE the
// new session exists. The ordering is the invariant: a completed adoption cannot
// leave a squatter credential alive, and because all of it is one commit there
// is no instant at which the new session coexists with the old ones.
//
// The identifier moves through updateIdentifier, so a use change that crosses
// the login/recovery predicate carries its authentication claim with it. The
// auth_revision advance is what invalidates any in-flight credential mutation
// the squatter had staged.
func (c redemptionWrites) adopt(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan,
	in passwordless.RedeemInput, now time.Time) (passwordless.RedeemResult, error) {

	next := c.identifier
	next.VerifiedAt = firestoredb.NullTime(now)
	next.LoginEnabled = in.AdoptedIdentifierUses.Login
	next.RecoveryEnabled = in.AdoptedIdentifierUses.Recovery
	next.NotificationEnabled = in.AdoptedIdentifierUses.Notification
	next.UpdatedAt = firestoredb.TruncateTime(now)

	// The projection can only move when the ADOPTED row is the projected one.
	// Adoption changes neither the row's kind, nor its value, nor its primary
	// flag, so the only reachable change is the verification flag flipping true
	// on an active primary email (SCHEMA.md §6.3 row 11); every other shape
	// leaves the user's stored pair exactly as it was.
	projection := projectionOfUser(c.owner)
	adopted, err := projectionOf(next)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	if adopted.Address != "" {
		projection = adopted
	}

	if err := dropPassword(ctx, db, w, c.owner.ID); err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := dropAuthGrants(ctx, db, w, c.grants); err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := dropSessionsForUser(ctx, db, w, plan, c.live); err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := updateIdentifier(ctx, db, w, plan, c.identifier, next); err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := advanceUserRevision(ctx, db, w, c.owner.ID, c.owner.AuthRevision+1, now, projection); err != nil {
		return passwordless.RedeemResult{}, err
	}

	after := c.owner
	after.AuthRevision++
	after.UpdatedAt = firestoredb.TruncateTime(now)
	u, err := after.user()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	ident, err := next.toDomain()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	sess, err := insertRedemptionSession(ctx, db, w, plan, in.Session, u.ID)
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

// provision creates ONE active subject and ONE verified primary identifier for
// the bound address, with the directory projection the new row implies, and
// mints the session against it.
//
// It reads nothing and takes both claims with Create: every rule it can break is
// a document that does not yet exist, and the SERVER evaluates a Create's
// precondition at commit — so a claim lost to a concurrent registration rolls
// the whole redemption back rather than producing a second subject for one
// address (ruling R3, and the reason Redeem answers ErrRedemption for
// AlreadyExists).
//
// Both ids may be empty under the greenfield convention; they are minted HERE,
// inside the callback, so a retried attempt mints fresh ones rather than reusing
// ids a losing attempt claimed (N-D5).
func (c redemptionWrites) provision(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan,
	in passwordless.RedeemInput, now time.Time) (passwordless.RedeemResult, error) {

	u := in.NewUser
	u.Status = user.NormalizeStatus(u.Status)
	u.CreatedAt, u.UpdatedAt = now, now
	userRow := newUserDoc(u)

	ident := in.NewIdentifier
	// The bound address, not the caller's template: the binding is what the
	// link PROVED, and consuming it is the proof of possession that makes the
	// row verified on creation.
	ident.Kind = identifier.Kind(c.binding.Kind)
	ident.NormalizedValue = c.binding.NormalizedValue
	ident.VerifiedAt = now
	ident.CreatedAt, ident.UpdatedAt = now, now
	identRow := newIdentifierDoc(ident, userRow.ID)

	projection, err := resolveEmailProjection(identRow)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := putUser(ctx, db, w, userRow, projection); err != nil {
		return passwordless.RedeemResult{}, err
	}
	if err := putIdentifier(ctx, db, w, plan, identRow); err != nil {
		return passwordless.RedeemResult{}, err
	}

	created, err := userRow.user()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	createdIdent, err := identRow.toDomain()
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	sess, err := insertRedemptionSession(ctx, db, w, plan, in.Session, created.ID)
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return passwordless.RedeemResult{
		Outcome:     passwordless.OutcomeProvisionNew,
		User:        created,
		Identifier:  createdIdent,
		Session:     sess,
		Provisioned: true,
	}, nil
}

// insertRedemptionSession writes the proposed session inside the redemption
// transaction, binding it to the subject the branch resolved — which a
// provisioning redemption does not know until the user row exists. The ordinary
// uniqueness contract applies: the row and its current-hash claim are both
// Creates, so a duplicate loses at the server and the whole redemption rolls
// back.
func insertRedemptionSession(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan,
	sess session.Session, userID string) (session.Session, error) {

	sess.UserID = userID
	row, err := newSessionDoc(sess)
	if err != nil {
		return session.Session{}, err
	}
	if err := putSession(ctx, db, w, plan, row); err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
