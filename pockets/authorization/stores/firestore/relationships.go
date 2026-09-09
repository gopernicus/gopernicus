package firestore

import (
	"context"
	"errors"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

var _ relationship.Storer = (*relationshipStore)(nil)

// relationshipStore fills relationship.Storer over the iam_relationships
// collection and its two claim collections (SCHEMA.md §5). Every method refuses
// an ambient transaction first (R1); the bodies land in A2a–A2d.
type relationshipStore struct {
	db *firestoredb.DB
}

func newRelationshipStore(db *firestoredb.DB) *relationshipStore {
	return &relationshipStore{db: db}
}

// CheckRelationWithGroupExpansion reports whether the concrete subject — or any
// EXACT userset it transitively belongs to — holds the relation on the resource.
// It is the engine-side walk of ruling R2: expand the subject, then ask whether
// the resource+relation carries a tuple whose subject is any reached state. A
// grant referencing group#admin is satisfied only by admin membership, never by
// group#member, because the reached states carry the userset relation.
//
// Every query — every expansion hop, every chunk, and the final match — runs
// inside ONE firestoredb.ReadSnapshot, so the whole check observes a single
// server-selected instant. Without that, a concurrent "revoke membership, grant
// the group access" pair could be straddled and authorize a path that never
// existed at any moment.
//
// maxExpansionStates bounds the walk exactly as the memstore bounds it:
// exceeding it returns relationship.ErrExpansionBudgetExceeded, never a deny;
// non-positive is unbounded.
func (s *relationshipStore) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	var allowed bool
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		allowed = false
		reached, err := expand(ctx, s.db, r, subjectType, subjectID, maxExpansionStates)
		if err != nil {
			return err
		}
		allowed, err = anyTupleWithSubject(ctx, s.db, r, resourceType, resourceID, relation, reached)
		return err
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}

// GetRelationTargets returns the subjects holding a relation on a resource, used
// for "through" permission traversal. It is ONE query on the derived
// resource_key, so it needs no snapshot; userset targets come back as stored
// (an empty Relation is a concrete subject).
func (s *relationshipStore) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	return relationTargets(ctx, s.db, s.db.ReaderFrom(ctx), resourceType, resourceID, relation)
}

