package pgx

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const auditPurgeSignature = auditPurgeFunction + auditPurgeFunctionArgs

// TestAuditRetentionLive proves migration 0008 against a real database with a
// migrator that owns the tables and the definer, and a runtime role holding
// only its grants: the runtime role still cannot delete iam_audit directly;
// without EXECUTE the purge maps to the GRANT fix; with it the definer deletes
// past 0006 and appends one iam_audit_purges row per call; the runtime role
// cannot touch iam_audit_purges; and the production posture probe accepts the
// definer, bare and under WithSchema.
func TestAuditRetentionLive(t *testing.T) {
	f := newImmutableFixture(t, true)
	for _, statement := range []string{
		`ALTER FUNCTION ` + auditPurgeSignature + ` OWNER TO "` + f.migrator + `"`,
		`ALTER TABLE iam_audit_purges OWNER TO "` + f.migrator + `"`,
	} {
		tupleExec(t, f.admin, statement)
	}
	runtime := f.runtime(t, "")
	repos, err := Repositories(t.Context(), runtime, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	var add []tuples.Tuple
	for _, id := range []string{"old-a", "old-b", "new"} {
		add = append(add, tuples.Tuple{Scope: tuples.On("doc", id), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "u"}})
	}
	if err := repos.Tuples.ApplyTuples(auditContext(), tuples.Changes{Add: add}); err != nil {
		t.Fatal(err)
	}
	owner := f.owner(t)
	if tag, err := owner.Exec(t.Context(), `UPDATE iam_audit SET occurred_at = now() - interval '400 days' WHERE resource_id LIKE 'old-%'`); err != nil || tag.RowsAffected() != 2 {
		t.Fatalf("age two rows as the owner: %v %v", tag, err)
	}
	expectForbidden(t, runtime, `DELETE FROM iam_audit`)
	cutoff := time.Now().Add(-366 * 24 * time.Hour)

	_, err = repos.AuditExpiry.Expire(t.Context(), cutoff, 10)
	if !errors.Is(err, sdk.ErrForbidden) || !strings.Contains(err.Error(), "GRANT EXECUTE ON FUNCTION "+auditPurgeSignature) {
		t.Fatalf("Expire without EXECUTE: %v; want the mapped GRANT fix", err)
	}
	tupleExec(t, f.admin, `GRANT EXECUTE ON FUNCTION `+auditPurgeSignature+` TO "`+f.role+`"`)

	if _, err := repos.AuditExpiry.Expire(t.Context(), time.Now().Add(-100*24*time.Hour), 10); !errors.Is(err, audit.ErrBelowRetentionFloor) {
		t.Fatalf("young cutoff: %v", err)
	}
	if n, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); err != nil || n != 2 {
		t.Fatalf("Expire as the runtime role: %d %v", n, err)
	}
	if got := queryText(t, f.admin, `SELECT string_agg(resource_id, ',' ORDER BY resource_id) FROM iam_audit`); got != "new" {
		t.Fatalf("audit after the purge: %s", got)
	}
	if n, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); err != nil || n != 0 {
		t.Fatalf("zero-count purge: %d %v", n, err)
	}
	if got := queryText(t, f.admin, `SELECT string_agg(purged || '/' || floor_days || '/' || max_rows || '/' || (session_role = '`+f.role+`')::text, ',' ORDER BY id) FROM iam_audit_purges`); got != "2/365/10/true,0/365/10/true" {
		t.Fatalf("evidence rows: %s", got)
	}
	expectForbidden(t, runtime,
		`INSERT INTO iam_audit_purges (cutoff, purged, floor_days, max_rows, session_role) VALUES (now(), 0, 365, 1, 'forged')`,
		`UPDATE iam_audit_purges SET purged = 9`,
		`DELETE FROM iam_audit_purges`,
		`TRUNCATE iam_audit_purges`,
	)
	if got := queryText(t, f.admin, `SELECT pg_catalog.array_to_string(proconfig, ';') FROM pg_catalog.pg_proc WHERE oid = '`+auditPurgeSignature+`'::pg_catalog.regprocedure`); got != "search_path=pg_catalog, "+f.schema+", pg_temp" {
		t.Fatalf("definer search_path: %q", got)
	}

	schema, err := pgxdb.NewSchema(f.schema)
	if err != nil {
		t.Fatal(err)
	}
	production := f.runtime(t, environment.ModeProduction)
	for _, opts := range [][]Option{{WithAudit()}, {WithAudit(), WithSchema(schema)}} {
		scoped, err := Repositories(t.Context(), production, opts...)
		if err != nil {
			t.Fatalf("production posture with the 0008 definer: %v", err)
		}
		if n, err := scoped.AuditExpiry.Expire(t.Context(), cutoff, 10); err != nil || n != 0 {
			t.Fatalf("Expire %v: %d %v", opts, n, err)
		}
	}

	tupleExec(t, f.admin, `REVOKE EXECUTE ON FUNCTION `+auditPurgeSignature+` FROM "`+f.role+`"`)
	if _, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); !errors.Is(err, sdk.ErrForbidden) || !strings.Contains(err.Error(), "GRANT EXECUTE") {
		t.Fatalf("Expire after REVOKE: %v", err)
	}
	tupleExec(t, f.admin, `DROP FUNCTION `+auditPurgeSignature)
	if _, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), auditRetentionMigration) {
		t.Fatalf("Expire without the function: %v", err)
	}
}
