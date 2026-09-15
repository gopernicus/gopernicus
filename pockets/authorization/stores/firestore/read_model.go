package firestore

import (
	"context"

	gcfs "cloud.google.com/go/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func (s *relationshipStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return &relationshipStore{db: s.db, model: &model, audit: s.audit}
}

func firstReadModel(models []*relationships.ReadModel) *relationships.ReadModel {
	if len(models) == 0 {
		return nil
	}
	return models[0]
}

func permits(model *relationships.ReadModel, row relationshipDoc) bool {
	return model == nil || model.Allows(row.ResourceType, row.Relation, row.SubjectType, row.SubjectRelation)
}

func (s *relationshipStore) modelResourceID(snap *gcfs.DocumentSnapshot) (string, error) {
	row, err := decodeRelationship(snap)
	if err != nil {
		return "", err
	}
	if !permits(s.model, row) {
		return "", nil
	}
	return row.ResourceID, nil
}

type modelDecisionReader struct {
	view  *decisionView
	model relationships.ReadModel
}

func (v *decisionView) ForModel(model relationships.ReadModel) relationships.PermissionReader {
	return modelDecisionReader{view: v, model: model}
}

func (r modelDecisionReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return r.view.checkRelationWithModel(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation, subjectType, subjectID, maxExpansionStates, &r.model)
}

func (r modelDecisionReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	targets, err := r.view.RelationTargets(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation)
	if err != nil {
		return nil, err
	}
	return r.model.FilterTargets(resourceType, relation, targets), nil
}
