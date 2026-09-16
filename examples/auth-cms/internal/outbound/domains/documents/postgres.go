package documents

import (
	"context"
	"fmt"
	"strings"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

type Postgres struct {
	db               *pgxdb.DB
	schema           pgxdb.Schema
	membershipSchema *pgxdb.Schema
}

type PostgresOption func(*Postgres)

// WithMembershipSchema enables exact membership restrictions against the same
// database. The host owns schema selection and validates its policy inbound.
func WithMembershipSchema(schema pgxdb.Schema) PostgresOption {
	return func(p *Postgres) { p.membershipSchema = &schema }
}

func NewPostgres(db *pgxdb.DB, schema pgxdb.Schema, opts ...PostgresOption) (*Postgres, error) {
	if db == nil {
		return nil, fmt.Errorf("documents: database is required: %w", sdk.ErrInvalidInput)
	}
	store := &Postgres{db: db, schema: schema}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("documents: nil PostgreSQL option: %w", sdk.ErrInvalidInput)
		}
		opt(store)
	}
	return store, nil
}

// Put is a trusted fixture/host write, never an HTTP authorization bypass. The
// persisted Unicode-lowercase key gives memory and SQL the same bytewise order.
func (p *Postgres) Put(ctx context.Context, doc domain.Document) error {
	if err := doc.Validate(); err != nil {
		return err
	}
	_, err := p.db.QuerierFrom(ctx).Exec(ctx, `INSERT INTO `+p.schema.Table("documents")+`
		(id, tenant_id, name, name_key) VALUES (@id, @tenant, @name, @key)
		ON CONFLICT (id) DO UPDATE SET tenant_id=excluded.tenant_id, name=excluded.name, name_key=excluded.name_key`,
		pgx.NamedArgs{"id": doc.ID, "tenant": doc.TenantID, "name": doc.Name, "key": strings.ToLower(doc.Name)})
	return pgxdb.MapError(err)
}

func (p *Postgres) querySQL(query domain.Query, after domain.Position, limit int, filter domain.Restriction) (string, pgx.NamedArgs) {
	args := pgx.NamedArgs{"tenant": query.TenantID, "search": query.Search, "limit": limit + 1}
	predicates := []string{"d.tenant_id = @tenant", "strpos(d.name_key, @search) > 0"}
	if !filter.Unrestricted && filter.Membership == nil {
		predicates = append(predicates, "d.id = ANY(@ids::text[])")
		args["ids"] = filter.IDs
	}
	if membership := filter.Membership; membership != nil {
		// Inbound selected this exact predicate. EXISTS cannot multiply business
		// rows, and storage never expands or evaluates a permission model.
		predicates = append(predicates, `EXISTS (
			SELECT 1 FROM `+p.membershipSchema.Table("iam_tuples")+` g
			WHERE g.scope_kind=2 AND g.resource_type='document' AND g.resource_id=d.id AND g.relation=@relation
			AND g.subject_type=@subject_type AND g.subject_id=@subject_id AND g.subject_relation='')`)
		args["subject_type"], args["subject_id"], args["relation"] = membership.SubjectType, membership.SubjectID, membership.Relation
	}
	direction, comparison := "ASC", ">"
	if query.Desc {
		direction, comparison = "DESC", "<"
	}
	if after.ID != "" {
		predicates = append(predicates, `(d.name_key COLLATE "C", d.id COLLATE "C") `+comparison+` (@after_name::text COLLATE "C", @after_id::text COLLATE "C")`)
		args["after_name"], args["after_id"] = after.NameKey, after.ID
	}
	sql := `SELECT d.id, d.tenant_id, d.name, d.name_key FROM ` + p.schema.Table("documents") + ` d
		WHERE ` + strings.Join(predicates, " AND ") + `
		ORDER BY d.name_key COLLATE "C" ` + direction + `, d.id COLLATE "C" ` + direction + ` LIMIT @limit`
	return sql, args
}

func (p *Postgres) Read(ctx context.Context, query domain.Query, after domain.Position, limit int, filter domain.Restriction) ([]domain.Row, bool, error) {
	if err := filter.Validate(); err != nil {
		return nil, false, err
	}
	if filter.Membership != nil && p.membershipSchema == nil {
		return nil, false, fmt.Errorf("documents: membership source is not configured: %w", sdk.ErrInvalidInput)
	}
	if !filter.Unrestricted && filter.Membership == nil && len(filter.IDs) == 0 {
		return []domain.Row{}, false, nil
	}
	sql, args := p.querySQL(query, after, limit, filter)
	rows, err := p.db.QuerierFrom(ctx).Query(ctx, sql, args)
	if err != nil {
		return nil, false, pgxdb.MapError(err)
	}
	defer rows.Close()
	items := make([]domain.Row, 0, limit+1)
	for rows.Next() {
		var item domain.Row
		if err := rows.Scan(&item.ID, &item.TenantID, &item.Name, &item.NameKey); err != nil {
			return nil, false, pgxdb.MapError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, pgxdb.MapError(err)
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, nil
}
