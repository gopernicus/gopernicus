package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// roleKeyExpr is the SQL keyset tiebreak: the 5-tuple joined by chr(1). iam_roles
// has no surrogate id — the 5-tuple is the natural key. PostgreSQL forbids NUL in
// text (so turso's chr(0) cannot be used here); chr(1) is the postgres-safe
// separator. The value is DB-computed and echoed back by PKOf (never recomputed in
// Go), so the cursor PK always matches the column byte-for-byte and the separator
// choice need not match the turso sibling (cursors are backend-local).
const roleKeyExpr = "subject_type || chr(1) || subject_id || chr(1) || role || chr(1) || resource_type || chr(1) || resource_id"

// roleRow is the db-tagged projection of an iam_roles listing row. RoleKey is the
// derived keyset tiebreak (see rolesBaseSQL) — scanned so PKOf can echo it.
type roleRow struct {
	SubjectType  string    `db:"subject_type"`
	SubjectID    string    `db:"subject_id"`
	Role         string    `db:"role"`
	ResourceType string    `db:"resource_type"`
	ResourceID   string    `db:"resource_id"`
	CreatedAt    time.Time `db:"created_at"`
	RoleKey      string    `db:"role_key"`
}

func (r roleRow) toDomain() role.Assignment {
	return role.Assignment{
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
		Role:         r.Role,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		CreatedAt:    r.CreatedAt.UTC(),
	}
}

// rolesBaseSQL wraps the filtered iam_roles rows in a derived table exposing the
// computed role_key column, so the keyset builder can reference it in the outer
// WHERE (a WHERE cannot see a same-level SELECT alias) and ORDER BY. The trailing
// `WHERE 1 = 1` lets pgxdb.List append its keyset predicate with AND.
func rolesBaseSQL(innerWhere string) string {
	return `SELECT subject_type, subject_id, role, resource_type, resource_id, created_at, role_key FROM (
	SELECT subject_type, subject_id, role, resource_type, resource_id, created_at, ` + roleKeyExpr + ` AS role_key
	FROM iam_roles` + innerWhere + `
) AS r WHERE 1 = 1`
}

// roleStore fills role.Storer over iam_roles.
type roleStore struct {
	db *pgxdb.DB
}

func newRoleStore(db *pgxdb.DB) *roleStore {
	return &roleStore{db: db}
}

var _ role.Storer = (*roleStore)(nil)

// Assign inserts an assignment idempotently via the TARGETED ON CONFLICT DO
// NOTHING on the 5-tuple index (never a bare OR-IGNORE-style suppression: a NOT
// NULL breach still raises). A duplicate is a no-op that retains the original
// store-stamped created_at.
func (s *roleStore) Assign(ctx context.Context, a role.Assignment) error {
	const q = `INSERT INTO iam_roles (subject_type, subject_id, role, resource_type, resource_id, created_at)
VALUES (@subject_type, @subject_id, @role, @resource_type, @resource_id, @created_at)
ON CONFLICT (subject_type, subject_id, role, resource_type, resource_id) DO NOTHING`
	_, err := s.db.Exec(ctx, q, pgx.NamedArgs{
		"subject_type":  a.SubjectType,
		"subject_id":    a.SubjectID,
		"role":          a.Role,
		"resource_type": a.ResourceType,
		"resource_id":   a.ResourceID,
		"created_at":    time.Now().UTC(),
	})
	return err
}

// Unassign removes an exact assignment (idempotent — zero rows deleted is nil).
func (s *roleStore) Unassign(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) error {
	const q = `DELETE FROM iam_roles WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id`
	if _, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"subject_type":  subjectType,
		"subject_id":    subjectID,
		"role":          roleName,
		"resource_type": resourceType,
		"resource_id":   resourceID,
	}); err != nil {
		return err
	}
	return nil
}

// HasExactRole reports whether an assignment exists at the EXACT scope. The
// global-fallback rule (a global grant satisfies a scoped check) is the service's,
// never the store's.
func (s *roleStore) HasExactRole(ctx context.Context, subjectType, subjectID, roleName, resourceType, resourceID string) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM iam_roles WHERE subject_type = @subject_type AND subject_id = @subject_id AND role = @role AND resource_type = @resource_type AND resource_id = @resource_id)`
	var ok bool
	if err := s.db.QueryRow(ctx, q, pgx.NamedArgs{
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

// ListBySubject pages a subject's assignments (created_at DESC, 5-tuple DESC).
func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := pgxdb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(" WHERE subject_type = @subject_type AND subject_id = @subject_id"),
		Args:         pgx.NamedArgs{"subject_type": subjectType, "subject_id": subjectID},
		OrderFields:  role.OrderFields,
		DefaultOrder: role.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleRow.toDomain), nil
}

// ListByResource pages the assignments scoped to a resource — DIRECT-scope only
// (it never surfaces globally-granted subjects the service would allow).
func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := pgxdb.ListQuery[roleRow]{
		BaseSQL:      rolesBaseSQL(" WHERE resource_type = @resource_type AND resource_id = @resource_id"),
		Args:         pgx.NamedArgs{"resource_type": resourceType, "resource_id": resourceID},
		OrderFields:  role.OrderFields,
		DefaultOrder: role.DefaultOrder,
		PK:           "role_key",
		OrderValueOf: func(r roleRow, _ string) any { return r.CreatedAt },
		PKOf:         func(r roleRow) string { return r.RoleKey },
	}
	page, err := pgxdb.List(ctx, s.db, q, req)
	if err != nil {
		return crud.Page[role.Assignment]{}, err
	}
	return crud.MapPage(page, roleRow.toDomain), nil
}