// FilterRelation returns the DISTINCT, byte-order sorted subset of resourceIDs
// the subject holds relation on, directly or through exact userset expansion —
// the set form of CheckRelationWithGroupExpansion over ONE shared walk.
//
// The candidate set is read in chunks of maxDisjunctions ids, never one query
// per candidate, and an id list past that chunk bound is MERGED rather than
// rejected or truncated: chunking is invisible to the contract. Everything — the
// walk and every candidate chunk — runs under one snapshot. An empty candidate
// list performs NO database I/O, and a budget overflow fails the whole call
// rather than returning a short list.
func (s *relationshipStore) FilterRelation(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	ids := distinctSortedIDs(resourceIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	var out []string
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		var err error
		out, err = filterRelation(ctx, s.db, r, resourceType, ids, relation, subjectType, subjectID, maxExpansionStates)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// filterRelation is FilterRelation's snapshot-bound body: one shared expansion,
// then one query per candidate chunk. ids must be distinct and byte-sorted, so
// the output is produced in the contractual order by construction.
func filterRelation(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, resourceType string, ids []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	reached, err := expand(ctx, db, r, subjectType, subjectID, maxExpansionStates)
	if err != nil {
		return nil, err
	}
	matched := make(map[string]struct{}, len(ids))
	if err := scanCandidates(ctx, db, r, resourceType, ids, relation, func(row relationshipDoc) {
		if _, ok := reached[row.SubjectKey]; ok {
			matched[row.ResourceID] = struct{}{}
		}
	}); err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if _, ok := matched[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// RelationTargetsFor returns, for each of resourceIDs, the subjects holding
// relation on it — the set form of GetRelationTargets. An id with NO targets is
// ABSENT from the map, a duplicated input id carries one entry, userset targets
// are returned as stored, and an empty input performs no database I/O. The
// candidate set is read in chunks of maxDisjunctions ids under one snapshot and
// the chunks are merged, so chunking is invisible to the contract.
func (s *relationshipStore) RelationTargetsFor(ctx context.Context, resourceType string, resourceIDs []string, relation string) (map[string][]relationship.RelationTarget, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	ids := distinctSortedIDs(resourceIDs)
	out := make(map[string][]relationship.RelationTarget, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		clear(out)
		return scanCandidates(ctx, s.db, r, resourceType, ids, relation, func(row relationshipDoc) {
			out[row.ResourceID] = append(out[row.ResourceID], relationship.RelationTarget{
				Type:     row.SubjectType,
				ID:       row.SubjectID,
				Relation: row.SubjectRelation,
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckRelationExists reports whether an exact DIRECT tuple is present for a
// CONCRETE subject — no expansion, and a stored userset tuple with the same
// type/id does not satisfy it, which is why the probed document id carries an
// empty subject_relation. It is the cheapest read in the store: the tuple's
// document id IS the unique tuple, so this is one Get by id rather than a query.
func (s *relationshipStore) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return false, err
	}
	ref := s.db.Doc(collectionRelationships, relationshipDocID(resourceType, resourceID, relation, subjectType, subjectID, ""))
	snap, err := s.db.ReaderFrom(ctx).Get(ctx, ref)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return false, err
	}
	return snap != nil && snap.Exists(), nil
}

// CheckBatchDirect returns resourceID -> allowed for one relation across the
// requested ids, with group expansion. EVERY requested id is present in the map
// (default false). The subject is expanded ONCE and the candidates are read in
// chunks of maxDisjunctions ids under one snapshot, never one query per
// candidate; a budget overflow fails the whole call rather than returning a
// partial map, and an empty id list performs no database I/O.
func (s *relationshipStore) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) (map[string]bool, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = false
	}
	ids := distinctSortedIDs(resourceIDs)
	if len(ids) == 0 {
		return out, nil
	}
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		for id := range out {
			out[id] = false
		}
		matched, err := filterRelation(ctx, s.db, r, resourceType, ids, relation, subjectType, subjectID, maxExpansionStates)
		if err != nil {
			return err
		}
		for _, id := range matched {
			out[id] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateRelationships inserts a batch of tuples in ONE Firestore transaction:
// every document the batch could collide with is read first, then the surviving
// rows are written. There is no partial commit — the whole batch lands or none
// of it does — and the batch shares one created_at, which is what makes the
// relationship_id the load-bearing keyset tiebreak.
//
// A colliding row is a SILENT NO-OP (nil error, existing row untouched), never
// ErrAlreadyExists: the SQL siblings' bare `ON CONFLICT DO NOTHING` has no
// conflict target, so it covers the unique tuple, the one-relation-per-subject
// index, AND the primary key. See createRelationships for the three collisions
// and for how duplicates inside the batch resolve in input order.
//
// A batch past the transaction write limit is refused BEFORE the transaction
// opens (ErrTupleWriteLimit): splitting it across transactions would be a
// partially applied batch, which is the one thing this method promises not to
// be.
func (s *relationshipStore) CreateRelationships(ctx context.Context, relationships []relationship.CreateRelationship) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	if len(relationships) == 0 {
		return nil
	}
	if len(relationships) > maxTuplesPerTransaction {
		return writeLimitError("CreateRelationships", len(relationships))
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		return createRelationships(ctx, s.db, relationships)
	})
}

// SetRelationTargets makes the stored targets of one resource+relation equal the
// desired set, atomically, in ONE Firestore transaction: read the current
// targets and the missing targets' claims, then delete the surplus and create
// the missing. An empty desired set clears the relation, and repeating a desired
// state changes nothing — existing rows keep their id and created_at.
//
// Concurrent callers CONVERGE rather than union. Firestore aborts the commit
// that lost the race against a writer whose document the loser's query covered,
// the vendor re-runs this callback, and the retry sees the winner's row as
// surplus and removes it. A desired target already holding a different relation
// is sdk.ErrConflict and the transaction rolls back unchanged.
func (s *relationshipStore) SetRelationTargets(ctx context.Context, resourceType, resourceID, relation string, targets []relationship.CreateRelationship) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	desired, err := desiredTargets(resourceType, resourceID, relation, targets)
	if err != nil {
		return err
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		return setRelationTargets(ctx, s.db, resourceType, resourceID, relation, desired)
	})
}

// DeleteRelationshipTarget removes ONE exact tuple, userset relation included.
// The tuple's document id IS the six-part tuple, so the read is a single Get
// rather than a query — but it still happens inside the transaction, because the
// relationship_id claim can only be dropped by reading the row that owns it.
// An absent tuple is nil (idempotent) and writes nothing.
func (s *relationshipStore) DeleteRelationshipTarget(ctx context.Context, resourceType, resourceID, relation string, target relationship.SubjectRef) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	ref := s.db.Doc(collectionRelationships, relationshipDocID(resourceType, resourceID, relation, target.Type, target.ID, target.Relation))
	return s.db.Transact(ctx, func(ctx context.Context) error {
		snap, err := s.db.ReaderFrom(ctx).Get(ctx, ref)
		if err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return err
		}
		if snap == nil || !snap.Exists() {
			return nil
		}
		row, err := decodeRelationship(snap)
		if err != nil {
			return err
		}
		return dropTuple(ctx, s.db, s.db.WriterFrom(ctx), row)
	})
}

// DeleteResourceRelationships removes every tuple for a resource — rows and both
// claims — in ONE transaction. Idempotent: a resource with no tuples writes
// nothing and returns nil.
func (s *relationshipStore) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		return dropMatching(ctx, s.db, "DeleteResourceRelationships",
			s.db.Collection(collectionRelationships).Where("resource_key", "==", resourceKey(resourceType, resourceID)),
			nil)
	})
}

