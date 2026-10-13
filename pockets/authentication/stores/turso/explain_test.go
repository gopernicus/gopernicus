package turso

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// errCaptured stops tursodb.List after it has rendered its page statement.
var errCaptured = errors.New("statement captured")

// captureQuerier records the page statement tursodb.List would run instead of
// running it. Only the first-page and offset flows are captured, so QueryRow
// (the count wrap) is never reached.
type captureQuerier struct {
	sql  string
	args []any
}

func (c *captureQuerier) Exec(context.Context, string, ...any) (sql.Result, error) {
	return nil, errCaptured
}

func (c *captureQuerier) Query(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	c.sql, c.args = query, args
	return nil, errCaptured
}

func (c *captureQuerier) QueryRow(context.Context, string, ...any) *sql.Row { return nil }

// TestListActiveByResourceUsesPartialIndex seeds a local file with one resource
// carrying a long terminal history, analyzes it, and requires EXPLAIN QUERY
// PLAN for the rendered active-listing statement to name the partial index
// idx_invitations_pending_tuple, whose predicate the literal status list matches.
func TestListActiveByResourceUsesPartialIndex(t *testing.T) {
	ctx := context.Background()
	db := openOAuthSQLite(t, filepath.Join(t.TempDir(), "explain.db"))
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	seed := `WITH RECURSIVE g(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM g WHERE n < 20000)
INSERT INTO invitations (id, resource_type, resource_id, relation, identifier, invited_by, token_hash, status, expires_at, created_at, updated_at)
SELECT 'inv-' || n, 'project', 'res-' || (n % 200), CASE WHEN n % 2 = 0 THEN 'member' ELSE 'admin' END, 'u' || n || '@example.com', 'owner', 'hash-' || n,
       CASE n % 3 WHEN 0 THEN 'accepted' WHEN 1 THEN 'declined' ELSE 'cancelled' END,
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '+1 day'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-' || n || ' seconds'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM g`
	if _, err := db.Exec(ctx, seed); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	hot := `WITH RECURSIVE g(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM g WHERE n < 5000)
INSERT INTO invitations (id, resource_type, resource_id, relation, identifier, invited_by, token_hash, status, expires_at, created_at, updated_at, resolved_subject_type)
SELECT 'hot-' || n, 'project', 'res-hot', 'member', 'hot' || n || '@example.com', 'owner', 'hot-hash-' || n,
       CASE WHEN n <= 6 THEN (CASE n % 2 WHEN 0 THEN 'pending' ELSE 'accepting' END) ELSE (CASE n % 2 WHEN 0 THEN 'declined' ELSE 'cancelled' END) END,
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '+1 day'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-' || n || ' seconds'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
       CASE WHEN n <= 6 AND n % 2 = 1 THEN 'user' ELSE '' END
FROM g`
	if _, err := db.Exec(ctx, hot); err != nil {
		t.Fatalf("seed hot resource: %v", err)
	}
	if _, err := db.Exec(ctx, `ANALYZE`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

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
			if _, err := tursodb.List(ctx, capture, activeListQuery(tc.in), tc.req); !errors.Is(err, errCaptured) {
				t.Fatalf("render statement: %v", err)
			}
			rows, err := db.Query(ctx, "EXPLAIN QUERY PLAN "+capture.sql, capture.args...)
			if err != nil {
				t.Fatalf("explain: %v\n%s", err, capture.sql)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, notUsed int
				var detail string
				if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			if !strings.Contains(plan, "idx_invitations_pending_tuple") {
				t.Fatalf("active listing is not served by idx_invitations_pending_tuple:\n%s", plan)
			}
			t.Logf("plan:\n%s", plan)
		})
	}
	store := NewInvitationStore(db)
	page, err := store.ListActiveByResource(ctx, invitations.ActiveListInput{ResourceType: "project", ResourceID: "res-hot"}, list.Request{Limit: 50, WithCount: true})
	if err != nil || len(page.Items) != 6 || page.Total == nil || *page.Total != 6 {
		t.Fatalf("hot resource active page: items=%d total=%v err=%v", len(page.Items), page.Total, err)
	}
}
