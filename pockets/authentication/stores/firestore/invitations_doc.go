package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/sdk"
)

// The invitations collection AND both of its claim collections are owned here:
// putInvitation and updateInvitation are the only writers, and
// ownership_test.go refuses any other non-test file to name any of the three.
//
// Two claims, with two DIFFERENT predicates, which is the whole reason this file
// exists (SCHEMA.md §5.5, §5.6):
//
//   - invitation_token_hashes reproduces the total unique index on token_hash.
//     Its predicate is "the invitation exists with that hash", so it is taken at
//     create, MOVED when a resend changes the hash, and released only with the
//     row. It doubles as GetByTokenHash's access path.
//
//   - invitation_pending reproduces the PARTIAL unique index over pending rows,
//     whose tuple INCLUDES relation. Its predicate is the STORED status alone.
//     Wall-clock expiry does not change a stored status, so an expired-but-still-
//     pending invitation KEEPS its claim: the tuple frees up when UpdateStatus
//     moves the row off pending, and not one moment earlier. Reading the clock
//     to decide a claim's fate is the mistake this file is shaped to prevent —
//     it would let a second pending invite exist for a tuple SQL still considers
//     taken, and the two families would diverge silently.
//
// Neither writer READS: a Firestore transaction refuses a read issued after its
// first write, so the CALLER owns the whole read phase.

// The invitation field paths the queries filter and order on. The two derived
// keys are equality identities, never sort keys (SCHEMA.md §4.1).
const (
	fieldInvitationResourceKey = "resource_key"
	fieldInvitationSubjectKey  = "subject_key"
	fieldInvitationCreatedAt   = "created_at"
	fieldInvitationID          = "id"
)

// invitationRef is the row's document — the KeyHash of its primary key.
func invitationRef(db *firestoredb.DB, invitationID string) *gcfs.DocumentRef {
	return db.Doc(collectionInvitations, invitationDocID(invitationID))
}

// invitationTokenClaimRef is the claim on a mailed secret's hash. It doubles as
// GetByTokenHash's access path: one point read resolves the hash to its
// invitation without an equality filter on the hash field.
func invitationTokenClaimRef(db *firestoredb.DB, tokenHash string) *gcfs.DocumentRef {
	return db.Doc(collectionInvitationTokens, invitationTokenClaimDocID(tokenHash))
}

// invitationPendingClaimRef is the claim on the PENDING tuple a row occupies.
// It is derived from the row's own five columns, never from a caller's
// arguments, so a claim can only ever be released by the state that took it.
func invitationPendingClaimRef(db *firestoredb.DB, row invitationDoc) *gcfs.DocumentRef {
	id := invitationPendingClaimDocID(row.ResourceType, row.ResourceID, row.IdentifierKind, row.Identifier, row.Relation)
	return db.Doc(collectionInvitationPending, id)
}

// invitationsByResourceQuery is ListByResource's base query: the derived
// (resource_type, resource_id) equality, with the ordering left to the List
// helper.
func invitationsByResourceQuery(db *firestoredb.DB, resourceType, resourceID string) gcfs.Query {
	return db.Collection(collectionInvitations).
		Where(fieldInvitationResourceKey, "==", invitationResourceKey(resourceType, resourceID))
}

// invitationsBySubjectQuery is ListBySubject's base query: the derived
// (identifier_kind, identifier) equality. The kind is IN the key, so a value
// shared across kinds never cross-resolves — the port's load-bearing rule.
func invitationsBySubjectQuery(db *firestoredb.DB, kind, identifierValue string) gcfs.Query {
	return db.Collection(collectionInvitations).
		Where(fieldInvitationSubjectKey, "==", invitationSubjectKey(kind, identifierValue))
}

