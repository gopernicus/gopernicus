package pgx

import (
	"context"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/jackc/pgx/v5"
)

// roleKeyExpr joins the natural five-field identity with the forbidden U+0001
// separator. The computed key supplies both ordering and cursor continuation.
const roleKeyExpr = "(subject_type || chr(1) || subject_id || chr(1) || role || chr(1) || resource_type || chr(1) || resource_id) COLLATE \"C\""

// roleRow is the db-tagged projection of an iam_roles listing row. RoleKey is the
// derived keyset tiebreak (see rolesBaseSQL) — scanned so PKOf can echo it.
type roleRow struct {
	SubjectType  string `db:"subject_type"`
	SubjectID    string `db:"subject_id"`
	Role         string `db:"role"`
	ResourceType string `db:"resource_type"`
	ResourceID   string `db:"resource_id"`
	RoleKey      string `db:"role_key"`
}

func (r roleRow) toDomain() roles.Assignment {
	return roles.Assignment{
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
		Role:         r.Role,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
	}
}

// effectiveGrantKeyExpr is the effective listing's derived ordering/keyset key:
// the (subject_type, subject_id, role) triple joined by chr(1) (postgres forbids
// NUL in text). It is DB-computed and echoed back by PKOf so the cursor PK
// matches the column byte-for-byte.
const effectiveGrantKeyExpr = "(subject_type || chr(1) || subject_id || chr(1) || role) COLLATE \"C\""

// effectiveRoleRow is the db-tagged projection of an effective-grant listing row.
// IsDirect/IsGlobal are the MAX(CASE …) provenance flags (1/0); GrantKey is the
// derived keyset key (see effectiveRolesBaseSQL).
type effectiveRoleRow struct {
	SubjectType string `db:"subject_type"`
	SubjectID   string `db:"subject_id"`
	Role        string `db:"role"`
	IsDirect    int    `db:"is_direct"`
	IsGlobal    int    `db:"is_global"`
	GrantKey    string `db:"grant_key"`
}

func (r effectiveRoleRow) toDomain() roles.EffectiveGrant {
	return roles.EffectiveGrant{
		SubjectType: r.SubjectType,
		SubjectID:   r.SubjectID,
		Role:        r.Role,
		Direct:      r.IsDirect == 1,
		Global:      r.IsGlobal == 1,
	}
}

// effectiveRolesBaseSQL builds the EFFECTIVE listing over iam_roles: it groups the
// direct-scope rows and (for a scoped request) the global rows by (subject, role),
// emitting a provenance flag per source plus the derived grant_key. scopedLiteral
// is the SQL boolean literal for "the request is scoped": TRUE gates the global
// fallback in, FALSE (a global request) collapses it out so every grant is Direct
// — mirroring HasRole's no-fallback path for an unscoped query. A global grant is
// NEVER rewritten as a scoped row; its scope stays out of the projection and only
// its provenance is reported. The outer `WHERE 1 = 1` lets pgxdb.List append its
// keyset predicate with AND (the inner GROUP BY subquery already carries a WHERE).
func effectiveRolesBaseSQL(schema pgxdb.Schema, scopedLiteral string) string {
	return `SELECT subject_type, subject_id, role, is_direct, is_global, grant_key FROM (
	SELECT subject_type, subject_id, role,
		MAX(CASE WHEN resource_type = @resource_type AND resource_id = @resource_id THEN 1 ELSE 0 END) AS is_direct,
		MAX(CASE WHEN ` + scopedLiteral + ` AND resource_type = '' AND resource_id = '' THEN 1 ELSE 0 END) AS is_global,
		` + effectiveGrantKeyExpr + ` AS grant_key
	FROM ` + schema.Table("iam_roles") + `
	WHERE (resource_type = @resource_type AND resource_id = @resource_id)
	   OR (` + scopedLiteral + ` AND resource_type = '' AND resource_id = '')
	GROUP BY subject_type, subject_id, role
) AS r WHERE 1 = 1`
}

// rolesBaseSQL wraps the filtered iam_roles rows in a derived table exposing the
// computed role_key column, so the keyset builder can reference it in the outer
// WHERE (a WHERE cannot see a same-level SELECT alias) and ORDER BY. The trailing
// `WHERE 1 = 1` lets pgxdb.List append its keyset predicate with AND.
func rolesBaseSQL(schema pgxdb.Schema, innerWhere string) string {
	return `SELECT subject_type, subject_id, role, resource_type, resource_id, role_key FROM (
	SELECT subject_type, subject_id, role, resource_type, resource_id, ` + roleKeyExpr + ` AS role_key
	FROM ` + schema.Table("iam_roles") + innerWhere + `
) AS r WHERE 1 = 1`
}

