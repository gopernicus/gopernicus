package pgx

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestCanonicalConstructorsRejectPartialSchemas(t *testing.T) {
	for name, statement := range map[string]string{
		"missing primary key":          `ALTER TABLE iam_tuples DROP CONSTRAINT iam_tuples_pkey`,
		"partial identity":             `ALTER TABLE iam_tuples DROP CONSTRAINT iam_tuples_pkey; ALTER TABLE iam_tuples ADD PRIMARY KEY(scope_kind, resource_type, resource_id, subject_type, subject_id, subject_relation)`,
		"missing column":               `ALTER TABLE iam_tuples DROP COLUMN subject_relation CASCADE`,
		"wrong type":                   `ALTER TABLE iam_tuples ALTER COLUMN scope_kind TYPE bigint`,
		"tuple integer scope":          `ALTER TABLE iam_tuples ALTER COLUMN scope_kind TYPE integer`,
		"audit integer scope":          `ALTER TABLE iam_audit ALTER COLUMN scope_kind TYPE integer`,
		"nullable relation":            `ALTER TABLE iam_tuples DROP CONSTRAINT iam_tuples_pkey; ALTER TABLE iam_tuples ALTER COLUMN relation DROP NOT NULL`,
		"wrong collation":              `ALTER TABLE iam_tuples ALTER COLUMN relation TYPE text COLLATE "default"`,
		"missing scope check":          `ALTER TABLE iam_tuples DROP CONSTRAINT ck_iam_tuples_scope`,
		"same name permissive check":   `ALTER TABLE iam_tuples DROP CONSTRAINT ck_iam_tuples_scope; ALTER TABLE iam_tuples ADD CONSTRAINT ck_iam_tuples_scope CHECK(true)`,
		"unvalidated check":            `ALTER TABLE iam_tuples DROP CONSTRAINT ck_iam_tuples_refs; ALTER TABLE iam_tuples ADD CONSTRAINT ck_iam_tuples_refs CHECK(relation<>'' AND subject_type<>'' AND subject_id<>'') NOT VALID`,
		"exclusive label index":        `CREATE UNIQUE INDEX restrictive_exclusivity ON iam_tuples(scope_kind,resource_type,resource_id,subject_type,subject_id,subject_relation)`,
		"row security":                 `ALTER TABLE iam_tuples ENABLE ROW LEVEL SECURITY`,
		"audit missing encoding":       `ALTER TABLE iam_audit DROP COLUMN encoding CASCADE`,
		"audit missing encoding check": `ALTER TABLE iam_audit DROP CONSTRAINT ck_iam_audit_encoding`,
		"audit bad source check":       `ALTER TABLE iam_audit DROP CONSTRAINT ck_iam_audit_source; ALTER TABLE iam_audit ADD CONSTRAINT ck_iam_audit_source CHECK(true)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			if _, err := db.Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("bundle accepted altered schema: %v", err)
			}
			if _, err := RelationshipRepository(t.Context(), db, WithAudit()); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("facade accepted altered schema: %v", err)
			}
		})
	}
}
