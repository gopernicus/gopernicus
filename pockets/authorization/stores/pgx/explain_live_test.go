// The lookup-plan measurement (authorization-lookup-paging, A6): it seeds a
// MILLION iam_relationships rows plus 100k iam_roles rows, ANALYZEs, then for
// every keyset lookup shape runs the STORE METHOD once (timing) and EXPLAINs the
// exact statement that method emits (plan). It is the executable form of "the
// 0005 indexes earn their lock": the two shapes whose predicate the index fully
// serves must range-scan it, the expansion-join shape must reach
// iam_relationships through SOME index rather than a sequential scan, and the
// descendant closure — whose per-page cost is the closure size by design (plan
// A3) — is recorded, not asserted.
//
// It requires POSTGRES_TEST_DSN and skips loudly without it, exactly like the
// conformance suite, and honours POSTGRES_TEST_SCHEMA. The seed is ONE
// INSERT … SELECT generate_series per table; the fixture is truncated on cleanup
// so the next test starts empty.
package pgx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/jackc/pgx/v5"
)

// explainSeedRows is the per-(type, relation) row multiplier: 4 resource types ×
// 4 relations × explainSeedRows ≈ 1e6 iam_relationships rows.
const explainSeedRows = 63000

// explainRoleRows is the per-role iam_roles row count: 2 roles × explainRoleRows
// = 100k assignments.
const explainRoleRows = 50000

// explainAfter is the keyset cursor every measured shape pages from — a
// non-empty after, so the plan is the CONTINUATION plan (the one the OR-form
// predicate must still fold into a range condition), never the first page.
const explainAfter = "r000040000"

// explainPage is the page size the engine would ask for (limit+1 lookahead).
const explainPage = 51

// relationshipKeysetIndex / rolesKeysetIndex are the 0005 access paths the
// measured plans must select.
const (
	relationshipKeysetIndex = "idx_iam_relationships_type_relation_resource"
	rolesKeysetIndex        = "idx_iam_roles_subject_resource_lookup"
)

// explainNode is the subset of a JSON plan node the measurement reads: what the
// node did, to which relation, through which index, and its children.
type explainNode struct {
	NodeType  string        `json:"Node Type"`
	Relation  string        `json:"Relation Name"`
	IndexName string        `json:"Index Name"`
	Plans     []explainNode `json:"Plans"`
}

// explainPlan is one EXPLAIN (FORMAT JSON) element.
type explainPlan struct {
	Plan explainNode `json:"Plan"`
}

