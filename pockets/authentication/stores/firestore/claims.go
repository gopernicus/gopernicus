package firestore

import (
	"context"

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
// Order is preserved for deterministic, reviewable commits; Firestore applies a
// transaction's writes atomically, so the order carries no semantics of its own.
type claimPlan struct {
	order []string
	steps map[string]*claimStep
}

// claimStep is one claim document's pending outcome.
type claimStep struct {
	ref      *gcfs.DocumentRef
	released bool
	taken    bool
	data     any
}

func newClaimPlan() *claimPlan {
	return &claimPlan{steps: make(map[string]*claimStep)}
}

// release records that the row which held this claim is leaving the index's
// stored predicate — retired, demoted, deleted, or re-valued. Releasing a claim
// the caller does not in fact own is a bug the caller must not commit: every
// release() in this store follows a READ of the row that owns the claim.
func (p *claimPlan) release(ref *gcfs.DocumentRef) {
	p.step(ref).released = true
}

// take records that a row now satisfies the index's stored predicate and claims
// the key. data is the claim document's body.
func (p *claimPlan) take(ref *gcfs.DocumentRef, data any) {
	s := p.step(ref)
	s.taken = true
	s.data = data
}

// commit emits the write for every claim the plan touched. A document that was
// released and re-taken is written ONCE.
func (p *claimPlan) commit(ctx context.Context, w firestoredb.Writer) error {
	for _, path := range p.order {
		s := p.steps[path]
		switch {
		case s.taken && s.released:
			if err := w.Set(ctx, s.ref, s.data); err != nil {
				return err
			}
		case s.taken:
			if err := w.Create(ctx, s.ref, s.data); err != nil {
				return err
			}
		case s.released:
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
