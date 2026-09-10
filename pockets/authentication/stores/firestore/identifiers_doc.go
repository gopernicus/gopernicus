package firestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// The user_identifiers collection and BOTH of its claim collections are owned
// here: putIdentifier and updateIdentifier are the only writers, and
// ownership_test.go refuses any other non-test file to name the three
// collections.
//
// The reason is the one A-D3 states for the authorization store and SCHEMA.md
// §5 restates here: an identifier row carries TWO partial unique indexes that
// Firestore cannot express, so each is reproduced by a claim document beside the
// row, and the failure mode that would break them silently is a future write
// path that changes the row and forgets a claim. Uniqueness would then be
// enforced by nothing at all, and nothing at READ time would notice — the
// duplicate would surface months later as two accounts logging in with one
// address. So the row and its claims move together, always, through this pair.
//
// The two stored predicates, verbatim from the migrations:
//
//	idx_user_identifiers_auth_claim  (kind, normalized_value)
//	    WHERE replaced_at IS NULL AND (login_enabled = 1 OR recovery_enabled = 1)
//	idx_user_identifiers_primary     (user_id, kind)
//	    WHERE replaced_at IS NULL AND is_primary = 1
//
// A NOTIFICATION-ONLY address therefore takes no authentication claim, which is
// exactly what lets a shared household phone be notification-only on many
// accounts while identifying at most one login or recovery subject.
//
// Neither helper READS. That is deliberate and load-bearing: a Firestore
// transaction refuses any read issued after its first write, so the CALLER owns
// the complete read phase and only then calls these.

// The user_identifiers field paths the queries filter and order on.
const (
	fieldIdentifierUserID    = "user_id"
	fieldIdentifierActive    = "active"
	fieldIdentifierCreatedAt = "created_at"
	fieldIdentifierID        = "id"
)

// identifierRef is the row's document — the KeyHash of its primary key.
func identifierRef(db *firestoredb.DB, identifierID string) *gcfs.DocumentRef {
	return db.Doc(collectionIdentifiers, identifierDocID(identifierID))
}

// authClaimRef is the authentication claim for (kind, normalized value). It is
// also GetLogin/GetRecovery's ACCESS PATH: reading it by id resolves the address
// without an equality filter on unbounded address text, whose index entry would
// truncate past 1500 bytes and could then match a DIFFERENT address (SCHEMA.md
// §4.2).
func authClaimRef(db *firestoredb.DB, kind, normalizedValue string) *gcfs.DocumentRef {
	return db.Doc(collectionIdentifierClaims, identifierClaimDocID(kind, normalizedValue))
}

// primaryClaimRef is the active-primary claim for (user, kind). It is also how a
// primary switch finds the row it must demote without a query, and how the
// directory projection is recomputed in O(1).
func primaryClaimRef(db *firestoredb.DB, userID, kind string) *gcfs.DocumentRef {
	return db.Doc(collectionIdentifierPrimaries, identifierPrimaryDocID(userID, kind))
}

// activeIdentifiersQuery is ListByUser's population: the user's rows that have
// not been replaced, in the SQL adapters' `ORDER BY created_at, id`.
func activeIdentifiersQuery(db *firestoredb.DB, userID string) gcfs.Query {
	return db.Collection(collectionIdentifiers).
		Where(fieldIdentifierUserID, "==", userID).
		Where(fieldIdentifierActive, "==", true).
		OrderBy(fieldIdentifierCreatedAt, gcfs.Asc).
		OrderBy(fieldIdentifierID, gcfs.Asc)
}

// newIdentifierDoc builds the document for an identifier being CREATED, minting
// the id when the caller left it empty (the greenfield cryptids.Database
// convention the SQL adapters serve with `RETURNING id`) and linking it to
// userID, which the atomic create only learns inside its own transaction.
//
// It is called INSIDE the transaction callback, so a retried attempt mints a
// fresh id rather than reusing one a losing attempt claimed.
func newIdentifierDoc(ident identifier.Identifier, userID string) identifierDoc {
	id := ident.ID
	if id == "" {
		id = firestoredb.NewID()
	}
	row := identifierDoc{
		ID:                  id,
		UserID:              userID,
		Kind:                string(ident.Kind),
		NormalizedValue:     ident.NormalizedValue,
		VerifiedAt:          firestoredb.NullTime(ident.VerifiedAt),
		LoginEnabled:        ident.LoginEnabled,
		RecoveryEnabled:     ident.RecoveryEnabled,
		NotificationEnabled: ident.NotificationEnabled,
		IsPrimary:           ident.IsPrimary,
		CreatedAt:           firestoredb.TruncateTime(ident.CreatedAt),
		UpdatedAt:           firestoredb.TruncateTime(ident.UpdatedAt),
	}
	row.setReplacedAt(ident.ReplacedAt)
	return row
}