// TestLookupPlansAtScale is the A6 measurement. Each subtest logs its plan's
// scan nodes and the store method's wall time at ~1e6 rows. The two shapes whose
// predicate the 0005 relationship index serves reach iam_relationships through
// it (as an ordered range scan when the cursor is the selective term, as a
// bitmap component when the planner prefers to AND it with the subject index —
// either way the keyset predicate is answered from the index, never by filtering
// the whole type/relation set).
func TestLookupPlansAtScale(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	db := openAndMigrate(t, dsn)

	cfg := config{}
	for _, o := range storeOptions(t) {
		o(&cfg)
	}
	rels := newRelationshipStore(db, cfg)
	roles := newRoleStore(db, cfg)

	seedLookupFixture(t, db)

	// Every self-hierarchy row hangs off a fanout-10 parent chain (resource g's
	// parent is resource g/10), so root r000000004 owns the whole 4* subtree —
	// ~11k nodes, of which the 40000-49999 generation sits past explainAfter.
	//
	// The relation targets are half of the 400 fixture containers, each holding
	// ~157 resources of the type spread evenly across the id range: the
	// "Through hop with a large target set" the plan's risk list names, and the
	// shape the keyset index exists for. A page is a genuine continuation with
	// rows on both sides of the cursor, never an empty tail.
	root := explainResourceID(4)
	targets := make([]string, 0, 200)
	for i := 1; i <= 200; i++ {
		targets = append(targets, explainContainerID(i))
	}

	t.Run("LookupResourceIDs", func(t *testing.T) {
		sql, args := rels.lookupResourceIDsSQL("doc", []string{"owner", "viewer"}, "user", "u00042", explainAfter, explainPage)
		start := time.Now()
		ids, err := rels.LookupResourceIDs(ctx, "doc", []string{"owner", "viewer"}, "user", "u00042", explainAfter, explainPage)
		if err != nil {
			t.Fatalf("LookupResourceIDs: %v", err)
		}
		plan := explainJSON(t, db, sql, args)
		t.Logf("LookupResourceIDs: %d ids in %s; scans=%v", len(ids), time.Since(start).Round(time.Millisecond), planScans(t, plan))
		// The expansion join makes the subject side a legitimate alternative
		// access path, so this shape asserts only that iam_relationships is
		// reached through an index — never a sequential scan of a million rows.
		assertNoSeqScan(t, plan, "LookupResourceIDs")
	})

	t.Run("LookupResourceIDsByRelationTarget", func(t *testing.T) {
		sql, args := rels.lookupResourceIDsByRelationTargetSQL("doc", "space", "space", targets, explainAfter, explainPage)
		start := time.Now()
		ids, err := rels.LookupResourceIDsByRelationTarget(ctx, "doc", "space", "space", targets, explainAfter, explainPage)
		if err != nil {
			t.Fatalf("LookupResourceIDsByRelationTarget: %v", err)
		}
		plan := explainJSON(t, db, sql, args)
		t.Logf("LookupResourceIDsByRelationTarget (%d targets): %d ids in %s; scans=%v", len(targets), len(ids), time.Since(start).Round(time.Millisecond), planScans(t, plan))
		assertPlanUses(t, plan, relationshipKeysetIndex, "LookupResourceIDsByRelationTarget")
	})

	t.Run("LookupDescendantResourceIDs", func(t *testing.T) {
		sql, args := rels.lookupDescendantResourceIDsSQL("doc", []string{"parent"}, "doc", []string{root}, explainAfter, explainPage)
		start := time.Now()
		ids, err := rels.LookupDescendantResourceIDs(ctx, "doc", []string{"parent"}, "doc", []string{root}, explainAfter, explainPage)
		if err != nil {
			t.Fatalf("LookupDescendantResourceIDs: %v", err)
		}
		plan := explainJSON(t, db, sql, args)
		// Recorded, not asserted: the closure is recomputed per page by design
		// (plan A3), so its cost is the closure size and no index changes that.
		t.Logf("LookupDescendantResourceIDs (root %s): %d ids in %s; scans=%v", root, len(ids), time.Since(start).Round(time.Millisecond), planScans(t, plan))
	})

	t.Run("LookupResourceIDsBySubjectAndRoles", func(t *testing.T) {
		sql, args := roles.lookupResourceIDsBySubjectAndRolesSQL("user", "u00007", "doc", []string{"editor", "reviewer"}, explainAfter, explainPage)
		start := time.Now()
		ids, unrestricted, err := roles.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u00007", "doc", []string{"editor", "reviewer"}, explainAfter, explainPage)
		if err != nil {
			t.Fatalf("LookupResourceIDsBySubjectAndRoles: %v", err)
		}
		if unrestricted {
			t.Fatal("fixture subject must hold no global grant")
		}
		plan := explainJSON(t, db, sql, args)
		t.Logf("LookupResourceIDsBySubjectAndRoles: %d ids in %s; scans=%v", len(ids), time.Since(start).Round(time.Millisecond), planScans(t, plan))
		assertPlanUses(t, plan, rolesKeysetIndex, "LookupResourceIDsBySubjectAndRoles")

		probe, probeArgs := roles.globalRoleGrantSQL("user", "u00007", []string{"editor", "reviewer"})
		probePlan := explainJSON(t, db, probe, probeArgs)
		t.Logf("global role probe: scans=%v", planScans(t, probePlan))
		assertNoSeqScan(t, probePlan, "global role probe")
	})
}

// explainResourceID renders the fixture's zero-padded resource id for row n.
func explainResourceID(n int) string { return explainPaddedID('r', n) }

// explainContainerID renders the fixture's zero-padded CONTAINER id for slot n —
// the target of the non-self "space" relation.
func explainContainerID(n int) string { return explainPaddedID('s', n) }

// explainPaddedID renders prefix + a 9-digit zero-padded n, matching the seed's
// lpad(…, 9, '0').
func explainPaddedID(prefix byte, n int) string {
	id := []byte(string(prefix) + "000000000")
	for i, n := len(id)-1, n; n > 0 && i > 0; i, n = i-1, n/10 {
		id[i] = byte('0' + n%10)
	}
	return string(id)
}

