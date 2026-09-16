package documents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	migrations "github.com/gopernicus/gopernicus/examples/auth-cms/workshop/migrations/documents"
	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	authzpgx "github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx"
	"github.com/gopernicus/gopernicus/sdk"
)

// This opt-in fixture creates only unique test schemas in the explicitly named
// scratch database. It never reuses the application's authorization schema.
func postgresFixture(t *testing.T) (*Postgres, *testAuthorizationStore, pgxdb.Schema) {
	t.Helper()
	dsn := os.Getenv("AUTHORIZATION_LISTING_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTHORIZATION_LISTING_TEST_DSN absent: live PostgreSQL listing unverified")
	}
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	authSchema, err := pgxdb.NewSchema("listing_auth_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	docSchema, err := pgxdb.NewSchema("listing_docs_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []pgxdb.Schema{authSchema, docSchema} {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := db.Exec(ctx, `DROP SCHEMA IF EXISTS "`+schema.String()+`" CASCADE`); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		})
	}
	if err := pgxdb.RunMigrations(t.Context(), db, authzpgx.MigrationsFS, authzpgx.MigrationsDir, pgxdb.WithSchema(authSchema)); err != nil {
		t.Fatal(err)
	}
	if err := pgxdb.RunMigrations(t.Context(), db, migrations.FS, migrations.Dir, pgxdb.WithSchema(docSchema)); err != nil {
		t.Fatal(err)
	}
	rel, err := authzpgx.RelationshipRepository(context.Background(), db, authzpgx.WithSchema(authSchema))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgres(db, docSchema)
	if err != nil {
		t.Fatal(err)
	}
	tuples, err := authzpgx.Repositories(context.Background(), db, authzpgx.WithSchema(authSchema))
	if err != nil {
		t.Fatal(err)
	}
	return store, &testAuthorizationStore{Storer: rel, Tuples: tuples.Tuples}, authSchema
}

func testSQLListing(t *testing.T, store *Postgres, schema pgxdb.Schema, az authorization.Components, bypass Bypass) *SQLListing {
	t.Helper()
	l, err := NewSQLListing(store, schema, az.Decisions, testCodec(t), bypass)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestPostgresListingHTTP(t *testing.T) {
	store, rel, schema := postgresFixture(t)
	for _, doc := range orderedFixture() {
		if err := store.Put(t.Context(), doc); err != nil {
			t.Fatal(err)
		}
	}
	grant(t, rel, "01", "90", "30", "20", "other", "unicode")
	az := testAuthorizer(t, rel, model.EvaluationLimits{})
	// Same business SQL over grants in another store is the portable deployment.
	remote := newMemoryAuthorizationStore()
	grant(t, remote, "01", "90", "30", "20", "other", "unicode")
	remoteAZ := testAuthorizer(t, remote, model.EvaluationLimits{})
	for _, tt := range []struct {
		name   string
		lister domain.Lister
	}{
		{"same_db_complete", testListing(t, store, az, CompleteSet, 0, nil)},
		{"same_db_candidates", testListing(t, store, az, Candidates, 3, nil)},
		{"different_stores_complete", testListing(t, store, remoteAZ, CompleteSet, 0, nil)},
		{"different_stores_candidates", testListing(t, store, remoteAZ, Candidates, 3, nil)},
		{"same_db_exists", testSQLListing(t, store, schema, az, nil)},
	} {
		t.Run(tt.name, func(t *testing.T) { verifyListingHTTP(t, tt.lister) })
	}
	// Duplicate creation remains idempotent, and EXISTS cannot multiply rows.
	grant(t, rel, "20", "20")
	sqlListing := testSQLListing(t, store, schema, az, nil)
	if got := walkHTTP(t, listingServer(t, sqlListing), alice.ID, domain.Query{TenantID: "a", Search: "beta", Limit: 1}); !slices.Equal(got, []string{"20", "30"}) {
		t.Fatalf("duplicate grants: %v", got)
	}
	bypass := testSQLListing(t, store, schema, az, func(context.Context, sdk.Principal) (bool, error) { return true, nil })
	if got := walkHTTP(t, listingServer(t, bypass), alice.ID, domain.Query{TenantID: "a", Search: "beta", Limit: 1}); !slices.Equal(got, []string{"20", "30"}) {
		t.Fatalf("SQL bypass removed tenant/search: %v", got)
	}
	// A machine is not an allowed concrete subject in this selected policy.
	page, err := sqlListing.ListVisible(t.Context(), sdk.Principal{Type: "machine", ID: alice.ID}, domain.Query{TenantID: "a", Limit: 2})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("unsupported principal gained access: %+v %v", page, err)
	}
}