// setReplacedAt writes the retirement timestamp AND the derived `active` flag
// together. active is the indexable spelling of `replaced_at IS NULL` — the
// leading conjunct of both partial unique indexes and of every active-only read
// — so the two must never be written separately (SCHEMA.md §4.1).
func (d *identifierDoc) setReplacedAt(t time.Time) {
	d.ReplacedAt = firestoredb.NullTime(t)
	d.Active = t.IsZero()
}

// retired returns the row as of its retirement: replaced_at and updated_at
// stamped, active cleared. Retirement is history-preserving — the row stays and
// Get still returns it — which is why it is a field change rather than a delete.
func (d identifierDoc) retired(now time.Time) identifierDoc {
	next := d
	next.UpdatedAt = firestoredb.TruncateTime(now)
	next.setReplacedAt(now)
	return next
}

// claimsAuth reports the STORED predicate of idx_user_identifiers_auth_claim.
func (d identifierDoc) claimsAuth() bool {
	return d.Active && (d.LoginEnabled || d.RecoveryEnabled)
}

// claimsPrimary reports the STORED predicate of idx_user_identifiers_primary.
func (d identifierDoc) claimsPrimary() bool {
	return d.Active && d.IsPrimary
}

// putIdentifier writes a NEW identifier row and records the claims its stored
// state takes. The verb is Create, never Set: the document id IS the primary
// key, so a host that supplies a duplicate id loses at the server rather than
// overwriting a live address.
//
// The claims are recorded in plan rather than written here so that a claim this
// same transaction released — a replaced address re-taken at the same value, a
// promoted primary taking the demoted row's key — is written ONCE (see
// claims.go). An untouched claim is Created, which is what makes a lost race
// sdk.ErrAlreadyExists (ruling R3).
func putIdentifier(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row identifierDoc) error {
	takeIdentifierClaims(db, plan, row)
	return w.Create(ctx, identifierRef(db, row.ID), row)
}

// updateIdentifier rewrites an EXISTING identifier row and moves its claims to
// match the new stored state: retirement, a use change that crosses the
// login/recovery predicate, a primary demotion or promotion, or a change of the
// address itself. Every transition releases what the OLD state claimed and takes
// what the NEW state claims, in this one transaction, so a claim can never
// outlive the predicate that justified it.
func updateIdentifier(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, old, next identifierDoc) error {
	if old.claimsAuth() {
		plan.release(authClaimRef(db, old.Kind, old.NormalizedValue))
	}
	if old.claimsPrimary() {
		plan.release(primaryClaimRef(db, old.UserID, old.Kind))
	}
	takeIdentifierClaims(db, plan, next)
	return w.Set(ctx, identifierRef(db, next.ID), next)
}

// takeIdentifierClaims records whichever claims row's stored state satisfies.
func takeIdentifierClaims(db *firestoredb.DB, plan *claimPlan, row identifierDoc) {
	docID := identifierDocID(row.ID)
	if row.claimsAuth() {
		plan.take(authClaimRef(db, row.Kind, row.NormalizedValue), identifierClaimDoc{
			DocID:        docID,
			IdentifierID: row.ID,
			UserID:       row.UserID,
		})
	}
	if row.claimsPrimary() {
		plan.take(primaryClaimRef(db, row.UserID, row.Kind), identifierPrimaryDoc{
			DocID:        docID,
			IdentifierID: row.ID,
		})
	}
}

// readIdentifier reads one row through r. An absent document is sdk.ErrNotFound,
// which is Get's port contract.
func readIdentifier(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, identifierID string) (identifierDoc, error) {
	snap, err := r.Get(ctx, identifierRef(db, identifierID))
	if err != nil {
		return identifierDoc{}, err
	}
	return decodeIdentifier(snap)
}