// DeleteRelationship removes the tuples a CONCRETE subject pair holds under one
// relation on a resource. It deliberately does not constrain subject_relation —
// the SQL siblings' five-column DELETE does not either — so a subject that is
// also stored as a userset on that relation loses both rows in the same
// transaction. Idempotent.
func (s *relationshipStore) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		return dropMatching(ctx, s.db, "DeleteRelationship",
			s.db.Collection(collectionRelationships).
				Where("resource_key", "==", resourceKey(resourceType, resourceID)).
				Where("relation", "==", relation),
			func(row relationshipDoc) bool {
				return row.SubjectType == subjectType && row.SubjectID == subjectID
			})
	})
}

// DeleteByResourceAndSubject removes every relation a subject holds on one
// resource, in ONE transaction. Idempotent.
func (s *relationshipStore) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	return s.db.Transact(ctx, func(ctx context.Context) error {
		return dropMatching(ctx, s.db, "DeleteByResourceAndSubject",
			s.db.Collection(collectionRelationships).Where("resource_key", "==", resourceKey(resourceType, resourceID)),
			func(row relationshipDoc) bool {
				return row.SubjectType == subjectType && row.SubjectID == subjectID
			})
	})
}

// CountByResourceAndRelation counts DIRECT tuples only — never expanded
// membership. That is the last-owner security pin, so the query is a plain
// equality pair on the derived resource_key with no expansion anywhere near it.
//
// It is ONE query, and it deliberately does NOT open a ReadSnapshot: the count
// aggregation is unavailable inside any Firestore transaction
// (firestoredb.ErrCountInTransaction), and a single query needs no snapshot to
// be self-consistent. The ambient refusal above is what guarantees ReaderFrom
// returns the client reader here.
func (s *relationshipStore) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	if err := refuseAmbient(ctx); err != nil {
		return 0, err
	}
	q := s.db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID)).
		Where("relation", "==", relation)
	n, err := s.db.ReaderFrom(ctx).Count(ctx, q)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ListRelationshipsBySubject pages the resources a subject relates to, in the
// port's contractual order (created_at DESC, relationship_id DESC by default).
// The subject is matched on its type and id ONLY — never on subject_key, which
// folds in subject_relation — so a subject's userset rows are listed beside its
// concrete ones, exactly as the SQL siblings' five-column WHERE lists them.
// Both optional filters become equality clauses.
func (s *relationshipStore) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter relationship.SubjectRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.SubjectRelationship], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[relationship.SubjectRelationship]{}, err
	}
	base := s.db.Collection(collectionRelationships).
		Where("subject_type", "==", subjectType).
		Where("subject_id", "==", subjectID)
	if filter.ResourceType != nil {
		base = base.Where("resource_type", "==", *filter.ResourceType)
	}
	if filter.Relation != nil {
		base = base.Where("relation", "==", *filter.Relation)
	}
	page, err := firestoredb.List(ctx, s.db.ReaderFrom(ctx), listRelationships(base), req)
	if err != nil {
		return crud.Page[relationship.SubjectRelationship]{}, err
	}
	return crud.MapPage(page, relationshipDoc.toSubjectRelationship), nil
}

