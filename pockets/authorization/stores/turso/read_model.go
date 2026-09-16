package turso

import (
	"context"
	"database/sql"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func (s *relationshipStore) ForModel(model relationships.ReadModel) relationships.Reader {
	return &relationshipStore{db: s.db, tupleBinding: s.tupleBinding, readQuerier: s.readQuerier, model: &model, audit: s.audit, integrity: s.integrity}
}

func (s *relationshipStore) reader(ctx context.Context) tursodb.Querier {
	q := s.readQuerier
	if q == nil {
		q = s.db.QuerierFrom(ctx)
	}
	if s.model == nil {
		return q
	}
	return modelQuerier{Querier: q, model: *s.model}
}

// This wrapper only receives the adapter's static relationship READ statements.
// The allowlist is one bound JSON value, before the original positional binds.
type modelQuerier struct {
	tursodb.Querier
	model relationships.ReadModel
}

func (q modelQuerier) scoped(query string, args []any) (string, []any) {
	query = strings.ReplaceAll(query, "(SELECT * FROM main.iam_tuples WHERE scope_kind=2)", "authorization_model_relationships")
	query = strings.TrimSpace(query)
	prefix := `WITH RECURSIVE authorization_model_relationships AS NOT MATERIALIZED (
	SELECT stored.* FROM (SELECT * FROM main.iam_tuples WHERE scope_kind=2) stored
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