// readIdentifierByAuthClaim resolves an address through its authentication
// claim: the claim document names the row, and the row is then read and its
// SPECIFIC use flag checked by the caller — the claim covers the UNION of login
// and recovery, so it answers "is this address claimed", not "for which use".
//
// Absence at either hop is sdk.ErrNotFound, matching the SQL adapters' empty
// result for `WHERE kind=? AND normalized_value=? AND replaced_at IS NULL AND
// login_enabled=1`.
func readIdentifierByAuthClaim(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, kind, normalizedValue string) (identifierDoc, error) {
	snap, err := r.Get(ctx, authClaimRef(db, kind, normalizedValue))
	if err != nil {
		return identifierDoc{}, err
	}
	var claim identifierClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return identifierDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionIdentifierClaims, err, sdk.ErrInvalidInput)
	}
	return readIdentifier(ctx, db, r, claim.IdentifierID)
}

// readPrimaryIdentifier resolves a user's ACTIVE PRIMARY row of a kind through
// the primary claim — one document read instead of a query, and the seam the
// directory projection is recomputed from. An absent claim is (zero, false, nil):
// having no primary of a kind is a normal state, not an error.
func readPrimaryIdentifier(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, userID, kind string) (identifierDoc, bool, error) {
	snap, err := r.Get(ctx, primaryClaimRef(db, userID, kind))
	if errors.Is(err, sdk.ErrNotFound) {
		return identifierDoc{}, false, nil
	}
	if err != nil {
		return identifierDoc{}, false, err
	}
	var claim identifierPrimaryDoc
	if err := snap.DataTo(&claim); err != nil {
		return identifierDoc{}, false, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionIdentifierPrimaries, err, sdk.ErrInvalidInput)
	}
	row, err := readIdentifier(ctx, db, r, claim.IdentifierID)
	if errors.Is(err, sdk.ErrNotFound) {
		return identifierDoc{}, false, nil
	}
	if err != nil {
		return identifierDoc{}, false, err
	}
	return row, true, nil
}

// queryIdentifiers runs one identifier query and decodes every document it
// returns, consuming iterator.Done as the loop terminator and mapping every
// other Next error HERE, at the iteration boundary.
func queryIdentifiers(ctx context.Context, r firestoredb.Reader, q gcfs.Query) ([]identifierDoc, error) {
	it := r.Documents(ctx, q)
	defer it.Stop()

	var out []identifierDoc
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, firestoredb.MapError(err)
		}
		row, err := decodeIdentifier(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
}

// decodeIdentifier turns one snapshot into an identifier document.
func decodeIdentifier(snap *gcfs.DocumentSnapshot) (identifierDoc, error) {
	var row identifierDoc
	if err := snap.DataTo(&row); err != nil {
		return identifierDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionIdentifiers, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// verifiedAt reads the nullable proof TIME. Its zero value is the domain's
// "unverified" sentinel.
func (d identifierDoc) verifiedAt() (time.Time, error) {
	return firestoredb.ParseNullTime(d.VerifiedAt)
}

// toDomain projects the document onto the domain entity.
func (d identifierDoc) toDomain() (identifier.Identifier, error) {
	verifiedAt, err := d.verifiedAt()
	if err != nil {
		return identifier.Identifier{}, err
	}
	replacedAt, err := firestoredb.ParseNullTime(d.ReplacedAt)
	if err != nil {
		return identifier.Identifier{}, err
	}
	return identifier.Identifier{
		ID:                  d.ID,
		UserID:              d.UserID,
		Kind:                identifier.Kind(d.Kind),
		NormalizedValue:     d.NormalizedValue,
		VerifiedAt:          verifiedAt,
		LoginEnabled:        d.LoginEnabled,
		RecoveryEnabled:     d.RecoveryEnabled,
		NotificationEnabled: d.NotificationEnabled,
		IsPrimary:           d.IsPrimary,
		CreatedAt:           d.CreatedAt,
		UpdatedAt:           d.UpdatedAt,
		ReplacedAt:          replacedAt,
	}, nil
}
