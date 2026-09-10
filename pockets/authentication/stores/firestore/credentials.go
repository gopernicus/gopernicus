package firestore

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ credential.MutationRepository = (*credentialMutationStore)(nil)

// credentialMutationStore fills credential.MutationRepository: the
// revision-serialized rail over users, user_passwords, oauth_accounts, and
// user_identifiers plus every claim and the directory projection those touch.
//
// The port's whole purpose is that a policy decision taken against a SNAPSHOT is
// applied only while that snapshot still holds: Snapshot reads the typed method
// set together with users.auth_revision, the service evaluates its policy, and
// Apply refuses with sdk.ErrConflict if the revision moved. This store makes the
// refusal exact by reading the users document inside the mutation's own
// transaction — that read is also the CONTENTION set, so two Applies at the same
// expected revision genuinely serialize on one document and exactly one commits.
//
// The mutation is never partially applied. Every kind's row change, its claim
// movements, the recomputed directory projection, and the single auth_revision
// increment are one commit.
type credentialMutationStore struct {
	db *firestoredb.DB
}

func newCredentialMutationStore(db *firestoredb.DB) *credentialMutationStore {
	return &credentialMutationStore{db: db}
}

// Snapshot projects the user's typed MethodSet and the auth_revision it was read
// at, under ONE snapshot so the revision and the credentials cannot disagree.
//
// A read-only transaction is what makes that guarantee real rather than
// aspirational: the four reads below are four round trips, and without a shared
// snapshot a mutation landing between them could return a revision that already
// counts a password the projection still shows. The caller would then evaluate
// policy against a state that never existed and Apply at a revision that lets it
// through.
//
// An unknown user is sdk.ErrNotFound, from the users read.
func (s *credentialMutationStore) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	if err := refuseAmbient(ctx); err != nil {
		return credential.MethodSet{}, err
	}

	var set credential.MethodSet
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		set = credential.MethodSet{}

		userRow, err := readUser(ctx, s.db, r, userID)
		if err != nil {
			return err
		}
		set.AuthRevision = userRow.AuthRevision

		// The password is EXISTENCE, not material: this port never returns the
		// hash, and the SQL adapters ask `SELECT EXISTS (…)` for the same reason.
		switch _, err := readPassword(ctx, s.db, r, userID); {
		case err == nil:
			set.HasPassword = true
		case errors.Is(err, sdk.ErrNotFound):
		default:
			return err
		}

		links, err := queryOAuthAccounts(ctx, r, oauthAccountsForUserQuery(s.db, userID))
		if err != nil {
			return err
		}
		// The SQL adapters order this projection by provider. Firestore would
		// need its own (user_id, provider) composite to serve that ordering, so
		// the store reuses ListByUser's existing index and sorts the handful of
		// links in Go: the set is a per-user inventory, not a page, and the
		// order is contractual only in that the two families agree.
		slices.SortStableFunc(links, func(a, b oauthAccountDoc) int { return cmp.Compare(a.Provider, b.Provider) })
		for _, link := range links {
			// Ordinary OAuth is a direct login method at AAL1 (design §12.1);
			// the honest level rides the descriptor so a stronger host policy
			// can reason over it.
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: link.Provider, Assurance: session.AssuranceAAL1})
		}

		rows, err := queryIdentifiers(ctx, r, activeIdentifiersQuery(s.db, userID))
		if err != nil {
			return err
		}
		for _, row := range rows {
			verifiedAt, err := row.verifiedAt()
			if err != nil {
				return err
			}
			set.Identifiers = append(set.Identifiers, credential.IdentifierMethod{
				ID:   row.ID,
				Kind: row.Kind,
				Uses: credential.IdentifierUses{
					Login:        row.LoginEnabled,
					Recovery:     row.RecoveryEnabled,
					Notification: row.NotificationEnabled,
				},
				Verified: !verifiedAt.IsZero(),
				Primary:  row.IsPrimary,
			})
		}
		return nil
	})
	if err != nil {
		return credential.MethodSet{}, err
	}
	return set, nil
}

