package firestore

import (
	"context"
	"errors"
	"testing"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// The claimPlan invariant, hermetically: ONE write per claim document, chosen by
// the document's LAST operation.
//
// These cases cannot be reached from a port. Every operation that releases and
// re-takes a claim does so on the SAME document, so the difference between "Set"
// and "Delete" is invisible from outside the transaction — until months later,
// when a claim that should have been released is still pointing at a row that no
// longer exists and the address it holds can never be used again. The plan is
// where that is decided, so it is tested here rather than inferred from a port
// case that would pass either way.

// claimRef builds a document reference without a client. claimPlan reads only
// Path (its map key) and ID (its failure messages), and recordingWriter records
// the ref it is handed, so no I/O is needed to prove which write each ordinal
// produces.
func claimRef(path string) *gcfs.DocumentRef {
	return &gcfs.DocumentRef{Path: path, ID: path}
}

// claimWrite is one write recordingWriter observed.
type claimWrite struct {
	verb string
	path string
	data any
}

// recordingWriter is a firestoredb.Writer that records instead of writing.
type recordingWriter struct {
	writes []claimWrite
}

var _ firestoredb.Writer = (*recordingWriter)(nil)

func (w *recordingWriter) Create(_ context.Context, ref *gcfs.DocumentRef, data any) error {
	w.writes = append(w.writes, claimWrite{verb: "Create", path: ref.Path, data: data})
	return nil
}

func (w *recordingWriter) Set(_ context.Context, ref *gcfs.DocumentRef, data any, _ ...gcfs.SetOption) error {
	w.writes = append(w.writes, claimWrite{verb: "Set", path: ref.Path, data: data})
	return nil
}

func (w *recordingWriter) Update(_ context.Context, ref *gcfs.DocumentRef, _ []gcfs.Update, _ ...gcfs.Precondition) error {
	w.writes = append(w.writes, claimWrite{verb: "Update", path: ref.Path})
	return nil
}

func (w *recordingWriter) Delete(_ context.Context, ref *gcfs.DocumentRef, _ ...gcfs.Precondition) error {
	w.writes = append(w.writes, claimWrite{verb: "Delete", path: ref.Path})
	return nil
}

// TestClaimPlanCommitsTheLastOperation is the invariant itself, in all four
// shapes. The two mixed orders are the ones that used to be indistinguishable.
func TestClaimPlanCommitsTheLastOperation(t *testing.T) {
	owner := identifierClaimDoc{DocID: "doc-1", IdentifierID: "ident-1", UserID: "user-1"}

	cases := []struct {
		name string
		plan func(*claimPlan, *gcfs.DocumentRef)
		verb string
		why  string
	}{
		{
			name: "take alone creates",
			plan: func(p *claimPlan, ref *gcfs.DocumentRef) { p.take(ref, owner) },
			verb: "Create",
			why:  "a claim this transaction did not release must be taken with Create, so a concurrent holder loses at commit (ruling R3)",
		},
		{
			name: "release alone deletes",
			plan: func(p *claimPlan, ref *gcfs.DocumentRef) { p.release(ref) },
			verb: "Delete",
			why:  "a row leaving the index's stored predicate releases its claim",
		},
		{
			name: "release then take sets",
			plan: func(p *claimPlan, ref *gcfs.DocumentRef) {
				p.release(ref)
				p.take(ref, owner)
			},
			verb: "Set",
			why:  "a hand-over is ONE write: a Delete plus a Create for one document in one commit is a document whose state depends on write ordering",
		},
		{
			name: "take then release deletes",
			plan: func(p *claimPlan, ref *gcfs.DocumentRef) {
				p.take(ref, owner)
				p.release(ref)
			},
			verb: "Delete",
			why:  "the LAST operation decides: a claim taken and then swept by a revocation in the same transaction must not survive it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := claimRef("identifier_claims/one")
			plan := newClaimPlan()
			tc.plan(plan, ref)

			w := &recordingWriter{}
			if err := plan.commit(context.Background(), w); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if len(w.writes) != 1 {
				t.Fatalf("commit emitted %+v, want exactly one write — %s", w.writes, tc.why)
			}
			if got := w.writes[0].verb; got != tc.verb {
				t.Errorf("commit emitted %s, want %s — %s", got, tc.verb, tc.why)
			}
			if got := w.writes[0].path; got != ref.Path {
				t.Errorf("commit wrote %s, want %s", got, ref.Path)
			}
		})
	}
}

