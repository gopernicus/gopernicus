package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
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

// ListByResource pages the assignments scoped to a resource — DIRECT-scope only
// (it never surfaces globally-granted subjects the service would allow).
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
