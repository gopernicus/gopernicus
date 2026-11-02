package turso

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

const auditPurgesSummary = `SELECT purged || '/' || floor_days || '/' || max_rows FROM main.iam_audit_purges ORDER BY id`

// auditExpiryFixture is a migrated local database with three recorded changes,
// old-a and old-b aged past the 365-day floor.
func auditExpiryFixture(t *testing.T) (*tursodb.DB, storetest.Repositories) {
	t.Helper()
	db := canonicalFixture(t, false)
	repos, err := testRepositories(t.Context(), db, WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	ctx := audit.WithSource(t.Context(), audit.Source{System: "audit-expiry"})
	var add []tuples.Tuple
	for _, id := range []string{"old-a", "old-b", "new"} {
		add = append(add, tuples.Tuple{Scope: tuples.On("doc", id), Relation: "viewer", Subject: tuples.SubjectRef{Type: "user", ID: "u"}})
	}
	if err := repos.Tuples.ApplyTuples(ctx, tuples.Changes{Add: add}); err != nil {
		t.Fatal(err)
	}
	ids, err := queryStrings(t.Context(), db, `SELECT id FROM main.iam_audit WHERE resource_id LIKE 'old-%' ORDER BY id`)
	if err != nil || len(ids) != 2 {
		t.Fatalf("fixture ids: %v %v", ids, err)
	}
	for _, id := range ids {
		repos.SetAuditOccurredAt(t, id, time.Now().Add(-400*24*time.Hour))
	}
	return db, repos
}

func auditTriggerSet(t *testing.T, db *tursodb.DB) []auditTrigger {
	t.Helper()
	got, err := auditTriggers(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func remainingAudit(t *testing.T, db *tursodb.DB) []string {
	t.Helper()
	got, err := queryStrings(t.Context(), db, `SELECT resource_id FROM main.iam_audit ORDER BY resource_id`)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAuditExpiryRestoresTriggersAndRecordsEvidence(t *testing.T) {
	db, repos := auditExpiryFixture(t)
	before := auditTriggerSet(t, db)
	if len(before) != 2 {
		t.Fatalf("0006 triggers: %+v", before)
	}
	cutoff := time.Now().Add(-366 * 24 * time.Hour)
	if n, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); err != nil || n != 2 {
		t.Fatalf("Expire = %d %v, want 2", n, err)
	}
	if after := auditTriggerSet(t, db); !slices.Equal(after, before) {
		t.Fatalf("trigger set changed:\n%+v\nwant\n%+v", after, before)
	}
	if got := remainingAudit(t, db); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("remaining audit: %v", got)
	}
	if n, err := repos.AuditExpiry.Expire(t.Context(), cutoff, 10); err != nil || n != 0 {
		t.Fatalf("second Expire = %d %v, want 0", n, err)
	}
	if got, err := queryStrings(t.Context(), db, auditPurgesSummary); err != nil || !slices.Equal(got, []string{"2/365/10", "0/365/10"}) {
		t.Fatalf("evidence rows: %v %v", got, err)
	}
	if _, err := db.Exec(t.Context(), `DELETE FROM main.iam_audit`); !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("restored trigger did not refuse a direct delete: %v", err)
	}
	for _, statement := range []string{`UPDATE main.iam_audit_purges SET purged = 9`, `DELETE FROM main.iam_audit_purges`} {
		if _, err := db.Exec(t.Context(), statement); !errors.Is(err, sdk.ErrForbidden) {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := repos.AuditExpiry.Expire(t.Context(), time.Now().Add(-100*24*time.Hour), 10); !errors.Is(err, audit.ErrBelowRetentionFloor) {
		t.Fatalf("young cutoff: %v", err)
	}
	if got, err := queryStrings(t.Context(), db, auditPurgesSummary); err != nil || len(got) != 2 {
		t.Fatalf("a refused purge recorded evidence: %v %v", got, err)
	}
}

func TestAuditExpiryFaultRollsBack(t *testing.T) {
	for _, stage := range []string{stageDropped, stageRecorded} {
		t.Run(stage, func(t *testing.T) {
			db, repos := auditExpiryFixture(t)
			before := auditTriggerSet(t, db)
			injected := errors.New("injected fault")
			expirer := repos.AuditExpiry.(*auditExpiry)
			expirer.fault = func(_ context.Context, _ *tursodb.Tx, at string) error {
				if at == stage {
					return injected
				}
				return nil
			}
			if n, err := expirer.Expire(t.Context(), time.Now().Add(-366*24*time.Hour), 10); n != 0 || !errors.Is(err, injected) {
				t.Fatalf("faulted Expire = %d %v", n, err)
			}
			if after := auditTriggerSet(t, db); !slices.Equal(after, before) {
				t.Fatalf("a rolled-back purge changed the trigger set: %+v", after)
			}
			if got := remainingAudit(t, db); !slices.Equal(got, []string{"new", "old-a", "old-b"}) {
				t.Fatalf("a rolled-back purge deleted history: %v", got)
			}
			if got, err := queryStrings(t.Context(), db, auditPurgesSummary); err != nil || len(got) != 0 {
				t.Fatalf("a rolled-back purge recorded evidence: %v %v", got, err)
			}
		})
	}
}

// A host that declined or renamed a 0006 trigger keeps exactly what it had: the
// purge restores what it captured, never the embedded text.
func TestAuditExpiryRestoresWhatItCaptured(t *testing.T) {
	db, repos := auditExpiryFixture(t)
	if _, err := db.Exec(t.Context(), `DROP TRIGGER main.iam_audit_immutable_update`); err != nil {
		t.Fatal(err)
	}
	before := auditTriggerSet(t, db)
	if n, err := repos.AuditExpiry.Expire(t.Context(), time.Now().Add(-366*24*time.Hour), 10); err != nil || n != 2 {
		t.Fatalf("Expire = %d %v", n, err)
	}
	if after := auditTriggerSet(t, db); !slices.Equal(after, before) || len(after) != 1 || after[0].name != "iam_audit_immutable_delete" {
		t.Fatalf("trigger set after the purge: %+v", after)
	}
}

func TestAuditExpiryRefusesMissingFloor(t *testing.T) {
	db, repos := auditExpiryFixture(t)
	for _, step := range []string{`DELETE FROM main.iam_audit_retention`, `DROP TABLE main.iam_audit_retention`} {
		if _, err := db.Exec(t.Context(), step); err != nil {
			t.Fatal(err)
		}
		_, err := repos.AuditExpiry.Expire(t.Context(), time.Now().Add(-366*24*time.Hour), 10)
		if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), auditRetentionMigration) {
			t.Fatalf("after %s: %v; want sdk.ErrNotFound naming %s", step, err, auditRetentionMigration)
		}
	}
	if got := remainingAudit(t, db); len(got) != 3 {
		t.Fatalf("a refused purge deleted history: %v", got)
	}
}

func TestAuditExpiryRefusesAmbientTransaction(t *testing.T) {
	db, repos := auditExpiryFixture(t)
	err := db.Transact(t.Context(), func(ctx context.Context) error {
		_, err := repos.AuditExpiry.Expire(ctx, time.Now().Add(-366*24*time.Hour), 10)
		return err
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Expire inside an ambient transaction: %v", err)
	}
}