// roleStore fills role.Storer over iam_roles. Every statement runs on
// s.db.QuerierFrom(ctx) — the connector's ambient Transact-owned transaction
// when the context carries one, the pool otherwise — so a role assignment joins
// the same host transaction the relationship tuples beside it do.
type roleStore struct {
	audit  bool
	db     *pgxdb.DB
	schema pgxdb.Schema
}

func newRoleStore(db *pgxdb.DB, cfg config) *roleStore {
	return &roleStore{db: db, schema: cfg.schema, audit: cfg.audit}
}

// table renders name under the store's schema — the one chokepoint every
// statement on this store goes through.
func (s *roleStore) table(name string) string { return s.schema.Table(name) }

var _ roles.Storer = (*roleStore)(nil)

// Assign inserts an assignment idempotently via the TARGETED ON CONFLICT DO
// NOTHING on the 5-tuple index (never a bare OR-IGNORE-style suppression: a NOT
// NULL breach still raises). A duplicate is a no-op.
func (s *roleStore) Assign(ctx context.Context, a roles.Assignment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	q := `INSERT INTO ` + s.table("iam_roles") + ` (subject_type, subject_id, role, resource_type, resource_id)
VALUES (@subject_type, @subject_id, @role, @resource_type, @resource_id)
ON CONFLICT (subject_type, subject_id, role, resource_type, resource_id) DO NOTHING`
	return s.write(ctx, func(tx *writeTx) error {
		_, err := tx.roles(ctx, audit.ActionAdded, q, pgx.NamedArgs{
			"subject_type":  a.SubjectType,
			"subject_id":    a.SubjectID,
			"role":          a.Role,
			"resource_type": a.ResourceType,
			"resource_id":   a.ResourceID,
		})
		return err
	})
}

// Unassign removes an exact assignment (idempotent — zero rows deleted is nil).
func (s *roleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	q := `DELETE FROM ` + s.table("iam_roles") + ` WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id`
	return s.write(ctx, func(tx *writeTx) error {
		if _, err := tx.roles(ctx, audit.ActionRemoved, q, pgx.NamedArgs{
			"subject_type":  subjectType,
			"subject_id":    subjectID,
			"role":          roleName,
			"resource_type": resourceType,
			"resource_id":   resourceID,
		}); err != nil {
			return err
		}
		return nil
	})
}

// HasExactRole reports whether an assignment exists at the EXACT scope. The
// global-fallback rule (a global grant satisfies a scoped check) is the service's,
// never the store's.
func (s *roleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	q := `SELECT EXISTS (SELECT 1 FROM ` + s.table("iam_roles") + ` WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id)`
	var ok bool
	if err := s.db.QuerierFrom(ctx).QueryRow(ctx, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"role":          roleName,
		"resource_type": resourceType,
		"resource_id":   resourceID,
	}).Scan(&ok); err != nil {
		return false, pgxdb.MapError(err)
	}
	return ok, nil
}

// ListBySubject pages a subject's assignments by role_key in byte order.
func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req list.Request) (list.Page[roles.Assignment], error) {
	q := pgxdb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(s.schema, " WHERE subject_type = @subject_type AND subject_id = @subject_id"),
		Args:         pgx.NamedArgs{"subject_type": subjectType, "subject_id": subjectID},
		OrderFields:  roles.OrderFields,
		DefaultOrder: roles.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.RoleKey },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[roles.Assignment]{}, err
	}
	return list.MapPage(page, roleRow.toDomain), nil
}

