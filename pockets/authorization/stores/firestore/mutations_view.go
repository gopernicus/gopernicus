package firestore

import (
	"context"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

var _ mutations.StoreDecisionView = (*decisionView)(nil)

// The view uses only the mutation transaction's Reader. A new attempt gets a
// fresh view, and native transaction isolation covers every document and query.
type decisionView struct {
	db *firestoredb.DB
	r  firestoredb.Reader
}

func newDecisionView(db *firestoredb.DB, r firestoredb.Reader) *decisionView {
	return &decisionView{db: db, r: r}
}

func (v *decisionView) CheckRelation(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string) (bool, error) {
	return v.CheckRelationBounded(ctx, target, relation, subjectType, subjectID, 0)
}

func (v *decisionView) CheckRelationBounded(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return v.checkRelationWithModel(ctx, target, relation, subjectType, subjectID, maxExpansionStates, nil)
}

func (v *decisionView) checkRelationWithModel(ctx context.Context, target mutations.Target, relation, subjectType, subjectID string, maxExpansionStates int, model *relationships.ReadModel) (allowed bool, err error) {
	defer func() { err = markGuardReadError(err) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	reached, err := expand(ctx, v.db, v.r, subjectType, subjectID, maxExpansionStates, model)
	if err != nil {
		return false, err
	}
	return anyTupleWithSubject(ctx, v.db, v.r, target.Type, target.ID, relation, reached, model)
}

func (v *decisionView) RelationTargets(ctx context.Context, target mutations.Target, relation string) (targets []relationships.RelationTarget, err error) {
	defer func() { err = markGuardReadError(err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return relationTargets(ctx, v.db, v.r, target.Type, target.ID, relation)
}

// HasRole uses exact scope plus the global fallback, through deterministic
// document reads. A missing document is part of the transaction's read set.
func (v *decisionView) HasRole(ctx context.Context, target mutations.Target, roleName, subjectType, subjectID string) (held bool, err error) {
	defer func() { err = markGuardReadError(err) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	if err := (relationships.SubjectRef{Type: subjectType, ID: subjectID}).Validate(); err != nil {
		return false, err
	}
	if err := authmodel.ValidateRefField("role", roleName); err != nil {
		return false, err
	}
	var resourceType, resourceID string
	if target.Kind == mutations.TargetResource {
		resourceType, resourceID = target.Type, target.ID
	}
	ok, err := roleExists(ctx, v.db, v.r, subjectType, subjectID, roleName, resourceType, resourceID)
	if err != nil || ok {
		return ok, err
	}
	if target.Kind != mutations.TargetResource {
		return false, nil
	}
	return roleExists(ctx, v.db, v.r, subjectType, subjectID, roleName, "", "")
}
