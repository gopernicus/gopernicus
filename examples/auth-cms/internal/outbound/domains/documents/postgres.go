package documents

import (
	"context"
	"fmt"
	"strings"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model2 "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

type Postgres struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

func NewPostgres(db *pgxdb.DB, schema pgxdb.Schema) (*Postgres, error) {
	if db == nil {
		return nil, fmt.Errorf("documents: database is required: %w", sdk.ErrInvalidInput)
	}
	return &Postgres{db: db, schema: schema}, nil
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

func (p *Postgres) read(ctx context.Context, query domain.Query, after position, limit int, filter decisions.ResourceSet) ([]row, bool, error) {
	return p.readWithPredicate(ctx, query, after, limit, filter, nil, pgxdb.Schema{})
}

func (p *Postgres) querySQL(query domain.Query, after position, limit int, filter decisions.ResourceSet, principal *sdk.Principal, authorizationSchema pgxdb.Schema) (string, pgx.NamedArgs) {
	args := pgx.NamedArgs{"tenant": query.TenantID, "search": query.Search, "limit": limit + 1}
	predicates := []string{"d.tenant_id = @tenant", "strpos(d.name_key, @search) > 0"}
	if !filter.Unrestricted {
		predicates = append(predicates, "d.id = ANY(@ids::text[])")
		args["ids"] = filter.IDs
	}
	if principal != nil {
		// This branch is available only after NewSQLListing validates the exact
		// selected permission. It is an EXISTS predicate, so grants cannot multiply
		// business rows or corrupt LIMIT/count semantics.
		predicates = append(predicates, `@principal_type::text = 'user' AND EXISTS (
			SELECT 1 FROM `+authorizationSchema.Table("iam_relationships")+` g
			WHERE g.resource_type='document' AND g.resource_id=d.id AND g.relation='viewer'
			AND g.subject_type=@principal_type AND g.subject_id=@principal_id AND g.subject_relation='')`)
		args["principal_type"], args["principal_id"] = principal.Type, principal.ID
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

func (p *Postgres) readWithPredicate(ctx context.Context, query domain.Query, after position, limit int, filter decisions.ResourceSet, principal *sdk.Principal, authorizationSchema pgxdb.Schema) ([]row, bool, error) {
	if !filter.Unrestricted && len(filter.IDs) == 0 {
		return []row{}, false, nil
	}
	sql, args := p.querySQL(query, after, limit, filter, principal, authorizationSchema)
	rows, err := p.db.QuerierFrom(ctx).Query(ctx, sql, args)
	if err != nil {
		return nil, false, pgxdb.MapError(err)
	}
	defer rows.Close()
	items := make([]row, 0, limit+1)
	for rows.Next() {
		var item row
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

type SQLListing struct {
	store               *Postgres
	authorizationSchema pgxdb.Schema
	codec               *CursorCodec
	bypass              Bypass
}

// NewSQLListing supports exactly document:view = Direct(viewer), where viewer
// allows concrete users only. Other permissions/types may coexist. Inheritance,
// usersets, additional OR branches and role-owned view are rejected at construction.
// The host must supply the same authorization database/schema used by authorizer;
// this example does not discover, synchronize or copy another store's grants.
func NewSQLListing(store *Postgres, authorizationSchema pgxdb.Schema, authorizer *relationships.Service, codec *CursorCodec, bypass Bypass) (*SQLListing, error) {
	if store == nil || authorizer == nil || codec == nil {
		return nil, fmt.Errorf("documents: incomplete SQL listing: %w", sdk.ErrInvalidInput)
	}
	model := authorizer.GetSchema()
	checks := model.Checks("document", "view")
	subjects := model.AllowedSubjects("document", "viewer")
	if len(checks) != 1 || checks[0].Relation != "viewer" || checks[0].Through != "" || checks[0].Permission != "" ||
		len(subjects) != 1 || subjects[0].Type != "user" || subjects[0].Relation != "" {
		return nil, fmt.Errorf("documents: SQL listing requires concrete-user Direct(viewer) policy: %w", sdk.ErrInvalidInput)
	}
	return &SQLListing{store: store, authorizationSchema: authorizationSchema, codec: codec, bypass: bypass}, nil
}

func (l *SQLListing) ListVisible(ctx context.Context, principal sdk.Principal, query domain.Query) (domain.Page, error) {
	if err := query.Normalize(); err != nil {
		return domain.Page{}, err
	}
	if err := model2.PrincipalFrom(principal).Validate(); err != nil {
		return domain.Page{}, err
	}
	binding := cursorBinding(principal, query)
	after, err := l.codec.decode(query.Cursor, binding)
	if err != nil {
		return domain.Page{}, err
	}
	unrestricted, err := checkBypass(ctx, l.bypass, principal)
	if err != nil {
		return domain.Page{}, err
	}
	var predicatePrincipal *sdk.Principal
	if !unrestricted {
		predicatePrincipal = &principal
	}
	rows, more, err := l.store.readWithPredicate(ctx, query, after, query.Limit, decisions.ResourceSet{Unrestricted: true}, predicatePrincipal, l.authorizationSchema)
	if err != nil {
		return domain.Page{}, err
	}
	return pageFromRows(l.codec, binding, rows, more)
}
