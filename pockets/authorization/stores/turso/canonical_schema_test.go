package turso

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestCanonicalConstructorsRejectPartialSchemas(t *testing.T) {
	for _, tc := range []struct{ name, table, from, to string }{
		{"missing primary key", "iam_tuples", "PRIMARY KEY (scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation),", ""},
		{"partial identity", "iam_tuples", "PRIMARY KEY (scope_kind, resource_type, resource_id, relation, subject_type, subject_id, subject_relation)", "PRIMARY KEY (scope_kind, resource_type, resource_id, subject_type, subject_id, subject_relation)"},
		{"missing column", "iam_audit", "encoding TEXT NOT NULL CONSTRAINT ck_iam_audit_encoding CHECK (encoding = 'tuple/v2'),", ""},
		{"wrong type", "iam_tuples", "scope_kind INTEGER", "scope_kind TEXT"},
		{"nullable column", "iam_tuples", "relation TEXT COLLATE BINARY NOT NULL", "relation TEXT COLLATE BINARY"},
		{"wrong collation", "iam_tuples", "COLLATE BINARY", "COLLATE NOCASE"},
		{"permissive refs", "iam_tuples", "CHECK (relation <> '' AND subject_type <> '' AND subject_id <> '')", "CHECK (true)"},
		{"permissive scope", "iam_tuples", "scope_kind = 2 AND resource_type <> '' AND resource_id <> ''", "scope_kind=2"},
		{"audit encoding check", "iam_audit", "CHECK (encoding = 'tuple/v2')", "CHECK (true)"},
		{"audit source check", "iam_audit", "actor_type<>'' AND actor_id<>'' AND system_source=''", "actor_type<>''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			var ddl string
			if err := db.QueryRow(t.Context(), `SELECT sql FROM main.sqlite_schema WHERE name=?`, tc.table).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ddl, tc.from) {
				t.Fatalf("test replacement absent: %s", ddl)
			}
			ddl = strings.Replace(ddl, tc.from, tc.to, 1)
			if _, err := db.Exec(t.Context(), "DROP TABLE main."+tc.table+"; "+ddl); err != nil {
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
	for name, statement := range map[string]string{
		"exclusive label index": `CREATE UNIQUE INDEX main.restrictive_exclusivity ON iam_tuples(scope_kind,resource_type,resource_id,subject_type,subject_id,subject_relation)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := canonicalFixture(t, false)
			if _, err := db.Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("accepted restrictive uniqueness: %v", err)
			}
		})
	}
}

func TestCanonicalUncachedIgnoresTempShadows(t *testing.T) {
	db, _ := cacheFixture(t, false, 1)
	if _, err := db.Exec(t.Context(), `CREATE TEMP TABLE iam_tuples AS SELECT * FROM main.iam_tuples;
INSERT INTO temp.iam_tuples VALUES (1,'','','admin','user','shadow','');`); err != nil {
		t.Fatal(err)
	}
	repos, err := testRepositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	fact := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "shadow"}}
	if allowed, err := repos.Tuples.Contains(t.Context(), fact); err != nil || allowed {
		t.Fatalf("TEMP granted authority: %v/%v", allowed, err)
	}
	if _, err := db.Exec(t.Context(), `DROP TABLE main.iam_tuples; CREATE TABLE main.iam_tuples(scope_kind INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("TEMP masked malformed main: %v", err)
	}
	if _, err := db.Exec(t.Context(), `DROP TABLE main.iam_tuples`); err != nil {
		t.Fatal(err)
	}
	if _, err := testRepositories(t.Context(), db); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("TEMP masked missing main: %v", err)
	}
}
