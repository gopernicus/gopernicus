package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// roleColumns is the iam_roles projection in a fixed order shared by the Assign
// insert and the rolesBaseSQL listing (matching roleRow's db tags).
const roleColumns = "subject_type, subject_id, role, resource_type, resource_id, created_at"

// roleKeyExpr is the SQL keyset tiebreak: the 5-tuple joined by char(1).
// iam_roles has no surrogate id — the 5-tuple is the natural key. The value is
// DB-computed and echoed back by PKOf (never recomputed in Go), so the cursor PK
// always matches the derived role_key column byte-for-byte; the separator choice
// is backend-local and need not match the pgx sibling (which uses chr(1)).
const roleKeyExpr = "subject_type || char(1) || subject_id || char(1) || role || char(1) || resource_type || char(1) || resource_id"

// effectiveGrantKeyExpr is the effective listing's derived ordering/keyset key:
// the (subject_type, subject_id, role) triple joined by char(1). It is DB-computed
// and echoed back by PKOf so the cursor PK matches the column byte-for-byte.
const effectiveGrantKeyExpr = "subject_type || char(1) || subject_id || char(1) || role"

// effectiveRoleRow is the db-tagged effective-grant listing projection ScanStruct
// scans into. IsDirect/IsGlobal are the MAX(CASE …) provenance flags (1/0);
// GrantKey is the derived keyset key (see effectiveRolesBaseSQL).
type effectiveRoleRow struct {
	SubjectType string `db:"subject_type"`
	SubjectID   string `db:"subject_id"`
	Role        string `db:"role"`
	IsDirect    int    `db:"is_direct"`
	IsGlobal    int    `db:"is_global"`
	GrantKey    string `db:"grant_key"`
}