// Apply performs one revision-CAS typed mutation atomically, incrementing
// auth_revision exactly once. A stale revision is sdk.ErrConflict with nothing
// written; the mutation is never partially applied.
//
// Sentinels: stale revision → sdk.ErrConflict (errStaleAuthRevision, which the
// contention loop classifies as TERMINAL — re-reading would find the same
// advanced revision, so retrying a deterministic refusal would only spin);
// unknown user → sdk.ErrNotFound; a mutation that would take an authentication
// or primary claim another row holds → sdk.ErrAlreadyExists with the whole
// operation rolled back, which is what the SQL adapters' partial unique indexes
// report.
//
// Absent targets are NO-OPS that still advance the revision, exactly as the SQL
// `DELETE … WHERE user_id = ?` / `UPDATE … WHERE id = ?` behind each kind
// affects zero rows without failing: removing a password the user does not have,
// unlinking a provider that is not linked, or naming an identifier that does not
// exist are all successful applications of a mutation with nothing to change.
func (s *credentialMutationStore) Apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	now := time.Now()
	return retryTransact(ctx, s.db, func(ctx context.Context) error {
		return s.apply(ctx, userID, expectedAuthRevision, mutation, now)
	})
}

// apply is ONE attempt: the revision CAS, the typed mutation's complete read
// phase, and then its writes. It holds no state across attempts — everything it
// decides comes from documents read in THIS attempt, which is what makes a
// re-run after a lost commit race safe.
func (s *credentialMutationStore) apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation, now time.Time) error {
	r := s.db.ReaderFrom(ctx)

	userRow, err := readUser(ctx, s.db, r, userID)
	if err != nil {
		return err
	}
	if userRow.AuthRevision != expectedAuthRevision {
		return errStaleAuthRevision
	}

	// READ PHASE — every document the typed mutation touches, before the first
	// write, because the vendor refuses a read issued after one.
	staged, err := s.stage(ctx, r, userRow, mutation, now)
	if err != nil {
		return err
	}

	// WRITE PHASE. Nothing below reads.
	w := s.db.WriterFrom(ctx)
	plan := newClaimPlan()
	projection, err := staged.write(ctx, s.db, w, plan, userRow)
	if err != nil {
		return err
	}
	if err := advanceUserRevision(ctx, s.db, w, userRow.ID, userRow.AuthRevision+1, now, projection); err != nil {
		return err
	}
	return plan.commit(ctx, w)
}

