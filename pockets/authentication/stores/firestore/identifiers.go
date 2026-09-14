package firestore

import (
	"context"
	"errors"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ identifier.IdentifierRepository = (*identifierStore)(nil)

// identifierStore fills identifier.IdentifierRepository over the
// user_identifiers collection and its two claim collections — the authentication
// claim and the active-primary claim (SCHEMA.md §5.1, §5.2). Every write goes
// through identifiers_doc.go's putIdentifier/updateIdentifier, so a row and its
// claims can never move apart.
type identifierStore struct {
	db *firestoredb.DB
}

func newIdentifierStore(db *firestoredb.DB) *identifierStore {
	return &identifierStore{db: db}
}

// Get returns the identifier with the given id (active or replaced — retirement
// is history-preserving), or sdk.ErrNotFound.
func (s *identifierStore) Get(ctx context.Context, id string) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	row, err := readIdentifier(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain()
}

// GetLogin returns the active login-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound. Verification is a service branch,
// not a store filter: an unverified row is returned so the caller can enforce
// the domain's §2.3 rule.
func (s *identifierStore) GetLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	return s.getClaimed(ctx, kind, normalizedValue, identifierDoc.loginUse)
}

// GetRecovery returns the active recovery-enabled identifier claiming
// (kind, normalizedValue), or sdk.ErrNotFound.
func (s *identifierStore) GetRecovery(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	return s.getClaimed(ctx, kind, normalizedValue, identifierDoc.recoveryUse)
}

// getClaimed is the shared body of the two use-scoped lookups. The AUTHENTICATION
// CLAIM is the access path: its document id is the KeyHash of
// (kind, normalized value), so the address resolves in one Get rather than
// through an equality filter on unbounded address text, whose index entry
// truncates past 1500 bytes and could then match a different address (SCHEMA.md
// §4.2). The claim covers the UNION of login and recovery, so the SPECIFIC use
// is then checked on the row — which is also why a notification-only address,
// holding no claim at all, is simply not found.
//
// The claim and the row are read under ONE snapshot: they are two documents
// describing one fact, and a concurrent replacement between the two reads would
// otherwise report an address as unclaimed while it is merely moving.
func (s *identifierStore) getClaimed(ctx context.Context, kind, normalizedValue string, use func(identifierDoc) bool) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}
	var row identifierDoc
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		found, err := readIdentifierByAuthClaim(ctx, s.db, r, kind, normalizedValue)
		if err != nil {
			return err
		}
		if !found.Active || !use(found) {
			return sdk.ErrNotFound
		}
		row = found
		return nil
	})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return row.toDomain()
}

// loginUse and recoveryUse are the per-use halves of the claim's predicate.
func (d identifierDoc) loginUse() bool    { return d.LoginEnabled }
func (d identifierDoc) recoveryUse() bool { return d.RecoveryEnabled }

