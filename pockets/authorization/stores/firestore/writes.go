package firestore

import (
	"context"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// writesPerTuple is how many documents one relationship tuple owns: the row
// plus its two claims (SCHEMA.md §5). It sizes read-phase collections; it is
// NOT a budget.
//
// There is no per-transaction WRITE COUNT ceiling to budget against. The
// Firestore quotas page bounds a commit by the 10 MiB maximum API request size
// and by 500 field transformations per document — neither of which is a count
// of writes — so this store enforces no count of its own. An oversized commit
// fails at the server, ATOMICALLY: nothing is written, the error is mapped, and
// the caller splits the work itself. Inventing a client-side refusal here would
// reject batches Firestore accepts (SCHEMA.md §8.1).
const writesPerTuple = 3

// errTargetRelationConflict is the reconciliation's stable domain refusal: a
// desired target already holds a DIFFERENT relation on the resource, which the
// one-relation-per-subject claim forbids. It wraps sdk.ErrConflict, which is
// what the port and the conformance suite check
// (specSetRelationTargetsConflictRollsBack), and it is named so the contention
// retry can tell it apart from a lost race: re-running it would re-read the
// same claim and refuse again, so it is TERMINAL (retryableConflict).
var errTargetRelationConflict = fmt.Errorf("authorization firestore store: a desired target already holds a different relation on the resource: %w", sdk.ErrConflict)

// docRefs collects document references for one transaction's READ phase,
// de-duplicated by path. A Firestore transaction must issue every read before
// its first write, and a batch commonly names the same document twice (two rows
// for one subject, a repeated relationship_id); reading it once keeps the
// GetAll small and its result unambiguous.
type docRefs struct {
	refs  []*gcfs.DocumentRef
	index map[string]int
	snaps []*gcfs.DocumentSnapshot
}

func newDocRefs(size int) *docRefs {
	return &docRefs{index: make(map[string]int, size)}
}

// add records ref for the read phase and returns its path, the key exists takes.
func (d *docRefs) add(ref *gcfs.DocumentRef) string {
	if _, ok := d.index[ref.Path]; !ok {
		d.index[ref.Path] = len(d.refs)
		d.refs = append(d.refs, ref)
	}
	return ref.Path
}

// read performs the ONE GetAll this phase needs. An empty ref set reads nothing.
func (d *docRefs) read(ctx context.Context, r firestoredb.Reader) error {
	if len(d.refs) == 0 {
		return nil
	}
	snaps, err := r.GetAll(ctx, d.refs)
	if err != nil {
		return err
	}
	d.snaps = snaps
	return nil
}

// exists reports whether the document at path was present at the transaction's
// read timestamp. A missing document is NOT an error in GetAll — it comes back
// as a snapshot whose Exists() is false — so absence is a value here.
func (d *docRefs) exists(path string) bool {
	i, ok := d.index[path]
	if !ok || i >= len(d.snaps) {
		return false
	}
	snap := d.snaps[i]
	return snap != nil && snap.Exists()
}

// subjectClaimAt decodes the subject claim read at path, reporting whether it
// was present. It is how the reconciliation learns that a desired target
// already holds a DIFFERENT relation on the resource without a second query.
func (d *docRefs) subjectClaimAt(path string) (subjectClaimDoc, bool, error) {
	if !d.exists(path) {
		return subjectClaimDoc{}, false, nil
	}
	claim, err := decodeSubjectClaim(d.snaps[d.index[path]])
	if err != nil {
		return subjectClaimDoc{}, false, err
	}
	return claim, true, nil
}

// newRow builds the document for an incoming tuple, minting the relationship_id
// when the caller left it empty (the DDL DEFAULT's mirror — the conformance
// suite's DBGeneratedIDOnEmpty case). It is called INSIDE the transaction
// callback, so a retried attempt mints fresh ids rather than reusing the ones a
// losing attempt claimed.
func newRow(c relationship.CreateRelationship, now time.Time) relationshipDoc {
	id := c.RelationshipID
	if id == "" {
		id = firestoredb.NewID()
	}
	return relationshipDoc{
		RelationshipID:  id,
		ResourceType:    c.ResourceType,
		ResourceID:      c.ResourceID,
		Relation:        c.Relation,
		SubjectType:     c.SubjectType,
		SubjectID:       c.SubjectID,
		SubjectRelation: c.SubjectRelation,
		CreatedAt:       now,
	}
}

// createRelationships is CreateRelationships' transaction body: ONE read phase
// over every document the batch could collide with, then the writes for the
// rows that survive. It is called inside db.Transact and may run MORE THAN ONCE
// (the vendor retries a commit that lost a race), so every piece of state it
// uses is built here rather than passed in.
//
// The no-op rule is the SQL adapters' bare `ON CONFLICT DO NOTHING`, which has
// no conflict target and therefore covers EVERY unique constraint on the table.
// A row is silently skipped — nil error, existing row untouched, its id and
// created_at preserved — when any of the three is already taken:
//
//   - the exact tuple exists (the unique-tuple index);
//   - the subject already holds SOME relation on the resource, the same one or
//     a different one (the unique-subject index, which excludes relation);
//   - the relationship_id is already in use (the primary key, SCHEMA.md §5.3).
//
// Duplicates WITHIN the batch resolve the same way, in INPUT ORDER: the first
// row claiming a subject or an id wins and the later ones are skipped, which is
// what a multi-row INSERT with DO NOTHING does.
func createRelationships(ctx context.Context, db *firestoredb.DB, in []relationship.CreateRelationship) error {
	now := time.Now().UTC()
	rows := make([]relationshipDoc, len(in))
	paths := make([][3]string, len(in))
	refs := newDocRefs(len(in) * writesPerTuple)
	for i, c := range in {
		rows[i] = newRow(c, now)
		tuple, subject, id := claimRefs(db, rows[i])
		paths[i] = [3]string{refs.add(tuple), refs.add(subject), refs.add(id)}
	}
	if err := refs.read(ctx, db.ReaderFrom(ctx)); err != nil {
		return err
	}

	w := db.WriterFrom(ctx)
	claimed := make(map[string]struct{}, len(in)*2)
	for i, row := range rows {
		if refs.exists(paths[i][0]) || refs.exists(paths[i][1]) || refs.exists(paths[i][2]) {
			continue
		}
		if _, taken := claimed[paths[i][1]]; taken {
			continue
		}
		if _, taken := claimed[paths[i][2]]; taken {
			continue
		}
		claimed[paths[i][1]] = struct{}{}
		claimed[paths[i][2]] = struct{}{}
		if err := putTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	return nil
}

// desiredTargets folds SetRelationTargets' input into the distinct desired
// subject references, IN INPUT ORDER, after checking every row names the
// requested resource and relation (the memstore's and turso's same refusal, so
// an engine bug is caught identically on all three families). Input order makes
// the write sequence deterministic, which a hash-map iteration would not.
func desiredTargets(resourceType, resourceID, relationName string, in []relationship.CreateRelationship) ([]relationship.CreateRelationship, error) {
	out := make([]relationship.CreateRelationship, 0, len(in))
	seen := make(map[relationship.SubjectRef]struct{}, len(in))
	for _, c := range in {
		if c.ResourceType != resourceType || c.ResourceID != resourceID || c.Relation != relationName {
			return nil, fmt.Errorf("authorization firestore store: SetRelationTargets row is outside requested scope: %w", sdk.ErrInvalidInput)
		}
		if _, dup := seen[c.Subject()]; dup {
			continue
		}
		seen[c.Subject()] = struct{}{}
		out = append(out, c)
	}
	return out, nil
}

// setRelationTargets is SetRelationTargets' transaction body: read the current
// targets, read the claims of the ones that are missing, then delete the
// surplus and create the missing. Reads strictly precede writes, and every
// attempt rebuilds its own state — the vendor re-runs this callback when a
// commit loses a race.
//
// That re-run is exactly what makes concurrent desired states CONVERGE instead
// of unioning: the loser's commit is aborted because the winner wrote a
// document its query covered, and the retry reads the winner's row as surplus
// and removes it. The final state is one caller's desired set, never the union
// of both.
//
// A desired target that already holds a DIFFERENT relation on the resource
// makes the requested state impossible under the one-relation-per-subject rule,
// so the callback returns sdk.ErrConflict and the transaction rolls back with
// nothing changed. The claim document answers that question without a second
// query: its Relation field IS the relation the subject holds.
func setRelationTargets(ctx context.Context, db *firestoredb.DB, resourceType, resourceID, relationName string, desired []relationship.CreateRelationship) error {
	r := db.ReaderFrom(ctx)
	current, err := queryRelationships(ctx, r, db.Collection(collectionRelationships).
		Where("resource_key", "==", resourceKey(resourceType, resourceID)).
		Where("relation", "==", relationName))
	if err != nil {
		return err
	}

	keep := make(map[relationship.SubjectRef]struct{}, len(desired))
	for _, c := range desired {
		keep[c.Subject()] = struct{}{}
	}
	var surplus []relationshipDoc
	present := make(map[relationship.SubjectRef]struct{}, len(current))
	for _, row := range current {
		ref := relationship.SubjectRef{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation}
		if _, wanted := keep[ref]; wanted {
			present[ref] = struct{}{}
			continue
		}
		surplus = append(surplus, row)
	}

	now := time.Now().UTC()
	var missing []relationshipDoc
	for _, c := range desired {
		if _, ok := present[c.Subject()]; !ok {
			missing = append(missing, newRow(c, now))
		}
	}

	refs := newDocRefs(len(missing) * 2)
	paths := make([][2]string, len(missing))
	for i, row := range missing {
		_, subject, id := claimRefs(db, row)
		paths[i] = [2]string{refs.add(subject), refs.add(id)}
	}
	if err := refs.read(ctx, r); err != nil {
		return err
	}
	for i, row := range missing {
		claim, ok, err := refs.subjectClaimAt(paths[i][0])
		if err != nil {
			return err
		}
		if ok && claim.Relation != relationName {
			return fmt.Errorf("authorization firestore store: target %s already holds relation %q on %s:%s: %w",
				relationship.SubjectRef{Type: row.SubjectType, ID: row.SubjectID, Relation: row.SubjectRelation},
				claim.Relation, resourceType, resourceID, errTargetRelationConflict)
		}
	}

	w := db.WriterFrom(ctx)
	for _, row := range surplus {
		if err := dropTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	for i, row := range missing {
		// A relationship_id already in use is the SQL family's silent no-op
		// (the bare DO NOTHING covers the primary key too), so the row is
		// skipped rather than failing the reconciliation. Engine-minted and
		// store-minted ids make this unreachable in practice; a caller
		// supplying its own id is the case it exists for.
		if refs.exists(paths[i][1]) {
			continue
		}
		if err := putTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	return nil
}

// dropMatching is the delete family's ONE body: inside a transaction, read the
// resource's tuples through query, keep the ones match accepts, and drop each
// through dropTuple so the row and BOTH claims go together. Nothing matching is
// nil — every delete on this port is idempotent.
//
// The subject-level predicates are applied in Go rather than as query filters
// on purpose: the reads are already scoped to one resource (and often one
// relation), which is a bounded population, and every delete therefore rides
// the two index shapes the store already needs — resource_key alone, and
// (resource_key, relation) — instead of adding a composite per delete variant.
func dropMatching(ctx context.Context, db *firestoredb.DB, query gcfs.Query, match func(relationshipDoc) bool) error {
	rows, err := queryRelationships(ctx, db.ReaderFrom(ctx), query)
	if err != nil {
		return err
	}
	matched := rows[:0:0]
	for _, row := range rows {
		if match == nil || match(row) {
			matched = append(matched, row)
		}
	}
	if len(matched) == 0 {
		return nil
	}
	w := db.WriterFrom(ctx)
	for _, row := range matched {
		if err := dropTuple(ctx, db, w, row); err != nil {
			return err
		}
	}
	return nil
}
