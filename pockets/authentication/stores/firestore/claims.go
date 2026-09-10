package firestore

import (
	"context"
	"fmt"
	"reflect"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// claimPlan accumulates the FINAL state of every claim document one transaction
// touches, so the writes are emitted once, at the end, from the whole picture
// rather than from each row's local view.
//
// It exists because a single operation routinely RELEASES and RE-TAKES the same
// claim document. Promoting a new primary email releases the displaced row's
// (user, kind) primary claim and takes the same one for the new row; replacing
// an identifier with the same address releases and re-takes the same
// (kind, value) authentication claim. Emitting a Delete and a Create for one
// document in one commit is not a hand-over — it is a document whose final state
// depends on write ordering. The plan collapses the pair into a Set.
//
// The Create-versus-Set choice is also what makes ruling R3 work, and it is the
// reason take() is not simply "Set":
//
//   - A claim this transaction did NOT release is written with CREATE. If a
//     concurrent operation holds it, the commit fails AlreadyExists and this
//     operation rolls back whole — which is the uniqueness arbitration, and the
//     reason a claim never has to be READ to be safely taken.
//   - A claim this transaction DID release is written with SET, because the
//     document already exists and this operation already proved (by reading the
//     row that owned it) that the claim is its own to move. A Create there would
//     fail AlreadyExists on the store's own consistent state.
//
// THE INVARIANT: a claim document's outcome is decided by its LAST operation,
// not by the set of operations it received. The plan records an ordinal per
// call, so
//
//   - release then take → Set (a hand-over: the document survives, re-pointed);
//   - take then release → Delete (the taker changed its mind, or a later
//     revocation swept the row it had just written — the document must NOT
//     survive);
//   - take alone → Create (the R3 arbitration above);
//   - release alone → Delete.
//
// Without the ordinals the two mixed cases are indistinguishable and both
// resolve to Set, which is correct for the first and silently wrong for the
// second: a claim would outlive the row it names, and the address, hash or
// digest it holds would be unclaimable forever with nothing to point at.
//
// Taking one document TWICE with different data is a caller bug rather than a
// state: two rows cannot both own one unique key, and picking a winner by write
// order is how a store starts disagreeing with itself. commit refuses it.
//
// Order is preserved for deterministic, reviewable commits; Firestore applies a
// transaction's writes atomically, so the order carries no semantics of its own.
type claimPlan struct {
	order []string
	steps map[string]*claimStep
	ops   int
}

// claimStep is one claim document's pending outcome. releasedAt and takenAt are
// the ORDINALS of the last release() and take() the document received; zero
// means "never", and the larger one wins at commit.
type claimStep struct {
	ref        *gcfs.DocumentRef
	releasedAt int
	takenAt    int
	data       any
	conflict   error
}

func newClaimPlan() *claimPlan {
	return &claimPlan{steps: make(map[string]*claimStep)}
}

// release records that the row which held this claim is leaving the index's
// stored predicate — retired, demoted, deleted, or re-valued. Releasing a claim
// the caller does not in fact own is a bug the caller must not commit: every
// release() in this store follows a READ of the row that owns the claim.
func (p *claimPlan) release(ref *gcfs.DocumentRef) {
	p.step(ref).releasedAt = p.next()
}

// take records that a row now satisfies the index's stored predicate and claims
// the key. data is the claim document's body.
//
// Two takes of one document with the SAME body are one take — a row re-taking
// the claim it already holds, which replacement and use changes both do. Two
// takes with DIFFERENT bodies are two rows claiming one unique key inside one
// transaction, which no arbitration can resolve: the error is recorded here and
// surfaced by commit, so the whole operation rolls back rather than committing
// whichever body was queued last.
func (p *claimPlan) take(ref *gcfs.DocumentRef, data any) {
	s := p.step(ref)
	if s.takenAt > 0 && s.conflict == nil && !reflect.DeepEqual(s.data, data) {
		s.conflict = fmt.Errorf("authentication firestore store: %s was claimed twice in one transaction with different owners (%+v, then %+v) — two rows cannot hold one unique key", ref.ID, s.data, data)
	}
	s.takenAt = p.next()
	s.data = data
}

// commit emits exactly ONE write per claim document the plan touched, chosen by
// the document's LAST operation (the invariant above), and refuses a plan that
// claimed one key for two owners.
func (p *claimPlan) commit(ctx context.Context, w firestoredb.Writer) error {
	for _, path := range p.order {
		s := p.steps[path]
		if s.conflict != nil {
			return s.conflict
		}
		switch {
		case s.takenAt > s.releasedAt:
			// Taken last. A Set when this transaction also released the
			// document (it proved ownership by reading the row that held it), a
			// Create when it did not (the R3 arbitration: a concurrent holder
			// makes the commit fail).
			if s.releasedAt > 0 {
				if err := w.Set(ctx, s.ref, s.data); err != nil {
					return err
				}
				continue
			}
			if err := w.Create(ctx, s.ref, s.data); err != nil {
				return err
			}
		case s.releasedAt > 0:
			// Released last, whether or not this transaction had taken it
			// first. Delete is idempotent on an absent document, so a
			// take-then-release needs no special case.
			if err := w.Delete(ctx, s.ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// step returns the accumulator for ref, creating it on first use.
func (p *claimPlan) step(ref *gcfs.DocumentRef) *claimStep {
	if s, ok := p.steps[ref.Path]; ok {
		return s
	}
	s := &claimStep{ref: ref}
	p.steps[ref.Path] = s
	p.order = append(p.order, ref.Path)
	return s
}

// next is the operation counter. It starts at 1, so a zero ordinal is
// unambiguously "this document never received that operation".
func (p *claimPlan) next() int {
	p.ops++
	return p.ops
}
