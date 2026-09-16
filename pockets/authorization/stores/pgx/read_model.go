package pgx

import (
	"context"
	"maps"
	"strings"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/jackc/pgx/v5"
)

func (s *relationshipStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return &relationshipStore{db: s.db, tupleBinding: s.tupleBinding, readQuerier: s.readQuerier, schema: s.schema, model: &model, audit: s.audit, integrity: s.integrity}
}

func (s *relationshipStore) reader(ctx context.Context) pgxdb.Querier {
	q := s.readQuerier
	if q == nil {
		q = s.db.QuerierFrom(ctx)
	}
	if s.model == nil {
		return q
	}
	return modelQuerier{Querier: q, schema: s.schema, model: *s.model}
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
	query = strings.ReplaceAll(query, resourceTupleTable(q.schema), "authorization_model_relationships")
	query = strings.TrimSpace(query)
	prefix := `WITH RECURSIVE authorization_model_relationships AS NOT MATERIALIZED (
	SELECT stored.* FROM ` + resourceTupleTable(q.schema) + ` stored
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