// TestClaimPlanRefusesTwoOwnersOfOneKey is the caller-bug case: two rows
// claiming one unique key inside one transaction. There is no arbitration to
// make — that is the whole point of a unique key — so the plan refuses and the
// operation rolls back, rather than committing whichever body was queued last.
func TestClaimPlanRefusesTwoOwnersOfOneKey(t *testing.T) {
	ref := claimRef("identifier_claims/contended")
	plan := newClaimPlan()
	plan.take(ref, identifierClaimDoc{DocID: "doc-1", IdentifierID: "ident-1", UserID: "user-1"})
	plan.take(ref, identifierClaimDoc{DocID: "doc-2", IdentifierID: "ident-2", UserID: "user-2"})

	w := &recordingWriter{}
	err := plan.commit(context.Background(), w)
	if err == nil {
		t.Fatal("commit accepted one claim document taken by two different owners — the second body would silently win")
	}
	if len(w.writes) != 0 {
		t.Errorf("commit wrote %+v before refusing — a refused plan writes nothing", w.writes)
	}
}

// TestClaimPlanCollapsesIdenticalTakes keeps the refusal above from firing on
// the legitimate case it sits next to: a row re-taking the claim it already
// holds, which replacement and use changes both do.
func TestClaimPlanCollapsesIdenticalTakes(t *testing.T) {
	ref := claimRef("identifier_claims/stable")
	owner := identifierClaimDoc{DocID: "doc-1", IdentifierID: "ident-1", UserID: "user-1"}

	plan := newClaimPlan()
	plan.release(ref)
	plan.take(ref, owner)
	plan.take(ref, owner)

	w := &recordingWriter{}
	if err := plan.commit(context.Background(), w); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(w.writes) != 1 || w.writes[0].verb != "Set" {
		t.Fatalf("commit emitted %+v, want one Set — two takes of one key by the SAME owner are one take", w.writes)
	}
}

// TestClaimPlanWritesEachDocumentOnce covers the plan's other job: several
// claims in one transaction, each written once, in the order they were first
// touched.
func TestClaimPlanWritesEachDocumentOnce(t *testing.T) {
	first := claimRef("identifier_claims/a")
	second := claimRef("identifier_primaries/b")
	third := claimRef("session_refresh_hashes/c")

	plan := newClaimPlan()
	plan.take(first, identifierClaimDoc{IdentifierID: "ident-1"})
	plan.release(second)
	plan.release(third)
	plan.take(third, refreshHashClaimDoc{SessionID: "session-1"})

	w := &recordingWriter{}
	if err := plan.commit(context.Background(), w); err != nil {
		t.Fatalf("commit: %v", err)
	}
	want := []claimWrite{
		{verb: "Create", path: first.Path},
		{verb: "Delete", path: second.Path},
		{verb: "Set", path: third.Path},
	}
	if len(w.writes) != len(want) {
		t.Fatalf("commit emitted %+v, want %+v", w.writes, want)
	}
	for i, got := range w.writes {
		if got.verb != want[i].verb || got.path != want[i].path {
			t.Errorf("write %d is %s %s, want %s %s", i, got.verb, got.path, want[i].verb, want[i].path)
		}
	}
}

// TestClaimPlanCommitStopsAtTheFirstError proves commit reports a writer failure
// rather than swallowing it — the property every caller relies on when it
// returns plan.commit's error straight out of a transaction callback.
func TestClaimPlanCommitStopsAtTheFirstError(t *testing.T) {
	plan := newClaimPlan()
	plan.take(claimRef("identifier_claims/x"), identifierClaimDoc{IdentifierID: "ident-1"})

	boom := errors.New("write refused")
	err := plan.commit(context.Background(), failingWriter{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("commit returned %v, want the writer's error", err)
	}
}

// failingWriter refuses every write.
type failingWriter struct{ err error }

var _ firestoredb.Writer = failingWriter{}

func (w failingWriter) Create(context.Context, *gcfs.DocumentRef, any) error { return w.err }
func (w failingWriter) Set(context.Context, *gcfs.DocumentRef, any, ...gcfs.SetOption) error {
	return w.err
}
func (w failingWriter) Update(context.Context, *gcfs.DocumentRef, []gcfs.Update, ...gcfs.Precondition) error {
	return w.err
}
func (w failingWriter) Delete(context.Context, *gcfs.DocumentRef, ...gcfs.Precondition) error {
	return w.err
}
