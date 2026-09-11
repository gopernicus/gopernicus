package pgx

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestAuditRawRelationshipDeltas(t *testing.T) {
	for _, method := range []string{"exact", "five-field", "resource-subject", "resource", "set"} {
		t.Run(method, func(t *testing.T) {
			_, repos := liveReposWith(t, WithAudit())
			ctx := auditContext()
			base := relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "group", SubjectID: "g"}
			member, admin := base, base
			member.SubjectRelation = "member"
			admin.SubjectRelation = "admin"
			seed := []relationships.CreateRelationship{base, member, admin}
			if err := repos.Relationships.CreateRelationships(ctx, seed); err != nil {
				t.Fatal(err)
			}
			if err := repos.Relationships.CreateRelationships(ctx, seed); err != nil {
				t.Fatal(err)
			}
			if got := len(auditRecords(t, repos)); got != 3 {
				t.Fatalf("duplicate insert recorded: %d", got)
			}
			if err := repos.Relationships.CreateRelationships(context.Background(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("enabled no-op needs source: %v", err)
			}
			var err error
			removed := 3
			added := 0
			switch method {
			case "exact":
				removed = 1
				err = repos.Relationships.DeleteRelationshipTarget(ctx, "doc", "d", "viewer", member.Subject())
			case "five-field":
				err = repos.Relationships.DeleteRelationship(ctx, "doc", "d", "viewer", "group", "g")
			case "resource-subject":
				err = repos.Relationships.DeleteByResourceAndSubject(ctx, "doc", "d", "group", "g")
			case "resource":
				err = repos.Relationships.DeleteResourceRelationships(ctx, "doc", "d")
			case "set":
				added = 1
				replacement := base
				replacement.SubjectID = "new"
				err = repos.Relationships.SetRelationTargets(ctx, "doc", "d", "viewer", []relationships.CreateRelationship{replacement})
			}
			if err != nil {
				t.Fatal(err)
			}
			records := auditRecords(t, repos)
			if len(records) != 3+removed+added {
				t.Fatalf("audit count=%d want=%d", len(records), 3+removed+added)
			}
			changes := records[:removed+added]
			event := changes[0].EventID
			removals := map[string]bool{}
			for _, r := range changes {
				if r.EventID != event || r.Source.System != "sql-test" || r.OccurredAt.Nanosecond()%1000 != 0 {
					t.Fatalf("event/source/time mismatch: %+v", r)
				}
				if r.Change.Action == audit.ActionRemoved {
					removals[r.Change.Relationship.SubjectRelation] = true
				}
			}
			if len(removals) != removed {
				t.Fatalf("lost exact userset identity: %+v", removals)
			}
		})
	}
}
func TestAuditRoleAndMutationDeltas(t *testing.T) {
	_, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	apply := func(cmd mutations.Command) {
		t.Helper()
		if _, err := repos.Mutations.Apply(ctx, cmd, nil); err != nil {
			t.Fatal(err)
		}
	}
	apply(grantCmd("d", "viewer", "alice"))
	replacement := grantCmd("d", "editor", "alice")
	replacement.Operation = mutations.OpReplace
	apply(replacement)
	if got := len(auditRecords(t, repos)); got != 3 {
		t.Fatalf("replace must record old removal+new addition: %d", got)
	}
	apply(replacement)
	if got := len(auditRecords(t, repos)); got != 3 {
		t.Fatalf("replace no-op recorded: %d", got)
	}
	scoped := roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "admin", ResourceType: "doc", ResourceID: "d"}
	global := scoped
	global.ResourceType = ""
	global.ResourceID = ""
	for _, a := range []roles.Assignment{scoped, global, global} {
		if err := repos.Roles.Assign(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := repos.Roles.Unassign(ctx, "user", "alice", "admin", "doc", "d"); err != nil {
		t.Fatal(err)
	}
	assign := mutations.Command{Target: scopeOf("doc", "d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "alice", Role: "admin"}}}
	apply(assign)
	teardown := mutations.Command{Target: scopeOf("doc", "d"), Operation: mutations.OpTeardown}
	apply(teardown)
	records := auditRecords(t, repos)
	if len(records) != 9 {
		t.Fatalf("role/teardown changes=%d want9: %+v", len(records), records)
	}
	if records[0].EventID != records[1].EventID {
		t.Fatal("teardown did not group relationship and scoped role removal")
	}
	if ok, err := repos.Roles.HasExactRole(ctx, "user", "alice", "admin", "", ""); err != nil || !ok {
		t.Fatalf("teardown removed global role: %v %v", ok, err)
	}
	apply(teardown)
	if got := len(auditRecords(t, repos)); got != 9 {
		t.Fatalf("teardown no-op recorded: %d", got)
	}
}
func TestAuditFailureRollsBackOwnedAndAmbientWrites(t *testing.T) {
	for _, ambient := range []bool{false, true} {
		for _, kind := range []string{"relationship", "role"} {
			t.Run(kind+map[bool]string{false: "/owned", true: "/ambient"}[ambient], func(t *testing.T) {
				db, repos := liveReposWith(t, WithAudit())
				ctx := auditContext()
				// Make the audit append fail after the fact statement. Restore before fixture cleanup.
				if _, err := db.Exec(ctx, `ALTER TABLE `+qualify(t, "iam_audit")+` RENAME TO iam_audit_saved`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := db.Exec(context.Background(), `ALTER TABLE `+qualify(t, "iam_audit_saved")+` RENAME TO iam_audit`); err != nil {
						t.Error(err)
					}
				})
				write := func(ctx context.Context) error {
					if kind == "role" {
						return repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "failed", Role: "reader"})
					}
					return repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{{ResourceType: "doc", ResourceID: "failed", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}})
				}
				if ambient {
					err := db.Transact(ctx, func(ctx context.Context) error {
						tx, _ := pgxdb.TxFromContext(ctx)
						if _, err := tx.Exec(ctx, `INSERT INTO `+qualify(t, "iam_roles")+` (subject_type,subject_id,role,resource_type,resource_id) VALUES ('user','host-marker','reader','','')`); err != nil {
							return err
						}
						if err := write(ctx); err == nil {
							t.Fatal("audit append should fail")
						}
						return nil // Host deliberately handles the operation error and commits its work.
					})
					if err != nil {
						t.Fatalf("savepoint did not preserve usable host transaction: %v", err)
					}
					if ok, err := repos.Roles.HasExactRole(ctx, "user", "host-marker", "reader", "", ""); err != nil || !ok {
						t.Fatalf("unrelated host work lost: %v %v", ok, err)
					}
				} else if err := write(ctx); err == nil {
					t.Fatal("audit append should fail")
				}
				if ok, err := repos.Relationships.CheckRelationExists(ctx, "doc", "failed", "viewer", "user", "alice"); err != nil || ok {
					t.Fatalf("failed audit left relationship: %v %v", ok, err)
				}
				if ok, err := repos.Roles.HasExactRole(ctx, "user", "failed", "reader", "", ""); err != nil || ok {
					t.Fatalf("failed audit left role: %v %v", ok, err)
				}
			})
		}
	}
}
func TestAuditAmbientRollbackAndDisabledReader(t *testing.T) {
	db, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	stop := errors.New("host rollback")
	err := db.Transact(ctx, func(ctx context.Context) error {
		if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "reader"}); err != nil {
			return err
		}
		page, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 10})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("ambient history invisible: %+v %v", page, err)
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if len(auditRecords(t, repos)) != 0 {
		t.Fatal("host rollback left history")
	}
	if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "reader"}); err != nil {
		t.Fatal(err)
	}
	off, err := Repositories(context.Background(), db, storeOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := off.Roles.Assign(context.Background(), roles.Assignment{SubjectType: "user", SubjectID: "bob", Role: "reader"}); err != nil {
		t.Fatal(err)
	}
	if records := auditRecords(t, off); len(records) != 1 || records[0].Change.Role.SubjectID != "alice" {
		t.Fatalf("disabled recording lost retained reader or wrote history: %+v", records)
	}
}
func TestAuditListingEqualTimestampPagination(t *testing.T) {
	db, repos := liveReposWith(t, WithAudit())
	ctx := auditContext()
	for _, id := range []string{"c", "a", "b"} {
		if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: id, Role: "reader"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 123456000, time.UTC)
	if _, err := db.Exec(ctx, `UPDATE `+qualify(t, "iam_audit")+` SET occurred_at=$1`, now); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		req := list.Request{Limit: 1, Order: list.NewOrder("occurred_at", direction)}
		var ids []string
		for i := 0; i < 5; i++ {
			page, err := repos.Audit.List(ctx, audit.Filter{}, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range page.Items {
				ids = append(ids, r.ID)
			}
			if !page.HasMore {
				break
			}
			if page.NextCursor == "" || page.NextCursor == req.Cursor {
				t.Fatal("cursor failed to advance")
			}
			req.Cursor = page.NextCursor
		}
		if len(ids) != 3 {
			t.Fatalf("cursor skipped tied rows: %v", ids)
		}
		for i := 1; i < len(ids); i++ {
			if (ids[i-1] < ids[i]) != (direction == list.ASC) {
				t.Fatalf("tie order %s: %v", direction, ids)
			}
		}
	}
	page, err := repos.Audit.List(ctx, audit.Filter{SubjectType: "user", SubjectID: "b"}, list.Request{Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Change.Role.SubjectID != "b" {
		t.Fatalf("subject filter %+v %v", page, err)
	}
	copy := page.Items[0]
	copy.Change.Role.SubjectID = "caller-change"
	again, err := repos.Audit.List(ctx, audit.Filter{SubjectType: "user", SubjectID: "b"}, list.Request{Limit: 10})
	if err != nil || again.Items[0].Change.Role.SubjectID != "b" {
		t.Fatal("returned fact ownership")
	}
}
func TestAuditUpgradeDropsLedgersPreservesFacts(t *testing.T) {
	ctx := context.Background()
	db := tupleUpgradeLegacy(t)
	if _, err := db.Exec(ctx, qualifySQL(t, `INSERT INTO iam_relationships (relationship_id,resource_type,resource_id,relation,subject_type,subject_id,subject_relation,created_at) VALUES ('legacy','doc','d','viewer','group','g','member','2026-01-01T00:00:00Z')`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, qualifySQL(t, `INSERT INTO iam_roles (subject_type,subject_id,role,resource_type,resource_id,created_at) VALUES ('user','alice','reader','doc','d','2026-01-01T00:00:00Z')`)); err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, name := range canonicalMigrations {
		if name >= "0007_" {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		before[MigrationsDir+"/"+name] = &fstest.MapFile{Data: data}
	}
	if err := pgxdb.RunMigrations(ctx, db, before, MigrationsDir, migrateOptions(t)...); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`INSERT INTO iam_scopes(scope_kind,scope_type,scope_id,revision) VALUES('resource','doc','d',8)`, `INSERT INTO iam_mutations(mutation_id,scope_kind,scope_type,scope_id,operation,payload_encoding,payload_digest,outcome,revision,schema_digest,created_at) VALUES('old-receipt','resource','doc','d','grant','v','digest','applied',8,'schema','2026-01-01T00:00:00Z')`} {
		if _, err := db.Exec(ctx, qualifySQL(t, statement)); err != nil {
			t.Fatal(err)
		}
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, migrateOptions(t)...); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(context.Background(), db, storeOptions(t)...)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", "d", "viewer")
	if err != nil || !reflect.DeepEqual(targets, []relationships.RelationTarget{{Type: "group", ID: "g", Relation: "member"}}) {
		t.Fatalf("upgrade lost exact tuple: %+v %v", targets, err)
	}
	if ok, err := repos.Roles.HasExactRole(ctx, "user", "alice", "reader", "doc", "d"); err != nil || !ok {
		t.Fatalf("upgrade lost role: %v %v", ok, err)
	}
	if len(auditRecords(t, repos)) != 0 {
		t.Fatal("upgrade invented actor/delta history from receipts")
	}
	for _, table := range []string{"iam_scopes", "iam_mutations"} {
		if err := pgxdb.ProbeTable(ctx, db, qualify(t, table)); err == nil {
			t.Fatalf("retired table remains: %s", table)
		}
	}
}

func TestAuditModelReaderRetainsRecordingConfiguration(t *testing.T) {
	_, repos := liveReposWith(t, WithAudit())
	bound := repos.Relationships.ForModel(relationships.NewReadModel(nil))
	writer := bound.(relationships.Storer)
	row := relationships.CreateRelationship{ResourceType: "doc", ResourceID: "d", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
	if err := writer.CreateRelationships(context.Background(), []relationships.CreateRelationship{row}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bound store lost source requirement: %v", err)
	}
	if err := writer.CreateRelationships(auditContext(), []relationships.CreateRelationship{row}); err != nil {
		t.Fatal(err)
	}
	if records := auditRecords(t, repos); len(records) != 1 {
		t.Fatalf("bound store lost audit: %+v", records)
	}
}