func TestSQLListingRejectsUnsupportedModels(t *testing.T) {
	user := []decisions.SubjectTypeRef{{Type: "user"}}
	for _, tt := range []struct {
		name      string
		resources []decisions.ResourceSchema
	}{
		{"extra_or", []decisions.ResourceSchema{{Name: "document", Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"viewer": {AllowedSubjects: user}, "owner": {AllowedSubjects: user}},
			Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"), decisions.Direct("owner"))},
		}}}},
		{"userset", []decisions.ResourceSchema{
			{Name: "document", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "group", Relation: "member"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))}}},
			{Name: "group", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"member": {AllowedSubjects: user}}}},
		}},
		{"through", []decisions.ResourceSchema{
			{Name: "document", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"parent": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "folder"}}}}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Through("parent", "view"))}}},
			{Name: "folder", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: user}}, Permissions: map[string]decisions.Expression{"view": decisions.AnyOf(decisions.Direct("viewer"))}}},
		}},
		{"exact_role", []decisions.ResourceSchema{{Name: "document", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: user}}, Permissions: map[string]decisions.Expression{"view": decisions.RoleIn("viewer")}}}}},
		{"all", []decisions.ResourceSchema{{Name: "document", Def: decisions.ResourceTypeDef{Relations: map[string]decisions.RelationDef{"viewer": {AllowedSubjects: user}}, Permissions: map[string]decisions.Expression{"view": decisions.All(decisions.Direct("viewer"), decisions.Role("active"))}}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := memory.New()
			components, err := authorization.New(authorization.Repositories{Tuples: store.Tuples()}, authorization.WithModel(decisions.NewSchema(tt.resources)))
			if err != nil {
				t.Fatal(err)
			}
			// Construction validates policy before any SQL runs.
			if _, err := NewSQLListing(&Postgres{}, pgxdb.Schema{}, components.Decisions, testCodec(t), nil); err == nil {
				t.Fatal("unsupported permission accepted")
			}
		})
	}
}