// stage performs the READ half of one typed mutation and records exactly what
// the write half will change. The switch is exhaustive over the sealed Mutation
// sum type; an unknown variant cannot exist, so an unrecognized value stages
// nothing and the operation degenerates to the revision increment — the same
// answer the SQL adapters' identical switch gives.
func (s *credentialMutationStore) stage(ctx context.Context, r firestoredb.Reader, userRow userDoc, mutation credential.Mutation, now time.Time) (credentialWrites, error) {
	var staged credentialWrites
	switch m := mutation.(type) {
	case credential.RemovePassword:
		// No read: the credential document's id is derived from the user id and
		// an unconditional Delete is idempotent, so reading it first would only
		// widen the lock set (the posture N2a recorded for taking a free claim).
		staged.removePassword = true

	case credential.UnlinkOAuth:
		// This one MUST read: the link's document id is h(provider,
		// provider_user_id) and the caller supplies only (user, provider), so
		// the match set has to be resolved before it can be deleted.
		links, err := queryOAuthAccounts(ctx, r, oauthAccountsForUserProviderQuery(s.db, userRow.ID, m.Provider))
		if err != nil {
			return credentialWrites{}, err
		}
		staged.oauthLinks = links

	case credential.RetireIdentifier:
		retired, hasRetired, err := readOptionalIdentifier(ctx, s.db, r, m.IdentifierID)
		if err != nil {
			return credentialWrites{}, err
		}
		promoted, hasPromoted, err := readOptionalIdentifier(ctx, s.db, r, m.ReplacementPrimaryID)
		if err != nil {
			return credentialWrites{}, err
		}
		if err := staged.readProjectionInput(ctx, s.db, r, userRow.ID); err != nil {
			return credentialWrites{}, err
		}
		// An already-retired row is a no-op, matching the SQL
		// `WHERE id = ? AND replaced_at IS NULL` that affects no row.
		if hasRetired && retired.Active {
			staged.stageIdentifier(retired, retireRow(now))
		}
		// The promotion is UNCONDITIONAL, as in the SQL: it does not demote
		// anything, because the row it replaces is the one being retired. If a
		// DIFFERENT row still holds the (user, kind) primary claim, this
		// promotion collides with it at commit and the whole mutation rolls back
		// as sdk.ErrAlreadyExists — which is exactly what the partial unique
		// index does to the equivalent UPDATE.
		if hasPromoted {
			staged.stageIdentifier(promoted, promoteRow(now))
		}

	case credential.ChangeIdentifierUses:
		target, hasTarget, err := readOptionalIdentifier(ctx, s.db, r, m.IdentifierID)
		if err != nil {
			return credentialWrites{}, err
		}
		if err := staged.readProjectionInput(ctx, s.db, r, userRow.ID); err != nil {
			return credentialWrites{}, err
		}
		if !hasTarget {
			break
		}
		if m.MakePrimary {
			// Promotion DEMOTES the current primary of the SAME KIND, which the
			// primary claim names in one read rather than a query. Demotion is
			// not retirement: the displaced address stays active and keeps its
			// authentication claim; only its primary flag moves.
			displaced, hasDisplaced := staged.currentEmail, staged.hasCurrentEmail
			if target.Kind != string(identifier.KindEmail) {
				displaced, hasDisplaced, err = readPrimaryIdentifier(ctx, s.db, r, userRow.ID, target.Kind)
				if err != nil {
					return credentialWrites{}, err
				}
			}
			if hasDisplaced && displaced.ID != target.ID {
				staged.stageIdentifier(displaced, demoteRow(now))
			}
		}
		staged.stageIdentifier(target, changeUsesRow(m.Uses, m.MakePrimary, now))
	}
	return staged, nil
}

// credentialWrites is one mutation's staged write phase: the documents read in
// the read phase, in the form the write phase needs them. It holds no reader and
// issues no read of its own, so a caller cannot accidentally read after a write
// by going through it.
type credentialWrites struct {
	removePassword bool
	oauthLinks     []oauthAccountDoc
	identifiers    []identifierChange

	// currentEmail is the user's ACTIVE PRIMARY email as READ — the directory
	// projection's only input (SCHEMA.md §6) and, for a promotion of another
	// email, the row being displaced. It is read only by the kinds that can
	// change the projection.
	currentEmail    identifierDoc
	hasCurrentEmail bool
}

// identifierChange is one row's transition: the state as READ and the state to
// be written. updateIdentifier needs both, because the claims the old state held
// are what it must release.
type identifierChange struct {
	old  identifierDoc
	next identifierDoc
}

// readProjectionInput reads the active primary EMAIL row, which every
// identifier-touching mutation needs in order to RECOMPUTE the directory
// projection rather than assume it (SCHEMA.md §6.3 rows 6–8). Having no primary
// email is a normal state, not an error.
func (c *credentialWrites) readProjectionInput(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID string) error {
	row, ok, err := readPrimaryIdentifier(ctx, db, r, userID, string(identifier.KindEmail))
	if err != nil {
		return err
	}
	c.currentEmail, c.hasCurrentEmail = row, ok
	return nil
}