func (r effectiveRoleRow) toDomain() role.EffectiveGrant {
	return role.EffectiveGrant{
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
// is the SQL literal for "the request is scoped": 1 gates the global fallback in,
// 0 (a global request) collapses it out so every grant is Direct — mirroring
// HasRole's no-fallback path for an unscoped query. A global grant is NEVER
// rewritten as a scoped row; only its provenance is reported. The outer
// `WHERE 1 = 1` is load-bearing (see rolesBaseSQL): the inner GROUP BY subquery's
// WHERE trips turso's List WHERE-vs-AND substring check, so the appended keyset
// AND needs an outer WHERE to attach to.
func effectiveRolesBaseSQL(scopedLiteral string) string {
	return `SELECT subject_type, subject_id, role, is_direct, is_global, grant_key FROM (
	SELECT subject_type, subject_id, role,
		MAX(CASE WHEN resource_type = ? AND resource_id = ? THEN 1 ELSE 0 END) AS is_direct,
		MAX(CASE WHEN ` + scopedLiteral + ` AND resource_type = '' AND resource_id = '' THEN 1 ELSE 0 END) AS is_global,
		` + effectiveGrantKeyExpr + ` AS grant_key
	FROM iam_roles
	WHERE (resource_type = ? AND resource_id = ?)
	   OR (` + scopedLiteral + ` AND resource_type = '' AND resource_id = '')
	GROUP BY subject_type, subject_id, role
) AS r WHERE 1 = 1`
}

// rolesBaseSQL wraps the filtered iam_roles rows in a derived table exposing the
// computed role_key column, so the connector's keyset builder can reference it in
// the outer WHERE (a WHERE cannot see a same-level SELECT alias) and ORDER BY as
// a plain identifier — the strict List helper rejects a raw-expression PK. The
// trailing `WHERE 1 = 1` is load-bearing: turso's List picks WHERE-vs-AND by
// substring, the inner subquery's WHERE trips it, and the appended keyset `AND
// (…)` would be a syntax error without an outer WHERE to attach to.
func rolesBaseSQL(innerWhere string) string {
	return `SELECT ` + roleColumns + `, role_key FROM (
	SELECT ` + roleColumns + `, ` + roleKeyExpr + ` AS role_key
	FROM iam_roles` + innerWhere + `
) AS r WHERE 1 = 1`
}

// roleRow is the db-tagged iam_roles listing projection ScanStruct scans into.
// RoleKey is the derived keyset tiebreak (see rolesBaseSQL) — scanned so PKOf can
// echo it rather than recompute it in Go.
type roleRow struct {
	SubjectType  string       `db:"subject_type"`
	SubjectID    string       `db:"subject_id"`
	Role         string       `db:"role"`
	ResourceType string       `db:"resource_type"`
	ResourceID   string       `db:"resource_id"`
	CreatedAt    tursodb.Time `db:"created_at"`
	RoleKey      string       `db:"role_key"`
}

func (r roleRow) toDomain() role.Assignment {
	return role.Assignment{
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
		Role:         r.Role,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		CreatedAt:    r.CreatedAt.Time,
	}
}

// roleStore fills role.Storer over iam_roles.
type roleStore struct {
	db *tursodb.DB
}

func newRoleStore(db *tursodb.DB) *roleStore {
	return &roleStore{db: db}
}

var _ role.Storer = (*roleStore)(nil)

// Assign inserts an assignment idempotently via the TARGETED ON CONFLICT DO
// NOTHING on the 5-tuple index (never INSERT OR IGNORE, which would swallow a NOT
// NULL breach too). A duplicate is a no-op that retains the original store-stamped
// created_at.
func (s *roleStore) Assign(ctx context.Context, a role.Assignment) error {
	const q = `INSERT INTO iam_roles (` + roleColumns + `)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(subject_type, subject_id, role, resource_type, resource_id) DO NOTHING`
	_, err := s.db.Exec(ctx, q,
		a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID,
		tursodb.FormatTime(time.Now().UTC()),
	)
	return err
}

// Unassign removes an exact assignment (idempotent — zero rows deleted is nil).
func (s *roleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_roles WHERE subject_type = ? AND subject_id = ? AND role = ? AND resource_type = ? AND resource_id = ?`
	if _, err := tursodb.ExecAffecting(ctx, s.db, q, subjectType, subjectID, roleName, resourceType, resourceID); err != nil {
		return err
	}
	return nil
}

// HasExactRole reports whether an assignment exists at the EXACT scope. The
// global-fallback rule (a global grant satisfies a scoped check) is the service's,
// never the store's.
func (s *roleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM iam_roles WHERE subject_type = ? AND subject_id = ? AND role = ? AND resource_type = ? AND resource_id = ?)`
	return existsQuery(ctx, s.db, q, subjectType, subjectID, roleName, resourceType, resourceID)
}

// ListBySubject pages a subject's assignments (created_at DESC, role_key DESC).
func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := tursodb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(" WHERE subject_type = ? AND subject_id = ?"),
		Args:         []any{subjectType, subjectID},
		OrderFields:  role.OrderFields,
		DefaultOrder: role.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleRow.toDomain), nil
}

// ListByResource is the RAW direct-scope listing: it pages the assignments stored
// exactly at (resourceType, resourceID) and never surfaces globally-granted
// subjects. Use ListEffectiveByResource for the enumeration that agrees with the
// service's HasRole fallback.
func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := tursodb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(" WHERE resource_type = ? AND resource_id = ?"),
		Args:         []any{resourceType, resourceID},
		OrderFields:  role.OrderFields,
		DefaultOrder: role.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.CreatedAt.Time },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleRow.toDomain), nil
}

// ListEffectiveByResource pages the EFFECTIVE role grants on a resource: the union
// of the direct scoped assignments with the global assignments a scoped HasRole
// satisfies, de-duplicated by (subject, role) with provenance, ordered by the
// derived grant_key ascending. A global grant is never rewritten as a scoped row.
func (s *roleStore) ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error) {
	scopedLiteral := "0"
	if resourceType != "" || resourceID != "" {
		scopedLiteral = "1"
	}
	q := tursodb.ListQuery[effectiveRoleRow]{
		BaseSQL:      effectiveRolesBaseSQL(scopedLiteral),
		Args:         []any{resourceType, resourceID, resourceType, resourceID},
		OrderFields:  role.EffectiveOrderFields,
		DefaultOrder: role.DefaultEffectiveOrder,
		PK:           "grant_key",
		OrderValueOf: func(r effectiveRoleRow, _ string) any { return r.GrantKey },
		PKOf:         func(r effectiveRoleRow) string { return r.GrantKey },
	}
	page, err := tursodb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[role.EffectiveGrant]{}, err
	}
	return crud.MapPage(page, effectiveRoleRow.toDomain), nil
}
