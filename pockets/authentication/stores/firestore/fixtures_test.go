//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// The white-box fixtures for the tasks the shared conformance suite cannot
// reach from the port surface: the CLAIM DOCUMENTS themselves (no port returns
// one), the stored-predicate transitions that only a use change crosses, and the
// document-read cost of a directory page.

// testBase is these tests' fixed reference instant, offset from the conformance
// suite's so a stray fixture collision would be obvious rather than plausible.
var testBase = time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)

// dbGenerated is the greenfield id strategy: the domain mints nothing and the
// store assigns the key.
var dbGenerated = cryptids.NewGenerator(cryptids.Database)

// normalizer is the pocket's bundled address normalizer.
var normalizer = identifier.DefaultNormalizer{}

// openRepos opens this train's emulator database, clears it, and returns the
// repository set together with the handle, so a test can also read the claim
// documents the ports deliberately do not expose.
//
// Callers bind the repository set to `r`, matching portcalls_test.go. That is
// not stylistic: guard G24's receiver heuristic recognizes a mediated call by
// the RECEIVER's spelling, so a port method literally named Get/Create/Update
// reads as unmediated vendor I/O under any other name. The guard's own comment
// prescribes the rename rather than a wider regex.
func openRepos(t *testing.T) (auth.Repositories, *firestoredb.DB) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	r, err := Repositories(db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	return r, db
}

// seedUser creates a user whose first identifier is the given address, with the
// given uses and verification. It is the port's own atomic create, so the
// fixture exercises the same write path the suite does.
func seedUser(t *testing.T, r auth.Repositories, kind identifier.Kind, value string, uses identifier.Uses, primary bool, verifiedAt, createdAt time.Time) (user.User, identifier.Identifier) {
	t.Helper()

	u := user.NewUser(dbGenerated, "Fixture", createdAt)
	u.CreatedAt, u.UpdatedAt = createdAt.UTC(), createdAt.UTC()
	ident, err := identifier.New(dbGenerated, normalizer, "", kind, value, uses, primary, verifiedAt, createdAt)
	if err != nil {
		t.Fatalf("identifier.New(%q): %v", value, err)
	}
	created, createdIdent, err := r.Users.CreateWithPrimaryIdentifier(context.Background(), u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier(%q): %v", value, err)
	}
	return created, createdIdent
}

// readRow reads one identifier row as it is STORED, which is what a claim
// predicate is evaluated against.
func readRow(t *testing.T, db *firestoredb.DB, identifierID string) identifierDoc {
	t.Helper()
	ctx := context.Background()
	row, err := readIdentifier(ctx, db, db.ReaderFrom(ctx), identifierID)
	if err != nil {
		t.Fatalf("readIdentifier(%q): %v", identifierID, err)
	}
	return row
}

// applyIdentifierUpdate drives updateIdentifier the way a port method does: one
// transaction, the row written and its claims moved together. It is how the
// stored-predicate edges are exercised before the ports that cross them exist
// (CredentialMutations.ChangeIdentifierUses lands at N2b).
func applyIdentifierUpdate(t *testing.T, db *firestoredb.DB, old, next identifierDoc) {
	t.Helper()
	err := db.Transact(context.Background(), func(ctx context.Context) error {
		w := db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := updateIdentifier(ctx, db, w, plan, old, next); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		t.Fatalf("updateIdentifier: %v", err)
	}
}

// authClaimOwner reports which identifier holds the authentication claim on
// (kind, value), if any.
func authClaimOwner(t *testing.T, db *firestoredb.DB, kind, value string) (identifierClaimDoc, bool) {
	t.Helper()
	var claim identifierClaimDoc
	if !readClaim(t, db, authClaimRef(db, kind, value), &claim) {
		return identifierClaimDoc{}, false
	}
	return claim, true
}

// primaryClaimOwner reports which identifier holds the active-primary claim for
// (user, kind), if any.
func primaryClaimOwner(t *testing.T, db *firestoredb.DB, userID, kind string) (identifierPrimaryDoc, bool) {
	t.Helper()
	var claim identifierPrimaryDoc
	if !readClaim(t, db, primaryClaimRef(db, userID, kind), &claim) {
		return identifierPrimaryDoc{}, false
	}
	return claim, true
}

// readClaim decodes a claim document into out, reporting whether it exists. An
// absent claim is a VALUE here — "this key is unclaimed" is the state most of
// these assertions are about.
func readClaim(t *testing.T, db *firestoredb.DB, ref *gcfs.DocumentRef, out any) bool {
	t.Helper()
	ctx := context.Background()
	snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if errors.Is(err, sdk.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("reading claim %s: %v", ref.Path, err)
	}
	if err := snap.DataTo(out); err != nil {
		t.Fatalf("decoding claim %s: %v", ref.Path, err)
	}
	return true
}

// storedProjection reads the two directory-projection fields straight off the
// users document, so a test asserts what is PERSISTED rather than what a
// projection happens to render.
func storedProjection(t *testing.T, db *firestoredb.DB, userID string) (string, bool) {
	t.Helper()
	ctx := context.Background()
	row, err := readUser(ctx, db, db.ReaderFrom(ctx), userID)
	if err != nil {
		t.Fatalf("readUser(%q): %v", userID, err)
	}
	summary, err := row.summary()
	if err != nil {
		t.Fatalf("summary(%q): %v", userID, err)
	}
	return summary.PrimaryEmail, summary.EmailVerified
}

// countingReader decorates a Reader and counts the document reads a call makes.
// The Documents counter drains a DUPLICATE of the query, so the count reflects
// what Firestore would bill without disturbing the iterator the caller receives.
type countingReader struct {
	reader    firestoredb.Reader
	queries   int
	documents int
	gets      int
}

var _ firestoredb.Reader = (*countingReader)(nil)

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

func (c *countingReader) Count(ctx context.Context, q gcfs.Query) (int64, error) {
	return c.reader.Count(ctx, q)
}

// countDocuments drains a duplicate of q and reports how many documents it
// returns. An error ends the count rather than failing: the real iterator the
// caller receives reports it, and that is where it belongs.
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
