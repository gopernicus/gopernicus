package turso

import (
	"context"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/role"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// roleColumns is the iam_roles projection in a fixed order shared by the listings
// and scanAssignment.
const roleColumns = "subject_type, subject_id, role, resource_type, resource_id, created_at"

// roleTiebreak is the SQL keyset tiebreak: the 5-tuple joined by char(0) (a NUL
// byte), byte-for-byte equal to roleAssignmentKey's strings.Join so a cursor's PK
// compares against exactly this expression. iam_roles has no surrogate id — the
// 5-tuple is the natural key.
const roleTiebreak = "subject_type || char(0) || subject_id || char(0) || role || char(0) || resource_type || char(0) || resource_id"

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

// ListBySubject pages a subject's assignments (created_at DESC, 5-tuple DESC).
func (s *roleStore) ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := tursodb.ListQuery[role.Assignment]{
		BaseSQL:      `SELECT ` + roleColumns + ` FROM iam_roles WHERE subject_type = ? AND subject_id = ?`,
		Args:         []any{subjectType, subjectID},
		OrderFields:  orderFields,
		DefaultOrder: defaultOrder,
		PK:           roleTiebreak,
		Scan:         scanAssignment,
		OrderValueOf: func(a role.Assignment, _ string) any { return a.CreatedAt },
		PKOf:         roleAssignmentKey,
	}
	return tursodb.List(ctx, s.db, q, req)
}

// ListByResource pages the assignments scoped to a resource — DIRECT-scope only
// (it never surfaces globally-granted subjects the service would allow).
func (s *roleStore) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error) {
	q := tursodb.ListQuery[role.Assignment]{
		BaseSQL:      `SELECT ` + roleColumns + ` FROM iam_roles WHERE resource_type = ? AND resource_id = ?`,
		Args:         []any{resourceType, resourceID},
		OrderFields:  orderFields,
		DefaultOrder: defaultOrder,
		PK:           roleTiebreak,
		Scan:         scanAssignment,
		OrderValueOf: func(a role.Assignment, _ string) any { return a.CreatedAt },
		PKOf:         roleAssignmentKey,
	}
	return tursodb.List(ctx, s.db, q, req)
}

// roleAssignmentKey is the Go twin of roleTiebreak: the 5-tuple joined by a NUL
// byte, the cursor PK the SQL keyset predicate compares against.
func roleAssignmentKey(a role.Assignment) string {
	return strings.Join([]string{a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID}, "\x00")
}

// scanAssignment scans one iam_roles row into a role.Assignment.
func scanAssignment(sc scanner) (role.Assignment, error) {
	var (
		a         role.Assignment
		createdAt string
	)
	if err := sc.Scan(&a.SubjectType, &a.SubjectID, &a.Role, &a.ResourceType, &a.ResourceID, &createdAt); err != nil {
		return role.Assignment{}, tursodb.MapError(err)
	}
	t, err := tursodb.ParseTime(createdAt)
	if err != nil {
		return role.Assignment{}, err
	}
	a.CreatedAt = t
	return a, nil
}
