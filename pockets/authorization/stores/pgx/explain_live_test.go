// Real query-plan measurement over more than one million canonical facts.
// Selective forward, reverse and exact-role lookups must use an index; the
// recursive descendant closure is measured without imposing an optimizer choice.
// POSTGRES_TEST_DSN names a disposable fixture; POSTGRES_TEST_SCHEMA is optional.
package pgx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/jackc/pgx/v5"
)

// explainSeedRows is the per-(type, relation) row multiplier: 4 resource types ×
// 4 relations × explainSeedRows ≈ 1e6 iam_tuples rows.
const explainSeedRows = 63000

// explainRoleRows is the count per role label: 2 labels × explainRoleRows
// = 100k assignments.
const explainRoleRows = 50000

// explainAfter is the keyset cursor every measured shape pages from — a
// non-empty after, so the plan is the CONTINUATION plan (the one the OR-form
// predicate must still fold into a range condition), never the first page.
const explainAfter = "r000040000"

// explainPage is the page size the engine would ask for (limit+1 lookahead).
const explainPage = 51

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

// TestLookupPlansAtScale records indexed plans and real method timings.
func TestLookupPlansAtScale(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()
	db := openAndMigrate(t, dsn)

	cfg := config{}
	for _, o := range storeOptions(t) {
		o(&cfg)
	}
	rels := newRelationshipStore(db, cfg)
	facts := newTupleStore(db, cfg)

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
		// access path, so this shape asserts only that iam_tuples is
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
		assertNoSeqScan(t, plan, "LookupResourceIDsByRelationTarget")
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

	t.Run("CanonicalRoleLookup", func(t *testing.T) {
		subject := tuples.SubjectRef{Type: "user", ID: "u00007"}
		query := tuples.Query{ResourceType: "doc", Subject: &subject, Limit: explainPage}
		args := &tupleArgs{}
		where, err := tupleWhere(args, query)
		if err != nil {
			t.Fatal(err)
		}
		sql := "SELECT " + tupleColumns + " FROM " + facts.table() + where + " ORDER BY " + tupleColumns + " LIMIT 50"
		rows, err := facts.Lookup(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		plan := explainJSON(t, db, sql, args.named())
		t.Logf("canonical role lookup: %d facts; scans=%v", len(rows), planScans(t, plan))
		assertNoSeqScan(t, plan, "canonical role lookup")
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

// seedLookupFixture creates direct grants, userset-navigation edges and role
// labels in the same authority, then gathers planner statistics. The self
// hierarchy has a fanout of ten; unrelated targets spread across 400 containers.
func seedLookupFixture(t *testing.T, db *pgxdb.DB) {
	t.Helper()
	ctx := context.Background()
	start := time.Now()

	relSQL := qualifySQL(t, `INSERT INTO iam_tuples (scope_kind,resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
SELECT 2,rt.t,
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
		t.Fatalf("seed iam_tuples: %v", err)
	}

	roleSQL := qualifySQL(t, `INSERT INTO iam_tuples (scope_kind,resource_type,resource_id,relation,subject_type,subject_id,subject_relation)
SELECT 2,'doc','r'||lpad(g::text,9,'0'),r.name,'user','u'||lpad((g % 100)::text,5,'0'),''
FROM generate_series(1, @rows) g, (VALUES ('editor'), ('reviewer')) AS r(name)`)
	if _, err := db.Exec(ctx, roleSQL, pgx.NamedArgs{"rows": explainRoleRows}); err != nil {
		t.Fatalf("seed role-labelled facts: %v", err)
	}

	if _, err := db.Exec(ctx, qualifySQL(t, `ANALYZE iam_tuples`)); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	var rels, roles int
	if err := db.QueryRow(ctx, qualifySQL(t, `SELECT (SELECT count(*) FROM iam_tuples), (SELECT count(*) FROM iam_tuples WHERE relation IN ('editor','reviewer'))`)).Scan(&rels, &roles); err != nil {
		t.Fatalf("count fixture: %v", err)
	}
	t.Logf("seeded %d canonical tuples (%d role-labelled facts) in %s", rels, roles, time.Since(start).Round(time.Millisecond))
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
// iam_tuples via idx_…" — for the log line and the assertions.
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

// assertNoSeqScan fails when the plan reads a pocket table sequentially — the
// regression the canonical lookup indexes exist to prevent.
func assertNoSeqScan(t *testing.T, plan, shape string) {
	t.Helper()
	for _, s := range planScans(t, plan) {
		if strings.HasPrefix(s, "Seq Scan") {
			t.Errorf("%s must not sequentially scan a pocket table (%s); plan:\n%s", shape, s, plan)
		}
	}
}
