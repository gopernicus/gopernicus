package pgx

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

// This measured plan regression pins the amplification removed from the
// bounded CTE: even a shallow graph previously produced every reachable state
// before DISTINCT/LIMIT. It does not assert a physical table-scan ceiling.
func TestBoundedExpansionStopsProducingStatesAtCap(t *testing.T) {
	db := openAndMigrate(t, requireDSN(t))
	schema := testSchema(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `INSERT INTO `+schema.Table("iam_relationships")+`
	(resource_type,resource_id,relation,subject_type,subject_id,subject_relation)
	SELECT 'group', 'g'||i::text, 'member', 'user', 'u1', ''
	FROM generate_series(1, 5000) i`)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	err = db.QueryRow(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+boundedReachableCTE(schema)+" SELECT count(*) FROM capped", pgx.NamedArgs{
		"subject_type": "user", "subject_id": "u1", "state_cap": 11,
	}).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	type planNode struct {
		Kind  string            `json:"Node Type"`
		Rows  float64           `json:"Actual Rows"`
		Plans []json.RawMessage `json:"Plans"`
	}
	var roots []struct{ Plan json.RawMessage }
	if err := json.Unmarshal(raw, &roots); err != nil {
		t.Fatal(err)
	}
	found := false
	var visit func(json.RawMessage)
	visit = func(data json.RawMessage) {
		var node planNode
		if err := json.Unmarshal(data, &node); err != nil {
			t.Fatal(err)
		}
		if node.Kind == "Recursive Union" {
			found = true
			if node.Rows != 11 {
				t.Fatalf("produced %g recursive states, want 11; plan=%s", node.Rows, raw)
			}
		}
		for _, child := range node.Plans {
			visit(child)
		}
	}
	for _, root := range roots {
		visit(root.Plan)
	}
	if !found {
		t.Fatalf("recursive plan missing: %s", raw)
	}
}
