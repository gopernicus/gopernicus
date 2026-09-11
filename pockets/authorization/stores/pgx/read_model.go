package pgx

import (
	"context"
	"maps"
	"strings"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/jackc/pgx/v5"
)

func (s *relationshipStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return &relationshipStore{db: s.db, schema: s.schema, model: &model, audit: s.audit}
}

func (s *relationshipStore) reader(ctx context.Context) pgxdb.Querier {
	if s.model == nil {
		return s.db.QuerierFrom(ctx)
	}
	return modelQuerier{Querier: s.db.QuerierFrom(ctx), schema: s.schema, model: *s.model}
}

// Only the adapter's static relationship READ statements use this wrapper.
// Both the recursive edge and final match read the same filtered relation;
// model names are bound JSON data, never interpolated SQL identifiers.
type modelQuerier struct {
	pgxdb.Querier
	schema pgxdb.Schema
	model  relationships.ReadModel
}

func (q modelQuerier) scoped(query string, args []any) (string, []any) {
	query = strings.ReplaceAll(query, q.schema.Table("iam_relationships"), "authorization_model_relationships")
	query = strings.TrimSpace(query)
	prefix := `WITH RECURSIVE authorization_model_relationships AS NOT MATERIALIZED (
	SELECT stored.* FROM ` + q.schema.Table("iam_relationships") + ` stored
	JOIN jsonb_to_recordset(@authorization_read_model::jsonb) AS model(resource_type text, relation text, subject_type text, subject_relation text)
	USING (resource_type, relation, subject_type, subject_relation)
)`
	if rest, ok := strings.CutPrefix(query, "WITH RECURSIVE "); ok {
		query = prefix + ", " + rest
	} else if rest, ok := strings.CutPrefix(query, "WITH "); ok {
		query = prefix + ", " + rest
	} else {
		query = prefix + " " + query
	}
	bound := maps.Clone(args[0].(pgx.NamedArgs))
	bound["authorization_read_model"] = q.model.JSON()
	return query, []any{bound}
}

func (q modelQuerier) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	query, args = q.scoped(query, args)
	return q.Querier.Query(ctx, query, args...)
}

func (q modelQuerier) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
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
		modelQuerier{Querier: r.view.tx, schema: r.view.schema, model: r.model})
}

func (r modelDecisionReader) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationships.RelationTarget, error) {
	targets, err := r.view.RelationTargets(ctx, mutations.Target{Kind: mutations.TargetResource, Type: resourceType, ID: resourceID}, relation)
	if err != nil {
		return nil, err
	}
	return r.model.FilterTargets(resourceType, relation, targets), nil
}