// ListByUser returns the user's ACTIVE identifiers ordered (created_at, id).
// Replaced rows are history, not a normal read, and an unknown user is an empty
// slice rather than an error.
func (s *identifierStore) ListByUser(ctx context.Context, userID string) ([]identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	rows, err := queryIdentifiers(ctx, s.db.ReaderFrom(ctx), activeIdentifiersQuery(s.db, userID))
	if err != nil {
		return nil, err
	}
	out := make([]identifier.Identifier, 0, len(rows))
	for _, row := range rows {
		got, err := row.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

// ApplyVerifiedChange applies a confirmed contact change in ONE transaction: the
// auth_revision CAS, the retirement of the replaced and displaced rows with
// their claims, the new row with its claims, the recomputed directory
// projection, and the revision increment.
//
// The read phase is complete before the first write, as Firestore requires, and
// its read set is therefore also the CONTENTION set: the users document (which
// serializes concurrent mutations of one subject), the row being replaced, the
// active primary of the target kind, and the active primary EMAIL that the
// directory projects. The new value's authentication claim is deliberately NOT
// read — it is CREATED, so a lost race is sdk.ErrAlreadyExists at commit and
// nothing is written (ruling R3), which is what makes
// ConcurrentClaimArbitration resolve to exactly one winner.
//
// Sentinels: stale revision → sdk.ErrConflict (nothing applied, so the revision
// does not advance and the caller may retry at the same expected value); a lost
// claim → sdk.ErrAlreadyExists; an unknown user → sdk.ErrNotFound.
func (s *identifierStore) ApplyVerifiedChange(ctx context.Context, input identifier.ApplyVerifiedChangeInput, expectedAuthRevision int64, verifiedAt time.Time) (identifier.Identifier, error) {
	if err := refuseAmbient(ctx); err != nil {
		return identifier.Identifier{}, err
	}

	now := firestoredb.TruncateTime(verifiedAt)
	kind := string(input.Kind)

	var applied identifierDoc
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		applied = identifierDoc{}
		r := s.db.ReaderFrom(ctx)

		userRow, err := readUser(ctx, s.db, r, input.UserID)
		if err != nil {
			return err
		}
		if userRow.AuthRevision != expectedAuthRevision {
			return errStaleAuthRevision
		}

		// The row this change replaces. An absent or already-retired row is a
		// no-op, matching the SQL `UPDATE … WHERE id = ? AND replaced_at IS
		// NULL` that affects no row.
		replaced, hasReplaced, err := readOptionalIdentifier(ctx, s.db, r, input.ReplacesIdentifierID)
		if err != nil {
			return err
		}

		// The active primary EMAIL: the directory projection's only input, and
		// — when this change promotes an email — the row being displaced.
		currentEmail, hasCurrentEmail, err := readPrimaryIdentifier(ctx, s.db, r, input.UserID, string(identifier.KindEmail))
		if err != nil {
			return err
		}
		displaced, hasDisplaced := currentEmail, hasCurrentEmail
		if !input.MakePrimary {
			hasDisplaced = false
		} else if kind != string(identifier.KindEmail) {
			displaced, hasDisplaced, err = readPrimaryIdentifier(ctx, s.db, r, input.UserID, kind)
			if err != nil {
				return err
			}
		}

		// WRITE PHASE. Nothing below reads.
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()

		// The projection is recomputed from every row this transaction has in
		// hand, in precedence order — as read, then as written — so a retirement
		// overrides the row's pre-change state and an address that nothing
		// replaces CLEARS both fields.
		candidates := make([]identifierDoc, 0, 4)
		if hasCurrentEmail {
			candidates = append(candidates, currentEmail)
		}
		for _, old := range retirementSet(replaced, hasReplaced, displaced, hasDisplaced) {
			next := old.retired(now)
			if err := updateIdentifier(ctx, s.db, w, plan, old, next); err != nil {
				return err
			}
			candidates = append(candidates, next)
		}

		row := newIdentifierDoc(identifier.Identifier{
			UserID:              input.UserID,
			Kind:                input.Kind,
			NormalizedValue:     input.NormalizedValue,
			VerifiedAt:          now,
			LoginEnabled:        input.LoginEnabled,
			RecoveryEnabled:     input.RecoveryEnabled,
			NotificationEnabled: input.NotificationEnabled,
			IsPrimary:           input.MakePrimary,
			CreatedAt:           now,
			UpdatedAt:           now,
		}, input.UserID)
		if err := putIdentifier(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		candidates = append(candidates, row)

		projection, err := resolveEmailProjection(candidates...)
		if err != nil {
			return err
		}
		if err := advanceUserRevision(ctx, s.db, w, input.UserID, userRow.AuthRevision+1, now, projection); err != nil {
			return err
		}
		if err := plan.commit(ctx, w); err != nil {
			return err
		}
		applied = row
		return nil
	})
	if err != nil {
		return identifier.Identifier{}, err
	}
	return applied.toDomain()
}

// readOptionalIdentifier reads a row named by a possibly-empty id. An empty id
// and an absent document are both "nothing to do", which is what the SQL
// adapters' zero-row UPDATE says.
func readOptionalIdentifier(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, identifierID string) (identifierDoc, bool, error) {
	if identifierID == "" {
		return identifierDoc{}, false, nil
	}
	row, err := readIdentifier(ctx, db, r, identifierID)
	if errors.Is(err, sdk.ErrNotFound) {
		return identifierDoc{}, false, nil
	}
	if err != nil {
		return identifierDoc{}, false, err
	}
	return row, true, nil
}

// retirementSet is the DE-DUPLICATED list of active rows a change retires: the
// row it replaces and the primary it displaces, which are frequently the SAME
// row. Retiring one row twice would queue two writes for one document in a
// single commit, and releasing its claims twice would confuse the claim plan
// about which state is final.
func retirementSet(replaced identifierDoc, hasReplaced bool, displaced identifierDoc, hasDisplaced bool) []identifierDoc {
	out := make([]identifierDoc, 0, 2)
	if hasReplaced && replaced.Active {
		out = append(out, replaced)
	}
	if hasDisplaced && displaced.Active && (!hasReplaced || displaced.ID != replaced.ID) {
		out = append(out, displaced)
	}
	return out
}
