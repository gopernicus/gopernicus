package turso

import (
	"context"
	"database/sql"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func (s *relationshipStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return &relationshipStore{db: s.db, model: &model, audit: s.audit}
}

func (s *relationshipStore) reader(ctx context.Context) tursodb.Querier {
	if s.model == nil {
		return s.db.QuerierFrom(ctx)
	}
	return modelQuerier{Querier: s.db.QuerierFrom(ctx), model: *s.model}
}

// This wrapper only receives the adapter's static relationship READ statements.
// The allowlist is one bound JSON value, before the original positional binds.
type modelQuerier struct {
	tursodb.Querier
	model relationships.ReadModel
}

func (q modelQuerier) scoped(query string, args []any) (string, []any) {
	query = strings.ReplaceAll(query, "iam_relationships", "authorization_model_relationships")
	query = strings.TrimSpace(query)
	prefix := `WITH RECURSIVE authorization_model_relationships AS NOT MATERIALIZED (
	SELECT stored.* FROM iam_relationships stored
	JOIN json_each(?) model ON
	stored.resource_type = json_extract(model.value, '$.resource_type') AND
	stored.relation = json_extract(model.value, '$.relation') AND
	stored.subject_type = json_extract(model.value, '$.subject_type') AND
	stored.subject_relation = json_extract(model.value, '$.subject_relation')
)`
	if rest, ok := strings.CutPrefix(query, "WITH RECURSIVE "); ok {
		query = prefix + ", " + rest
	} else if rest, ok := strings.CutPrefix(query, "WITH "); ok {
		query = prefix + ", " + rest
	} else {
		query = prefix + " " + query
	}
	return query, append([]any{q.model.JSON()}, args...)
}

func (q modelQuerier) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query, args = q.scoped(query, args)
	return q.Querier.Query(ctx, query, args...)
}

func (q modelQuerier) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	query, args = q.scoped(query, args)
	return q.Querier.QueryRow(ctx, query, args...)
}

type modelDecisionReader struct {
	view  *decisionView
	model relationships.ReadModel
}

func (v *decisionView) ForModel(model relationships.ReadModel) relationships.PermissionReader {
	return modelDecisionReader{view: v, model: model}
}

func (r modelDecisionReader) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error) {
	return r.view.checkRelationWithReader(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation, subjectType, subjectID, maxExpansionStates,
		modelQuerier{Querier: r.view.tx, model: r.model})
}

func (r modelDecisionReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	targets, err := r.view.RelationTargets(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation)
	if err != nil {
		return nil, err
	}
	return r.model.FilterTargets(resourceType, relation, targets), nil
}
