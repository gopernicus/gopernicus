package pgx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

func tupleFixture(t *testing.T) (*pgxdb.DB, config, tuplecache.Source) {
	t.Helper()
	db, cfg := cacheFixture(t, true)
	repos, err := Repositories(context.Background(), db, cacheOptions(cfg)...)
	if err != nil {
		t.Fatal(err)
	}
	if repos.Relationships.(interface{ TupleCacheBinding() string }).TupleCacheBinding() != repos.TupleSource.Binding() {
		t.Fatal("reader/source bindings differ")
	}
	return db, cfg, repos.TupleSource
}
func tupleSnapshot(t *testing.T, s tuplecache.Source, receipt string) tuplecache.Snapshot {
	t.Helper()
	snapshot, err := s.Snapshot(context.Background(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func ackSnapshot(t *testing.T, s tuplecache.Source, snapshot tuplecache.Snapshot, next string) {
	t.Helper()
	ids := make([]string, len(snapshot.Changes))
	for i, c := range snapshot.Changes {
		ids[i] = c.ID
	}
	if err := s.Acknowledge(context.Background(), snapshot.Receipt, next, ids); err != nil {
		t.Fatal(err)
	}
}
func tupleExec(t *testing.T, q pgxdb.Querier, query string) {
	t.Helper()
	if _, err := q.Exec(context.Background(), query); err != nil {
		t.Fatal(err)
	}
}
func tupleInsert(table, id string) string {
	return "INSERT INTO " + table + " (resource_type,resource_id,relation,subject_type,subject_id,subject_relation) VALUES ('document','" + id + "','viewer','user','alice','')"
}

func TestTupleSourceCaptureAndRecovery(t *testing.T) {
	ctx := context.Background()
	db, cfg, source := tupleFixture(t)
	table := cacheTable(cfg, "iam_relationships")
	snapshot := tupleSnapshot(t, source, "")
	if !snapshot.Full || len(snapshot.Tuples) != 0 {
		t.Fatalf("empty rebuild: %+v", snapshot)
	}
	ackSnapshot(t, source, snapshot, "initial")
	tupleExec(t, db, tupleInsert(table, "one"))
	tupleExec(t, db, tupleInsert(table, "one")+" ON CONFLICT DO NOTHING")
	tupleExec(t, db, "UPDATE "+table+" SET subject_id=subject_id")
	snapshot = tupleSnapshot(t, source, "initial")
	original := relationships.CreateRelationship{ResourceType: "document", ResourceID: "one", Relation: "viewer", SubjectType: "user", SubjectID: "alice"}
	if snapshot.Full || len(snapshot.Changes) != 1 || snapshot.Changes[0].Before != nil || !reflect.DeepEqual(snapshot.Changes[0].After, &original) {
		t.Fatalf("insert/noop: %+v", snapshot)
	}
	ackSnapshot(t, source, snapshot, "inserted")
	// Update all six tuple fields and capture both exact identities.
	tupleExec(t, db, "UPDATE "+table+" SET resource_type='space',resource_id='two',relation='member',subject_type='team',subject_id='engineering',subject_relation='member'")
	snapshot = tupleSnapshot(t, source, "inserted")
	updated := relationships.CreateRelationship{ResourceType: "space", ResourceID: "two", Relation: "member", SubjectType: "team", SubjectID: "engineering", SubjectRelation: "member"}
	if len(snapshot.Changes) != 1 || !reflect.DeepEqual(snapshot.Changes[0].Before, &original) || !reflect.DeepEqual(snapshot.Changes[0].After, &updated) {
		t.Fatalf("update: %+v", snapshot)
	}
	ackSnapshot(t, source, snapshot, "updated")
	// Processed history is gone; recovery comes from current facts.
	snapshot = tupleSnapshot(t, source, "")
	if !snapshot.Full || len(snapshot.Changes) != 0 || !reflect.DeepEqual(snapshot.Tuples, []relationships.CreateRelationship{updated}) {
		t.Fatalf("rebuild without history: %+v", snapshot)
	}
	snapshot = tupleSnapshot(t, source, "restored-old-receipt")
	if !snapshot.Full || len(snapshot.Tuples) != 1 {
		t.Fatal("stale restored receipt did not rebuild")
	}
	injected := errors.New("rollback")
	if err := db.InTx(ctx, func(tx *pgxdb.Tx) error { tupleExec(t, tx, "DELETE FROM "+table); return injected }); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if snapshot = tupleSnapshot(t, source, "updated"); len(snapshot.Changes) != 0 {
		t.Fatal("rollback leaked work")
	}
	tupleExec(t, db, "DELETE FROM "+table)
	snapshot = tupleSnapshot(t, source, "updated")
	if len(snapshot.Changes) != 1 || !reflect.DeepEqual(snapshot.Changes[0].Before, &updated) || snapshot.Changes[0].After != nil {
		t.Fatalf("delete: %+v", snapshot)
	}
	ids := []string{snapshot.Changes[0].ID}
	if err := source.Acknowledge(ctx, "wrong", "next", ids); !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("stale ack: %v", err)
	}
	if got := tupleSnapshot(t, source, "updated"); len(got.Changes) != 1 {
		t.Fatal("failed ack deleted pending work")
	}
	ackSnapshot(t, source, snapshot, "deleted")
	if err := source.Acknowledge(ctx, "updated", "deleted", ids); !errors.Is(err, tuplecache.ErrConflict) {
		t.Fatalf("duplicate ack: %v", err)
	}
}

func TestTupleSourceCommittedVisibilityAndExactAcknowledgements(t *testing.T) {
	ctx := context.Background()
	db, cfg, source := tupleFixture(t)
	ackSnapshot(t, source, tupleSnapshot(t, source, ""), "initial")
	table := cacheTable(cfg, "iam_relationships")
	first, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	tupleExec(t, first, tupleInsert(table, "slow"))
	var slowID int64
	if err := first.QueryRow(ctx, "SELECT id FROM "+cacheTable(cfg, "iam_tuple_outbox")).Scan(&slowID); err != nil {
		t.Fatal(err)
	}
	// Different tuples do not require this transaction to commit in ID order.
	tupleExec(t, db, tupleInsert(table, "fast"))
	fast := tupleSnapshot(t, source, "initial")
	if fast.Full || len(fast.Changes) != 1 || fast.Changes[0].After.ResourceID != "fast" {
		t.Fatalf("uncommitted change visible: %+v", fast)
	}
	fastID, _ := strconv.ParseInt(fast.Changes[0].ID, 10, 64)
	if fastID <= slowID {
		t.Fatal("fixture did not allocate IDs out of commit order")
	}
	ackSnapshot(t, source, fast, "fast")
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	slow := tupleSnapshot(t, source, "fast")
	if len(slow.Changes) != 1 || slow.Changes[0].ID != strconv.FormatInt(slowID, 10) {
		t.Fatalf("late lower ID lost: %+v", slow)
	}
	ackSnapshot(t, source, slow, "both")
	// All mutations in a committed transaction belong to one publication snapshot.
	if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
		tupleExec(t, tx, "DELETE FROM "+table)
		tupleExec(t, tx, tupleInsert(table, "replacement"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	batch := tupleSnapshot(t, source, "both")
	if len(batch.Changes) != 3 {
		t.Fatalf("committed transaction split: %+v", batch)
	}
}

func TestTupleSourceTruncateAndRoleWrites(t *testing.T) {
	db, cfg, source := tupleFixture(t)
	table := cacheTable(cfg, "iam_relationships")
	tupleExec(t, db, tupleInsert(table, "one"))
	ackSnapshot(t, source, tupleSnapshot(t, source, ""), "one")
	tupleExec(t, db, "INSERT INTO "+cacheTable(cfg, "iam_roles")+" (subject_type,subject_id,role,resource_type,resource_id) VALUES ('user','alice','admin','','')")
	if got := tupleSnapshot(t, source, "one"); len(got.Changes) != 0 {
		t.Fatal("role write generated tuple work")
	}
	tupleExec(t, db, "TRUNCATE "+table)
	tupleExec(t, db, tupleInsert(table, "after"))
	snapshot := tupleSnapshot(t, source, "one")
	if !snapshot.Full || len(snapshot.Changes) != 2 || len(snapshot.Tuples) != 1 || snapshot.Tuples[0].ResourceID != "after" {
		t.Fatalf("truncate rebuild: %+v", snapshot)
	}
	ackSnapshot(t, source, snapshot, "truncated")
	if got := tupleSnapshot(t, source, "truncated"); got.Full || len(got.Changes) != 0 {
		t.Fatalf("reset not acknowledged: %+v", got)
	}
}

func TestTupleSourceAmbientRejected(t *testing.T) {
	ctx := context.Background()
	db, _, source := tupleFixture(t)
	if err := db.Transact(ctx, func(ctx context.Context) error {
		if source.CacheableContext(ctx) {
			t.Fatal("ambient cacheable")
		}
		if _, err := source.Snapshot(ctx, ""); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient source snapshot: %v", err)
		}
		if err := source.Acknowledge(ctx, "", "next", nil); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient ack: %v", err)
		}
		if err := source.ReadSnapshot(ctx, func(context.Context, tuplecache.CheckReads) error { return nil }); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("ambient read snapshot: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTupleSourceInvokerPrivileges(t *testing.T) {
	ctx := context.Background()
	admin, cfg := cacheFixture(t, true)
	name := "tuple_invoker_" + fmt.Sprint(time.Now().UnixNano())
	tupleExec(t, admin, "CREATE ROLE "+name+" NOLOGIN")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP OWNED BY "+name+"; DROP ROLE "+name); err != nil {
			t.Error(err)
		}
	})
	schema := cacheSchemaName(cfg)
	for _, query := range []string{
		"GRANT USAGE ON SCHEMA " + schema + " TO " + name,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON " + cacheTable(cfg, "iam_relationships") + "," + cacheTable(cfg, "iam_roles") + " TO " + name,
		"GRANT SELECT ON " + cacheTable(cfg, "iam_audit") + "," + cacheTable(cfg, "iam_tuple_cache") + "," + cacheTable(cfg, "iam_tuple_outbox") + " TO " + name,
	} {
		tupleExec(t, admin, query)
	}
	db := cacheConnection(t, schema)
	tupleExec(t, db, "SET ROLE "+name)
	insert := tupleInsert(cacheTable(cfg, "iam_relationships"), "invoker")
	for _, grant := range []string{"", "GRANT INSERT ON " + cacheTable(cfg, "iam_tuple_outbox") + " TO " + name} {
		if grant != "" {
			tupleExec(t, admin, grant)
		}
		_, err := db.Exec(ctx, insert)
		if grant != "" {
			if err != nil {
				t.Fatalf("complete invoker permissions: %v", err)
			}
		} else if err == nil {
			t.Fatal("incomplete invoker permissions allowed mutation")
		}
	}
	if _, err := Repositories(ctx, db, cacheOptions(cfg)...); err != nil {
		t.Fatalf("least privilege constructor: %v", err)
	}
}

func TestTupleSourceMalformedPayloadAndMissingIdentity(t *testing.T) {
	for _, payload := range []string{`{"resource_type":"document"}`, `{"resource_type":"document","resource_id":"a","relation":"viewer","subject_type":"user","subject_id":"alice","subject_relation":null}`, `[]`} {
		t.Run(payload, func(t *testing.T) {
			db, cfg, source := tupleFixture(t)
			if _, err := db.Exec(t.Context(), "INSERT INTO "+cacheTable(cfg, "iam_tuple_outbox")+" (after_tuple) VALUES ($1::jsonb)", payload); err != nil {
				t.Fatal(err)
			}
			if _, err := source.Snapshot(t.Context(), ""); !errors.Is(err, tuplecache.ErrUnavailable) {
				t.Fatalf("malformed event: %v", err)
			}
		})
	}
	db, cfg, _ := tupleFixture(t)
	tupleExec(t, db, "DELETE FROM "+cacheTable(cfg, "iam_tuple_cache"))
	if _, err := db.Exec(t.Context(), tupleInsert(cacheTable(cfg, "iam_relationships"), "missing")); err == nil {
		t.Fatal("missing source identity allowed mutation")
	}
}

func TestTupleSourceAcknowledgementRollback(t *testing.T) {
	db, cfg, source := tupleFixture(t)
	tupleExec(t, db, tupleInsert(cacheTable(cfg, "iam_relationships"), "one"))
	before := tupleSnapshot(t, source, "")
	tupleExec(t, db, "CREATE FUNCTION "+cfg.schema.Table("reject_ack")+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected acknowledgement failure'; END; $$")
	tupleExec(t, db, "CREATE TRIGGER reject_ack BEFORE DELETE ON "+cacheTable(cfg, "iam_tuple_outbox")+" FOR EACH ROW EXECUTE FUNCTION "+cfg.schema.Table("reject_ack")+"()")
	if err := source.Acknowledge(t.Context(), "", "next", []string{before.Changes[0].ID}); err == nil {
		t.Fatal("failed delete ignored")
	}
	if got := tupleSnapshot(t, source, ""); !reflect.DeepEqual(before, got) {
		t.Fatalf("failed acknowledgement committed receipt: %+v", got)
	}
}