func TestPostgresListingRevocationAndMoveBetweenPages(t *testing.T) {
	store, rel, schema := postgresFixture(t)
	for _, strategy := range []string{"complete", "candidates", "exists"} {
		t.Run(strategy, func(t *testing.T) {
			for _, doc := range []domain.Document{{ID: "a", TenantID: "a", Name: "a"}, {ID: "b", TenantID: "a", Name: "b"}, {ID: "c", TenantID: "a", Name: "c"}} {
				if err := store.Put(t.Context(), doc); err != nil {
					t.Fatal(err)
				}
			}
			grant(t, rel, "a", "b", "c")
			az := testAuthorizer(t, rel, model.EvaluationLimits{})
			var lister domain.Lister
			switch strategy {
			case "complete":
				lister = testListing(t, store, az, CompleteSet, 0, nil)
			case "candidates":
				lister = testListing(t, store, az, Candidates, 0, nil)
			case "exists":
				lister = testSQLListing(t, store, schema, az, nil)
			}
			server := listingServer(t, lister)
			query := domain.Query{TenantID: "a", Limit: 1}
			first := getPage(t, server, alice.ID, query, 200)
			if !slices.Equal(documentIDs(first.Items), []string{"a"}) {
				t.Fatalf("first page: %+v", first)
			}
			if err := rel.DeleteRelationship(t.Context(), "document", "b", "viewer", "user", alice.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.Put(t.Context(), domain.Document{ID: "c", TenantID: "b", Name: "c"}); err != nil {
				t.Fatal(err)
			}
			query.Cursor = first.NextCursor
			if got := walkHTTP(t, server, alice.ID, query); len(got) != 0 {
				t.Fatalf("cursor retained revoked/moved rows: %v", got)
			}
		})
	}
}

func TestPostgresLargeListingAndQueryPlan(t *testing.T) {
	store, rel, schema := postgresFixture(t)
	docs, ids := largeFixture(1005)
	for _, doc := range docs {
		if err := store.Put(t.Context(), doc); err != nil {
			t.Fatal(err)
		}
	}
	grant(t, rel, ids...)
	az := testAuthorizer(t, rel, model.EvaluationLimits{})
	complete := testListing(t, store, az, CompleteSet, 0, nil)
	if page, err := complete.ListVisible(t.Context(), alice, domain.Query{TenantID: "a", Limit: 50}); !errors.Is(err, model.ErrEvaluationLimit) || len(page.Items) != 0 {
		t.Fatalf("complete overflow: %+v %v", page, err)
	}
	sqlListing := testSQLListing(t, store, schema, az, nil)
	candidates := testListing(t, store, az, Candidates, 0, nil)
	want := slices.Clone(ids)
	slices.Reverse(want)
	for _, lister := range []domain.Lister{candidates, sqlListing} {
		got := walkHTTP(t, listingServer(t, lister), alice.ID, domain.Query{TenantID: "a", Limit: 50})
		if !slices.Equal(got, want) {
			t.Fatalf("large listing order/count: %d IDs, want %d", len(got), len(want))
		}
	}
	// The bounded complete strategy and EXISTS agree once the host explicitly
	// sizes its complete-set budget for this dataset. No overflow fallback occurs.
	wide := testAuthorizer(t, rel, model.EvaluationLimits{MaxLookupResults: 1100})
	if got := walkHTTP(t, listingServer(t, testListing(t, store, wide, CompleteSet, 0, nil)), alice.ID, domain.Query{TenantID: "a", Limit: 50}); !slices.Equal(got, want) {
		t.Fatalf("bounded complete order/count: %d", len(got))
	}
	query := domain.Query{TenantID: "a", Limit: 50}
	sql, args := store.querySQL(query, position{}, 50, decisions.ResourceSet{Unrestricted: true}, &alice, schema)
	for _, tt := range []struct {
		name    string
		divisor int
	}{{"dense", 2}, {"sparse", 100}} {
		if _, err := store.db.Exec(t.Context(), `DELETE FROM `+schema.Table("iam_tuples")+` WHERE resource_id::int % $1 <> 0`, tt.divisor); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{schema.Table("iam_tuples"), store.schema.Table("documents")} {
			if _, err := store.db.Exec(t.Context(), "ANALYZE "+table); err != nil {
				t.Fatal(err)
			}
		}
		var raw []byte
		if err := store.db.QueryRow(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plan []struct {
			Plan          map[string]any
			ExecutionTime float64 `json:"Execution Time"`
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s actual PostgreSQL plan: %s", tt.name, strings.TrimSpace(string(raw)))
		if len(plan) != 1 || plan[0].Plan["Actual Rows"].(float64) > 51 {
			t.Fatalf("unexpected bounded business result: %s", raw)
		}
		// Query plans measure physical work; semantic limits do not claim to cap it.
		got, err := sqlListing.ListVisible(t.Context(), alice, query)
		if err != nil {
			t.Fatal(err)
		}
		portable, err := candidates.ListVisible(t.Context(), alice, query)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(documentIDs(got.Items), documentIDs(portable.Items)) {
			t.Fatalf("%s predicate/check mismatch: %v / %v", tt.name, documentIDs(got.Items), documentIDs(portable.Items))
		}
		t.Logf("%s visible first page: %d; SQL execution %.3fms (one local run, not a production benchmark)", tt.name, len(got.Items), plan[0].ExecutionTime)
	}
}