// newInvitationDoc builds the document for an invitation being CREATED, minting
// the id when the caller left it empty (the port's DB-generated-key case) and
// deriving both equality keys from the columns their listings filter on.
//
// Metadata is written through the domain's CloneMetadata, which is
// always-non-nil: a nil map would store Firestore's null and read back as a nil
// map, while the pocket's uniform contract is a non-nil empty map (the SQL
// column's '{}' default).
//
// It is called INSIDE the write path so a retried attempt mints a fresh id
// rather than reusing one a rolled-back attempt claimed (N-D5).
func newInvitationDoc(inv invitation.Invitation) invitationDoc {
	invitationID := inv.ID
	if invitationID == "" {
		invitationID = firestoredb.NewID()
	}
	return invitationDoc{
		ID:                invitationID,
		ResourceType:      inv.ResourceType,
		ResourceID:        inv.ResourceID,
		Relation:          inv.Relation,
		Identifier:        inv.Identifier,
		IdentifierKind:    inv.IdentifierKind,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		InvitedBy:         inv.InvitedBy,
		TokenHash:         inv.TokenHash,
		AutoAccept:        inv.AutoAccept,
		Status:            inv.Status,
		ExpiresAt:         firestoredb.TruncateTime(inv.ExpiresAt),
		AcceptedAt:        firestoredb.NullTime(inv.AcceptedAt),
		CreatedAt:         firestoredb.TruncateTime(inv.CreatedAt),
		UpdatedAt:         firestoredb.TruncateTime(inv.UpdatedAt),
		Metadata:          invitation.CloneMetadata(inv.Metadata),
		ResourceKey:       invitationResourceKey(inv.ResourceType, inv.ResourceID),
		SubjectKey:        invitationSubjectKey(inv.IdentifierKind, inv.Identifier),
	}
}

// putInvitation writes a NEW invitation row and records both claims it takes.
// Create for the row and for every claim: the document id IS the primary key and
// each claim id IS its unique key, so a duplicate invitation id, a reused token
// hash, or a second pending invite for one tuple all lose at the SERVER as
// sdk.ErrAlreadyExists rather than overwriting a live invite (ruling R3 — a free
// claim is taken, never read).
func putInvitation(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, row invitationDoc) error {
	takeInvitationClaims(db, plan, row)
	return w.Create(ctx, invitationRef(db, row.ID), row)
}

// updateInvitation rewrites an EXISTING invitation row and moves its claims to
// match the new STORED state: the old token claim and, when the old row was
// pending, the old pending claim are released, and whatever the next state
// claims is taken — in this same transaction, so a claim can never describe a
// state the row no longer has.
//
// The two interesting cases are both handled by that one rule. A transition off
// pending releases the tuple and takes nothing, which is what lets a NEW pending
// invitation for the same tuple succeed afterwards. A resend keeps the status
// and moves the token claim to the new hash while the pending claim is released
// and re-taken on the SAME document — a pair claimPlan collapses into one Set
// rather than a delete-then-create whose outcome would depend on write ordering.
func updateInvitation(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, plan *claimPlan, old, next invitationDoc) error {
	plan.release(invitationTokenClaimRef(db, old.TokenHash))
	if old.pending() {
		plan.release(invitationPendingClaimRef(db, old))
	}
	takeInvitationClaims(db, plan, next)
	return w.Set(ctx, invitationRef(db, next.ID), next)
}

// takeInvitationClaims records the claims row's STORED state takes: the token
// hash always, the pending tuple only while the stored status is pending.
func takeInvitationClaims(db *firestoredb.DB, plan *claimPlan, row invitationDoc) {
	docID := invitationDocID(row.ID)
	plan.take(invitationTokenClaimRef(db, row.TokenHash), invitationTokenClaimDoc{
		DocID:        docID,
		InvitationID: row.ID,
	})
	if row.pending() {
		plan.take(invitationPendingClaimRef(db, row), invitationPendingClaimDoc{
			DocID:        docID,
			InvitationID: row.ID,
		})
	}
}

// readInvitation reads one row through r. An absent document is sdk.ErrNotFound.
func readInvitation(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, invitationID string) (invitationDoc, error) {
	snap, err := r.Get(ctx, invitationRef(db, invitationID))
	if err != nil {
		return invitationDoc{}, err
	}
	return decodeInvitation(snap)
}

// readInvitationByTokenClaim resolves a mailed secret's hash through its claim:
// the claim document names the row, and the row is then read. Absence at either
// hop is sdk.ErrNotFound, matching the SQL adapters' empty result for
// `WHERE token_hash = ?`.
func readInvitationByTokenClaim(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, tokenHash string) (invitationDoc, error) {
	snap, err := r.Get(ctx, invitationTokenClaimRef(db, tokenHash))
	if err != nil {
		return invitationDoc{}, err
	}
	var claim invitationTokenClaimDoc
	if err := snap.DataTo(&claim); err != nil {
		return invitationDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionInvitationTokens, err, sdk.ErrInvalidInput)
	}
	return readInvitation(ctx, db, r, claim.InvitationID)
}

