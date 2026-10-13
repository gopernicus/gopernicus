// Real query-plan check for ListActiveByResource: over a resource whose history
// is mostly terminal rows, the active listing must be served by the partial
// index idx_invitations_pending_tuple, whose predicate its literal status list
// matches. POSTGRES_TEST_DSN names a disposable fixture; POSTGRES_TEST_SCHEMA is
// optional.
package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	jackpgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// errCaptured stops pgxdb.List after it has rendered its first statement.
var errCaptured = errors.New("statement captured")

// explainNode is the subset of a JSON plan node the check reads.
type explainNode struct {
	NodeType  string        `json:"Node Type"`
	Relation  string        `json:"Relation Name"`
	IndexName string        `json:"Index Name"`
	Plans     []explainNode `json:"Plans"`
}

type explainPlan struct {
	Plan explainNode `json:"Plan"`
}

// captureQuerier records the statement pgxdb.List would run instead of running it.
type captureQuerier struct {
	sql  string
	args []any
}

func (c *captureQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (c *captureQuerier) Query(_ context.Context, sql string, args ...any) (jackpgx.Rows, error) {
	c.sql, c.args = sql, args
	return nil, errCaptured
}

func (c *captureQuerier) QueryRow(_ context.Context, sql string, args ...any) jackpgx.Row {
	c.sql, c.args = sql, args
	return capturedRow{}
}

type capturedRow struct{}

func (capturedRow) Scan(...any) error { return errCaptured }

// TestListActiveByResourceUsesPartialIndex seeds one resource with a long
// terminal history and a handful of live rows, analyzes, and requires the
// rendered first-page statement to scan idx_invitations_pending_tuple.
func TestListActiveByResourceUsesPartialIndex(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — active-listing plan NOT verified")
	}
	ctx := context.Background()
	schema := testSchema(t)
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, migrateOpts(schema)...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })

	table := schema.Table(invitationsTable)
	// 400 resources × 100 rows of history, one in ten of each resource's rows still pending, so the
	// partial index holds ~4000 entries and its (resource_type, resource_id)
	// prefix is what must be selective. The hot resource carries 20000 more,
	// with 6 active rows (pending and accepting) among them.
	seed := `INSERT INTO ` + table + ` (id, resource_type, resource_id, relation, identifier, invited_by, token_hash, status, expires_at, accepted_at, created_at, updated_at, resolved_subject_type)
SELECT 'inv-' || g, 'project', 'res-' || (g % 400), CASE WHEN g % 2 = 0 THEN 'member' ELSE 'admin' END, 'u' || g || '@example.com', 'owner', 'hash-' || g,
       CASE WHEN (g / 400) % 10 = 0 THEN 'pending' ELSE (ARRAY['accepted', 'declined', 'cancelled'])[1 + g % 3] END, now() + interval '1 day',
       CASE WHEN (g / 400) % 10 <> 0 AND g % 3 = 0 THEN now() END, now() - g * interval '1 second', now(),
       CASE WHEN (g / 400) % 10 <> 0 AND g % 3 = 0 THEN 'user' ELSE '' END
FROM generate_series(1, 40000) AS g`
	if _, err := db.Exec(ctx, seed); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	hot := `INSERT INTO ` + table + ` (id, resource_type, resource_id, relation, identifier, invited_by, token_hash, status, expires_at, created_at, updated_at, resolved_subject_type)
SELECT 'hot-' || g, 'project', 'res-hot', 'member', 'hot' || g || '@example.com', 'owner', 'hot-hash-' || g,
       CASE WHEN g <= 6 THEN (ARRAY['pending', 'accepting'])[1 + g % 2] ELSE (ARRAY['declined', 'cancelled'])[1 + g % 2] END,
       now() + interval '1 day', now() - g * interval '1 second', now(), CASE WHEN g <= 6 AND g % 2 = 1 THEN 'user' ELSE '' END
FROM generate_series(1, 20000) AS g`
	if _, err := db.Exec(ctx, hot); err != nil {
		t.Fatalf("seed hot resource: %v", err)
	}
	if _, err := db.Exec(ctx, `ANALYZE `+table); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	var indexed int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE status IN ('pending', 'accepting')`).Scan(&indexed); err != nil || indexed < 4000 {
		t.Fatalf("fixture: partial index holds %d rows (%v), want thousands", indexed, err)
	}

	store := NewInvitationStore(db, storeOpts(schema)...)
	for _, tc := range []struct {
		name string
		in   invitations.ActiveListInput
		req  list.Request
	}{
		{"FirstPage", invitations.ActiveListInput{ResourceType: "project", ResourceID: "res-hot"}, list.Request{Limit: 50}},
		{"RelationFilter", invitations.ActiveListInput{ResourceType: "project", ResourceID: "res-hot", Relation: "member"}, list.Request{Limit: 50}},
		{"Offset", invitations.ActiveListInput{ResourceType: "project", ResourceID: "res-hot"}, list.Request{Limit: 50, Strategy: list.StrategyOffset}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := &captureQuerier{}
			if _, err := pgxdb.List(ctx, capture, store.activeListQuery(tc.in), tc.req); !errors.Is(err, errCaptured) {
				t.Fatalf("render statement: %v", err)
			}
			var raw []byte
			if err := db.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+capture.sql, capture.args...).Scan(&raw); err != nil {
				t.Fatalf("explain: %v\n%s", err, capture.sql)
			}
			var plans []explainPlan
			if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
				t.Fatalf("decode plan: %v", err)
			}
			if !usesIndex(plans[0].Plan, "idx_invitations_pending_tuple") {
				t.Fatalf("active listing is not served by idx_invitations_pending_tuple:\n%s", raw)
			}
			rows, err := db.Query(ctx, "EXPLAIN "+capture.sql, capture.args...)
			if err != nil {
				t.Fatalf("explain text: %v", err)
			}
			lines, err := jackpgx.CollectRows(rows, jackpgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("plan:\n%s", strings.Join(lines, "\n"))
		})
	}
	page, err := store.ListActiveByResource(ctx, invitations.ActiveListInput{ResourceType: "project", ResourceID: "res-hot"}, list.Request{Limit: 50, WithCount: true})
	if err != nil || len(page.Items) != 6 || page.Total == nil || *page.Total != 6 {
		t.Fatalf("hot resource active page: items=%d total=%v err=%v", len(page.Items), page.Total, err)
	}
}

// usesIndex reports whether any node of the plan scans through index.
func usesIndex(n explainNode, index string) bool {
	if n.IndexName == index {
		return true
	}
	for _, child := range n.Plans {
		if usesIndex(child, index) {
			return true
		}
	}
	return false
}