// ListRelationshipsByResource pages the subjects related to a resource, in the
// same contractual order. The resource is ONE equality clause on the derived
// resource_key, which is the hash of exactly the (resource_type, resource_id)
// pair the SQL siblings match with two columns.
func (s *relationshipStore) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter relationship.ResourceRelationshipFilter, req crud.ListRequest) (crud.Page[relationship.ResourceRelationship], error) {
	if err := refuseAmbient(ctx); err != nil {
		return crud.Page[relationship.ResourceRelationship]{}, err
	}
	base := s.db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID))
	if filter.SubjectType != nil {
		base = base.Where("subject_type", "==", *filter.SubjectType)
	}
	if filter.Relation != nil {
		base = base.Where("relation", "==", *filter.Relation)
	}
	page, err := firestoredb.List(ctx, s.db.ReaderFrom(ctx), listRelationships(base), req)
	if err != nil {
		return crud.Page[relationship.ResourceRelationship]{}, err
	}
	return crud.MapPage(page, relationshipDoc.toResourceRelationship), nil
}

// LookupResourceIDs returns the DISTINCT resource ids where the subject holds
// any of the relations, with group expansion: raw-byte order, strictly after
// `after`, at most limit (non-positive is unbounded).
//
// The subject is expanded ONCE, and the reached states and the relations are
// both `in` filters — so the queries are chunked by their DNF PRODUCT, and the
// per-chunk streams are merged in id order with duplicates folded BEFORE the
// limit applies. The expansion and every chunk stream run under ONE snapshot, so
// a page cannot mix a pre- and post-revocation view of the membership graph.
func (s *relationshipStore) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	if len(relations) == 0 {
		return nil, nil
	}
	var out []string
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		out = nil
		reached, err := expand(ctx, s.db, r, subjectType, subjectID, 0)
		if err != nil {
			return err
		}
		var streams []*idStream
		for _, pair := range chunkProduct(distinctSortedIDs(relations), sortedKeys(reached), lookupChunkBudget) {
			q := whereAnyOf(
				whereAnyOf(s.db.Collection(collectionRelationships).Where("resource_type", "==", resourceType), "relation", pair.primary),
				"subject_key", pair.secondary)
			streams = append(streams, newIDStream(r, q, after, limit, relationshipResourceID))
		}
		out, err = mergeDistinctIDs(ctx, streams, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LookupResourceIDsByRelationTarget returns the DISTINCT resource ids whose
// relation points at any of the target ids — CONCRETE subjects only, no
// expansion — in the same keyset contract as LookupResourceIDs.
//
// The concrete-subject requirement is not a separate filter: a subject key
// hashes (type, id, subject_relation) together, so keying the targets with an
// empty subject relation is what excludes a stored userset with the same type
// and id.
func (s *relationshipStore) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	if len(targetIDs) == 0 {
		return nil, nil
	}
	var out []string
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		out = nil
		var streams []*idStream
		for _, chunk := range chunkStrings(subjectKeysOf(targetType, targetIDs), lookupChunkBudget) {
			q := whereAnyOf(s.db.Collection(collectionRelationships).
				Where("resource_type", "==", resourceType).
				Where("relation", "==", relation), "subject_key", chunk)
			streams = append(streams, newIDStream(r, q, after, limit, relationshipResourceID))
		}
		var err error
		out, err = mergeDistinctIDs(ctx, streams, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LookupDescendantResourceIDs walks the UNION of the self-referential relations
// transitively from the root ids and pages the sorted, distinct CLOSURE — after
// and limit bound the RESULT, not the work, exactly as the recursive CTE in the
// SQL siblings computes the whole closure on every call. A root is returned only
// when a cycle makes it a genuine descendant, and the walk terminates on a cycle
// because a resource already in the closure is never re-queued.
//
// The whole walk runs under ONE snapshot: a hop that observed a newer instant
// than its predecessor could return a descendant of an edge that never coexisted
// with the one that reached it.
func (s *relationshipStore) LookupDescendantResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType string, rootIDs []string, after string, limit int) ([]string, error) {
	if err := refuseAmbient(ctx); err != nil {
		return nil, err
	}
	if len(rootIDs) == 0 || len(relations) == 0 {
		return nil, nil
	}
	var out []string
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		closure, err := descendantClosure(ctx, s.db, r, resourceType, relations, subjectType, rootIDs)
		if err != nil {
			return err
		}
		out = pageIDs(closure, after, limit)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
