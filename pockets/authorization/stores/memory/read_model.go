package memory

import (
	"context"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func (r *Relationships) ForModel(model relationships.ReadModel) relationships.Reader {
	return &Relationships{st: r.st, model: &model}
}

func (r *Relationships) allows(row relRow) bool {
	return r.model == nil || r.model.Allows(row.resourceType, row.relation, row.subjectType, row.subjectRelation)
}

// A model view retains the same staged snapshot. Reads remain under the
// mutation's already-held mutex.
type modelDecisionReader struct {
	view  *decisionView
	model relationships.ReadModel
}

func (v *decisionView) ForModel(model relationships.ReadModel) relationships.PermissionReader {
	return modelDecisionReader{view: v, model: model}
}

func (r modelDecisionReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	v := r.view
	rels := Relationships{st: v.m.rels.st, model: &r.model}
	ok, overflow := rels.checkRelationExpandedLocked(ctx, resourceType, resourceID, relation, subjectType, subjectID, maxExpansionStates)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if overflow {
		return false, relationships.ErrExpansionBudgetExceeded
	}
	return ok, nil
}

func (r modelDecisionReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	targets, err := r.view.RelationTargets(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation)
	if err != nil {
		return nil, err
	}
	return r.model.FilterTargets(resourceType, relation, targets), nil
}