// seedLookupFixture loads ~1e6 iam_relationships rows (4 resource types × 4
// relations × explainSeedRows) and 100k iam_roles rows in ONE INSERT each, then
// ANALYZEs so the planner sees real statistics.
//
// The four relations are the four measured shapes: "owner"/"viewer" are direct
// user grants (each offsetting its subject by a different stride, so no subject
// holds two relations on one resource — the unique-subject index forbids it),
// "parent" is the SELF hierarchy (resource g's parent is resource g/10: a
// fanout-10 chain, so root r000000004 has an ~11k-node transitive closure), and
// "space" is a non-self Through target pointing at one of 400 containers, each
// holding ~157 resources of the type spread evenly across the id range.
func seedLookupFixture(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	ctx := context.Background()
	start := time.Now()

	relSQL := qualifySQL(t, `INSERT INTO iam_relationships (resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
SELECT rt.t,
       'r' || lpad(g::text, 9, '0'),
       rel.r,
       CASE rel.r WHEN 'parent' THEN rt.t WHEN 'space' THEN 'space' ELSE 'user' END,
       CASE rel.r
            WHEN 'parent' THEN 'r' || lpad((g / 10)::text, 9, '0')
            WHEN 'space' THEN 's' || lpad(((g % 400) + 1)::text, 9, '0')
            ELSE 'u' || lpad(((g + rel.k * 167) % 500)::text, 5, '0') END,
       ''
FROM generate_series(1, @rows) g,
     (VALUES ('doc'), ('space'), ('dash'), ('tenant')) AS rt(t),
     (VALUES ('owner', 0), ('viewer', 1), ('parent', 2), ('space', 3)) AS rel(r, k)
WHERE NOT (rel.r = 'parent' AND g < 10)`)
	if _, err := db.Exec(ctx, relSQL, pgx.NamedArgs{"rows": explainSeedRows}); err != nil {
		t.Fatalf("seed iam_relationships: %v", err)
	}

	roleSQL := qualifySQL(t, `INSERT INTO iam_roles (subject_type, subject_id, role, resource_type, resource_id)
SELECT 'user', 'u' || lpad((g % 100)::text, 5, '0'), r.name, 'doc', 'r' || lpad(g::text, 9, '0')
FROM generate_series(1, @rows) g, (VALUES ('editor'), ('reviewer')) AS r(name)`)
	if _, err := db.Exec(ctx, roleSQL, pgx.NamedArgs{"rows": explainRoleRows}); err != nil {
		t.Fatalf("seed iam_roles: %v", err)
	}

	if _, err := db.Exec(ctx, qualifySQL(t, `ANALYZE iam_relationships, iam_roles`)); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	var rels, roles int
	if err := db.QueryRow(ctx, qualifySQL(t, `SELECT (SELECT count(*) FROM iam_relationships), (SELECT count(*) FROM iam_roles)`)).Scan(&rels, &roles); err != nil {
		t.Fatalf("count fixture: %v", err)
	}
	t.Logf("seeded %d iam_relationships + %d iam_roles rows in %s", rels, roles, time.Since(start).Round(time.Millisecond))
}

// explainJSON plans the exact statement a store method emits, with the exact
// arguments it binds, and returns the JSON plan text.
func explainJSON(t *testing.T, db *pgxdb.DB, sql string, args pgx.NamedArgs) string {
	t.Helper()
	var plan string
	if err := db.QueryRow(context.Background(), "EXPLAIN (FORMAT JSON) "+sql, args).Scan(&plan); err != nil {
		t.Fatalf("explain: %v\nSQL:\n%s", err, sql)
	}
	return plan
}

// planScans summarizes every scan node in plan order — "Index Scan
// iam_relationships via idx_…" — for the log line and the assertions.
func planScans(t *testing.T, plan string) []string {
	t.Helper()
	var roots []explainPlan
	if err := json.Unmarshal([]byte(plan), &roots); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, plan)
	}
	var out []string
	var walk func(explainNode)
	walk = func(n explainNode) {
		if strings.Contains(n.NodeType, "Scan") {
			s := n.NodeType
			if n.Relation != "" {
				s += " " + n.Relation
			}
			if n.IndexName != "" {
				s += " via " + n.IndexName
			}
			out = append(out, s)
		}
		for _, c := range n.Plans {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r.Plan)
	}
	return out
}

// assertPlanUses fails when no scan node selected index.
func assertPlanUses(t *testing.T, plan, index, shape string) {
	t.Helper()
	scans := planScans(t, plan)
	for _, s := range scans {
		if strings.HasSuffix(s, "via "+index) {
			return
		}
	}
	t.Errorf("%s must reach its table through %s; plan scanned %v:\n%s", shape, index, scans, plan)
}

// assertNoSeqScan fails when the plan reads a pocket table sequentially — the
// regression the 0005 indexes exist to prevent.
func assertNoSeqScan(t *testing.T, plan, shape string) {
	t.Helper()
	for _, s := range planScans(t, plan) {
		if strings.HasPrefix(s, "Seq Scan") {
			t.Errorf("%s must not sequentially scan a pocket table (%s); plan:\n%s", shape, s, plan)
		}
	}
}