// stageIdentifier records a row transition. A row staged TWICE — retiring the
// identifier that is also the named replacement primary — keeps its original
// as-read state as old and applies the second change on top of the first next,
// so one document takes exactly one write. Two writes to one document in one
// commit is not a stronger change; it is a document whose final state depends on
// write ordering.
func (c *credentialWrites) stageIdentifier(row identifierDoc, change func(identifierDoc) identifierDoc) {
	for i := range c.identifiers {
		if c.identifiers[i].old.ID == row.ID {
			c.identifiers[i].next = change(c.identifiers[i].next)
			return
		}
	}
	c.identifiers = append(c.identifiers, identifierChange{old: row, next: change(row)})
}

// write emits the staged mutation and returns the directory projection the users
// document must carry afterwards.
//
// A mutation that touches NO identifier returns the STORED pair unchanged
// (SCHEMA.md §6.3 row 9): advanceUserRevision always writes both projection
// fields, so passing anything else — the zero value in particular — would blank
// the operator directory on a password removal.
func (c credentialWrites) write(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, userRow userDoc) (emailProjection, error) {
	if c.removePassword {
		if err := dropPassword(ctx, db, w, userRow.ID); err != nil {
			return emailProjection{}, err
		}
	}
	if err := dropOAuthAccounts(ctx, db, w, c.oauthLinks); err != nil {
		return emailProjection{}, err
	}
	if len(c.identifiers) == 0 {
		return projectionOfUser(userRow), nil
	}

	// The projection is recomputed from every row this transaction has in hand,
	// in precedence order — as read, then as written — so a retirement overrides
	// the row's pre-change state and a primary email that nothing replaces
	// CLEARS both fields.
	candidates := make([]identifierDoc, 0, len(c.identifiers)+1)
	if c.hasCurrentEmail {
		candidates = append(candidates, c.currentEmail)
	}
	for _, change := range c.identifiers {
		if err := updateIdentifier(ctx, db, w, plan, change.old, change.next); err != nil {
			return emailProjection{}, err
		}
		candidates = append(candidates, change.next)
	}
	return resolveEmailProjection(candidates...)
}

// The row transitions, one per mutation kind. Each takes a row as it will be
// written and returns the row as it must become; none of them writes, so a
// transition can be composed onto another (stageIdentifier does exactly that).

// retireRow is RetireIdentifier's history-preserving retirement: replaced_at and
// updated_at stamped, the derived active flag cleared, and the row still
// readable by id.
func retireRow(now time.Time) func(identifierDoc) identifierDoc {
	return func(row identifierDoc) identifierDoc { return row.retired(now) }
}

// promoteRow makes a row primary. It is the SQL `SET is_primary = 1`.
func promoteRow(now time.Time) func(identifierDoc) identifierDoc {
	return func(row identifierDoc) identifierDoc {
		row.IsPrimary = true
		row.UpdatedAt = firestoredb.TruncateTime(now)
		return row
	}
}

// demoteRow clears a row's primary flag, leaving it ACTIVE and leaving its
// authentication claim alone. It is the SQL `SET is_primary = 0` a promotion
// runs against the displaced row of the same kind.
func demoteRow(now time.Time) func(identifierDoc) identifierDoc {
	return func(row identifierDoc) identifierDoc {
		row.IsPrimary = false
		row.UpdatedAt = firestoredb.TruncateTime(now)
		return row
	}
}

// changeUsesRow writes the three use flags and, when the mutation promotes,
// the primary flag with them. Crossing the login/recovery predicate is what
// takes or releases the AUTHENTICATION claim — updateIdentifier derives that
// from the row's stored state, so this transition only has to state the state.
func changeUsesRow(uses credential.IdentifierUses, makePrimary bool, now time.Time) func(identifierDoc) identifierDoc {
	return func(row identifierDoc) identifierDoc {
		row.LoginEnabled = uses.Login
		row.RecoveryEnabled = uses.Recovery
		row.NotificationEnabled = uses.Notification
		if makePrimary {
			row.IsPrimary = true
		}
		row.UpdatedAt = firestoredb.TruncateTime(now)
		return row
	}
}