// ListByResource is the RAW direct-scope listing: it pages the assignments stored
// exactly at (resourceType, resourceID) and never surfaces globally-granted
// subjects. Use ListEffectiveByResource for the enumeration that agrees with the
// service's HasRole fallback.
func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.Assignment], error) {
	q := pgxdb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(s.schema, " WHERE resource_type = @resource_type AND resource_id = @resource_id"),
		Args:         pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID},
		OrderFields:  roles.OrderFields,
		DefaultOrder: roles.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.RoleKey },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[roles.Assignment]{}, err
	}
	return list.MapPage(page, roleRow.toDomain), nil
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the union
// of the direct scoped assignments with the global assignments a scoped HasRole
// satisfies, de-duplicated by (subject, role) with provenance, ordered by the
// derived grant_key ascending. A global grant is never rewritten as a scoped row.
func (s *roleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[roles.EffectiveGrant], error) {
	scopedLiteral := "FALSE"
	if resourceType != "" || resourceID != "" {
		scopedLiteral = "TRUE"
	}
	q := pgxdb.ListQuery[effectiveRoleRow]{
		BaseSQL:      effectiveRolesBaseSQL(s.schema, scopedLiteral),
		Args:         pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID},
		OrderFields:  roles.EffectiveOrderFields,
		DefaultOrder: roles.DefaultEffectiveOrder,
		PK:           "grant_key",
		OrderValueOf: func(r effectiveRoleRow, _ string) any { return r.GrantKey },
		PKOf:         func(r effectiveRoleRow) string { return r.GrantKey },
	}
	page, err := pgxdb.List(ctx, s.db.QuerierFrom(ctx), q, req)
	if err != nil {
		return list.Page[roles.EffectiveGrant]{}, err
	}
	return list.MapPage(page, effectiveRoleRow.toDomain), nil
}

// lookupResourceIDsBySubjectAndRolesSQL renders the SCOPED half of the roles
// keyset lookup and its args (split from the method so the EXPLAIN test can plan
// the exact statement the store runs). No per-query COLLATE "C" is needed here,
// unlike the relationship lookups: every iam_roles structural column is pinned
// COLLATE "C" in 0002, so resource_id already compares and orders byte-wise.
func (s *roleStore) lookupResourceIDsBySubjectAndRolesSQL(subjectType, subjectID, resourceType string, roles []string, after string, limit int) (string, pgx.NamedArgs) {
	q := `SELECT DISTINCT resource_id FROM ` + s.table("iam_roles") + `
WHERE subject_type = @subject_type AND subject_id = @subject_id AND resource_type = @resource_type AND role = ANY(@roles::text[])
  AND (@after::text = '' OR resource_id > @after::text)
ORDER BY resource_id` + limitClause(limit)
	return q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"resource_type": resourceType,
		"roles":         roles,
		"after":         after,
		"limit":         limit,
	}
}

// globalRoleGrantSQL renders the GLOBAL probe of the roles keyset lookup: does
// the subject hold any of the granting roles at the GLOBAL scope (both scope
// columns empty)? A hit
// is the unrestricted verdict — the subject reaches every resource of the type,
// so there is nothing to page.
func (s *roleStore) globalRoleGrantSQL(subjectType, subjectID string, roles []string) (string, pgx.NamedArgs) {
	q := `SELECT EXISTS (SELECT 1 FROM ` + s.table("iam_roles") + `
WHERE subject_type = @subject_type AND subject_id = @subject_id AND resource_type = '' AND resource_id = '' AND role = ANY(@roles::text[]))`
	return q, pgx.NamedArgs{
		"subject_type": subjectType,
		"subject_id":   subjectID,
		"roles":        roles,
	}
}

// LookupResourceIDsBySubjectAndRoles is the roles kind's resource-id lookup: a
// global grant of any of roles reports unrestricted (nil ids, nothing to page),
// otherwise the distinct scoped resource IDs of resourceType at which the subject
// holds any of roles — byte-order sorted, strictly greater than after, at most
// limit rows. An empty roles is (nil, false, nil): no role grants nothing, and
// the caller is spared a query. It applies no model knowledge — the engine passes
// the compiled granting roles.
func (s *roleStore) LookupResourceIDsBySubjectAndRoles(ctx context.Context, subjectType, subjectID, resourceType string, roles []string, after string, limit int) ([]string, bool, error) {
	if len(roles) == 0 {
		return nil, false, nil
	}
	probe, probeArgs := s.globalRoleGrantSQL(subjectType, subjectID, roles)
	var unrestricted bool
	if err := s.db.QuerierFrom(ctx).QueryRow(ctx, probe, probeArgs).Scan(&unrestricted); err != nil {
		return nil, false, pgxdb.MapError(err)
	}
	if unrestricted {
		return nil, true, nil
	}

	q, args := s.lookupResourceIDsBySubjectAndRolesSQL(subjectType, subjectID, resourceType, roles, after, limit)
	ids, err := queryStrings(ctx, s.db.QuerierFrom(ctx), q, args)
	if err != nil {
		return nil, false, err
	}
	return ids, false, nil
}

func (s *roleStore) write(ctx context.Context, fn func(*writeTx) error) error {
	return runWrite(ctx, s.db, config{audit: s.audit, schema: s.schema}, fn)
}
