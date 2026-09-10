//go:build integration && !live

package firestore

import (
	"context"
	"fmt"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

// The A2a/A2b fixtures. The read side lands before the write side, so these
// tests cannot seed through CreateRelationships (A2c). They seed through
// putTuple — the SAME private helper the write path will use — so a fixture can
// never disagree with a real row about derived keys, claims, or timestamps, and
// so the read tests exercise the document shape SCHEMA.md pins rather than one
// invented for testing.

// newRelationships opens this train's emulator database, clears it, and returns
// a fresh relationship store over it.
func newRelationships(t *testing.T) (*firestoredb.DB, *relationshipStore) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	return db, newRelationshipStore(db)
}

// ctf is the storetest fixture constructor's local twin: a concrete-subject tuple.
func ctf(rt, rid, relation, st, sid string) relationship.CreateRelationship {
	return relationship.CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: relation, SubjectType: st, SubjectID: sid}
}

// ctfUserset is ctf for a USERSET subject (subject_relation non-empty), the edge
// group expansion actually walks.
func ctfUserset(rt, rid, relation, st, sid, subjectRelation string) relationship.CreateRelationship {
	c := ctf(rt, rid, relation, st, sid)
	c.SubjectRelation = subjectRelation
	return c
}

// seedTuples writes tuples through putTuple and returns the rows written, so a
// test can hand them straight back to dropTuples. The whole batch shares one
// timestamp, exactly as a real CreateRelationships batch does.
func seedTuples(t *testing.T, db *firestoredb.DB, tuples ...relationship.CreateRelationship) []relationshipDoc {
	t.Helper()
	ctx := context.Background()
	w := db.WriterFrom(ctx)
	now := time.Now().UTC()

	rows := make([]relationshipDoc, 0, len(tuples))
	for _, c := range tuples {
		id := c.RelationshipID
		if id == "" {
			id = firestoredb.NewID()
		}
		row := relationshipDoc{
			RelationshipID:  id,
			ResourceType:    c.ResourceType,
			ResourceID:      c.ResourceID,
			Relation:        c.Relation,
			SubjectType:     c.SubjectType,
			SubjectID:       c.SubjectID,
			SubjectRelation: c.SubjectRelation,
			CreatedAt:       now,
		}
		if err := putTuple(ctx, db, w, row); err != nil {
			t.Fatalf("seed %s:%s#%s <- %s:%s: %v", c.ResourceType, c.ResourceID, c.Relation, c.SubjectType, c.SubjectID, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// dropTuples removes seeded rows and both of their claims.
func dropTuples(t *testing.T, db *firestoredb.DB, rows ...relationshipDoc) {
	t.Helper()
	ctx := context.Background()
	w := db.WriterFrom(ctx)
	for _, row := range rows {
		if err := dropTuple(ctx, db, w, row); err != nil {
			t.Fatalf("drop %s:%s#%s: %v", row.ResourceType, row.ResourceID, row.Relation, err)
		}
	}
}

// closedDB opens a SECOND handle on the emulator database and closes it
// immediately. It is how the "no database I/O" cases are proven rather than
// asserted: a method that touches the wire through this handle fails, so a green
// really does mean the early return fired before any read. (firestoretest's
// cleanup closes it a second time and ignores the error.)
func closedDB(t *testing.T) *firestoredb.DB {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return db
}

// countingReader wraps a connector Reader and counts what an operation actually
// reads: the QUERIES issued, the DOCUMENTS those queries return, and the
// documents fetched by address. It is what makes "one read per chunk, never one
// per candidate" — and the published read budgets in README.md, "Ceilings and
// costs" — proven properties instead of claims in a comment. The Reader is an
// interface, so the counts are taken at the seam every read of this store goes
// through.
//
// documents is measured by DRAINING A DUPLICATE of each query before the real
// iterator is handed back, because *gcfs.DocumentIterator is a concrete type and
// cannot be decorated. So a counted operation issues each of its queries twice.
// That is fine for a measurement — the store's read SHAPE is what is under test
// — but the counter is not itself a cost measurement of the run it decorates.
type countingReader struct {
	reader    firestoredb.Reader
	queries   int
	documents int
	gets      int
}

func (c *countingReader) Get(ctx context.Context, ref *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error) {
	c.gets++
	return c.reader.Get(ctx, ref)
}

func (c *countingReader) GetAll(ctx context.Context, refs []*gcfs.DocumentRef) ([]*gcfs.DocumentSnapshot, error) {
	c.gets += len(refs)
	return c.reader.GetAll(ctx, refs)
}

func (c *countingReader) Documents(ctx context.Context, q gcfs.Query) *gcfs.DocumentIterator {
	c.queries++
	c.documents += countDocuments(ctx, c.reader, q)
	return c.reader.Documents(ctx, q)
}

// countDocuments drains a DUPLICATE of q and reports how many documents it
// returns — the number of document reads Firestore bills for that query, Limit
// clauses included. An error ends the count rather than failing: the real
// iterator the caller receives reports it, and that is where it belongs.
func countDocuments(ctx context.Context, r firestoredb.Reader, q gcfs.Query) int {
	it := r.Documents(ctx, q)
	defer it.Stop()

	n := 0
	for {
		if _, err := it.Next(); err != nil {
			return n
		}
		n++
	}
}

func (c *countingReader) Count(ctx context.Context, q gcfs.Query) (int64, error) {
	return c.reader.Count(ctx, q)
}

var _ firestoredb.Reader = (*countingReader)(nil)

// docID formats a zero-padded fixture id, so the byte order of the ids is the
// numeric order and a chunk boundary is easy to reason about.
func docID(prefix string, i int) string { return fmt.Sprintf("%s%04d", prefix, i) }
