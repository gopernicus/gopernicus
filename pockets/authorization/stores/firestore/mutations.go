package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

var (
	_ mutation.MutationRepository = (*mutationStore)(nil)
	_ mutation.StoreDecisionView  = (*decisionView)(nil)
)

// mutationStore fills mutation.MutationRepository: one Firestore transaction per
// Apply, with the phases of A-D5 strictly ordered (guard, replay/digest,
// validate, evaluate, then the single write phase). Its bodies land in A4a–A4c.
//
// The callback may run MORE THAN ONCE — the vendor re-runs it when a commit
// loses a race — so every attempt-local view, dependency list, staged write, and
// result is reset at the top of the callback, never initialized outside it.
type mutationStore struct {
	db       *firestoredb.DB
	guardian mutation.GuardianPolicy
}

func newMutationStore(db *firestoredb.DB, guardian mutation.GuardianPolicy) *mutationStore {
	return &mutationStore{db: db, guardian: guardian}
}

func (s *mutationStore) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := refuseAmbientMutation(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

func (s *mutationStore) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := refuseAmbientMutation(ctx); err != nil {
		return nil, err
	}
	return nil, errNotImplemented
}

// decisionView is the transaction-bound mutation.StoreDecisionView a guard
// reads through: every read runs on the mutation transaction's Reader and
// records the scope and the revision it observed, revision BEFORE rows, with a
// scope keeping its FIRST observed revision. It is never handed to a caller
// outside the transaction that built it, so it does not refuse an ambient
// transaction — it IS one. Bodies land in A4c.
type decisionView struct {
	db *firestoredb.DB
}

func (v *decisionView) CheckRelation(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string) (bool, error) {
	return false, errNotImplemented
}

func (v *decisionView) CheckRelationBounded(ctx context.Context, scope mutation.ScopeKey, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return false, errNotImplemented
}

func (v *decisionView) RelationTargets(ctx context.Context, scope mutation.ScopeKey, relation string) ([]relationship.RelationTarget, error) {
	return nil, errNotImplemented
}

func (v *decisionView) HasRole(ctx context.Context, scope mutation.ScopeKey, roleName, subjectType, subjectID string) (bool, error) {
	return false, errNotImplemented
}

func (v *decisionView) Dependencies() []mutation.Dependency {
	return nil
}