// listInvitationsByResource is ListByResource's paged query, and
// listInvitationsBySubject its subject-scoped twin. Both use the port's own
// OrderFields/DefaultOrder and the ORIGINAL id as the PK, so the keyset cursor
// is the domain's id — never the document-name hash, which sorts differently and
// means nothing to a caller.
//
// Neither declares a PostFilter, so a non-blank Search is refused with
// sdk.ErrInvalidInput by the connector's List rather than answered with an
// unfiltered page: invitation.OrderFields declares nothing searchable (R4).
func listInvitationsByResource(db *firestoredb.DB, resourceType, resourceID string) firestoredb.ListQuery[invitation.Invitation] {
	return invitationListQuery(invitationsByResourceQuery(db, resourceType, resourceID))
}

func listInvitationsBySubject(db *firestoredb.DB, kind, identifierValue string) firestoredb.ListQuery[invitation.Invitation] {
	return invitationListQuery(invitationsBySubjectQuery(db, kind, identifierValue))
}

// invitationListQuery is the shared shape of the two listings: one base query,
// the pinned (created_at, id) ordering, and one decode.
func invitationListQuery(base gcfs.Query) firestoredb.ListQuery[invitation.Invitation] {
	return firestoredb.ListQuery[invitation.Invitation]{
		Query:        base,
		OrderFields:  invitation.OrderFields,
		DefaultOrder: invitation.DefaultOrder,
		PK:           fieldInvitationID,
		Decode:       decodeInvitationDomain,
		OrderValueOf: func(row invitation.Invitation, _ string) any { return row.CreatedAt },
		PKOf:         func(row invitation.Invitation) string { return row.ID },
	}
}

// decodeInvitationDomain decodes one listed document straight to the domain
// aggregate, which is what the List helper pages over.
func decodeInvitationDomain(snap *gcfs.DocumentSnapshot) (invitation.Invitation, error) {
	row, err := decodeInvitation(snap)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain()
}

// decodeInvitation turns one snapshot into an invitation document.
func decodeInvitation(snap *gcfs.DocumentSnapshot) (invitationDoc, error) {
	var row invitationDoc
	if err := snap.DataTo(&row); err != nil {
		return invitationDoc{}, fmt.Errorf("authentication firestore store: decoding %s: %s: %w", collectionInvitations, err, sdk.ErrInvalidInput)
	}
	return row, nil
}

// pending reports whether the row's STORED status is pending — the partial
// index's predicate, and the only thing that decides whether the row holds its
// tuple. It deliberately takes no clock: see the file comment.
func (d invitationDoc) pending() bool {
	return d.Status == invitation.StatusPending
}

// applied returns the row as of a lifecycle transition. Exactly the six columns
// the SQL adapters' UPDATE touches move; everything else — including both
// derived keys, which the tuple cannot change — is carried over verbatim.
func (d invitationDoc) applied(upd invitation.StatusUpdate) invitationDoc {
	next := d
	next.Status = upd.Status
	next.TokenHash = upd.TokenHash
	next.ExpiresAt = firestoredb.TruncateTime(upd.ExpiresAt)
	next.AcceptedAt = firestoredb.NullTime(upd.AcceptedAt)
	next.ResolvedSubjectID = upd.ResolvedSubjectID
	next.UpdatedAt = firestoredb.TruncateTime(upd.UpdatedAt)
	return next
}

// toDomain projects the document onto the domain aggregate. Metadata is
// normalized to a NON-NIL map: absent, null, and empty are one fact here, and
// the pocket's round-trip contract is the empty map (the '{}' column default).
func (d invitationDoc) toDomain() (invitation.Invitation, error) {
	acceptedAt, err := firestoredb.ParseNullTime(d.AcceptedAt)
	if err != nil {
		return invitation.Invitation{}, err
	}
	metadata := d.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	return invitation.Invitation{
		ID:                d.ID,
		ResourceType:      d.ResourceType,
		ResourceID:        d.ResourceID,
		Relation:          d.Relation,
		Identifier:        d.Identifier,
		IdentifierKind:    d.IdentifierKind,
		ResolvedSubjectID: d.ResolvedSubjectID,
		InvitedBy:         d.InvitedBy,
		TokenHash:         d.TokenHash,
		AutoAccept:        d.AutoAccept,
		Status:            d.Status,
		Metadata:          metadata,
		ExpiresAt:         d.ExpiresAt,
		AcceptedAt:        acceptedAt,
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
	}, nil
}
