package pgx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// TestLive_RecorderBoundsClientAttribution records through the ONE Recorder
// into this store the three attributed values PostgreSQL refuses or the
// Recorder bounds: a client id carrying the NUL a %00 Basic username decodes
// to (JSONB refuses \u0000), a 4 KB client id, and a User-Agent that is not
// UTF-8 (TEXT refuses it). Each row must exist with that field blank and no
// write may fail. Absent a DSN it skips loudly, like the conformance suite.
func TestLive_RecorderBoundsClientAttribution(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN not set — recorder client attribution on postgres NOT verified")
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
	store := NewSecurityEventStore(db, storeOpts(schema)...)

	const ip, agent, client = "203.0.113.9", "agent/1", "https://client.example/metadata"
	clientID := func(e securityevent.SecurityEvent) string { return fmt.Sprint(e.Details["client_id"]) }
	userAgent := func(e securityevent.SecurityEvent) string { return e.UserAgent }
	for _, tc := range []struct {
		name, userID, clientID, userAgent string
		blanked                           func(securityevent.SecurityEvent) string
		kept                              func(securityevent.SecurityEvent) string
		keptWant                          string
	}{
		{"NUL client id", "bound-nul", client + "\x00", agent, clientID, userAgent, agent},
		{"4 KB client id", "bound-oversize", client + "/" + strings.Repeat("a", 4096), agent, clientID, userAgent, agent},
		{"invalid UTF-8 user agent", "bound-ua", client, "agent/\xff", userAgent, clientID, client},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := securityevent.NewRecorder(store, sdk.IDGenerator{}, nil, slog.New(slog.DiscardHandler),
				securityevent.WithClientInfoReader(func(context.Context) (string, string) { return ip, tc.userAgent }))
			rec.Record(ctx, securityevent.Input{UserID: tc.userID, Type: securityevent.TypeOAuth2GrantFailed, Status: securityevent.StatusFailure,
				Details: map[string]any{"client_id": tc.clientID, "reason": securityevent.ReasonInvalidClient}})
			if n := rec.WriteFailures(); n != 0 {
				t.Errorf("WriteFailures = %d, want 0: the row was lost", n)
			}
			page, err := store.List(ctx, securityevent.ListFilter{UserID: tc.userID}, list.Request{Limit: 10})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("stored %d rows for %s, want 1", len(page.Items), tc.userID)
			}
			evt := page.Items[0]
			if got := tc.blanked(evt); got != "" {
				t.Errorf("bounded field = %.40q (%d bytes), want blank", got, len(got))
			}
			if got := tc.kept(evt); got != tc.keptWant {
				t.Errorf("unbounded field = %q, want %q", got, tc.keptWant)
			}
			if evt.IPAddress != ip || evt.Details["reason"] != securityevent.ReasonInvalidClient {
				t.Errorf("row ip/reason = %q/%v, want %q/%s", evt.IPAddress, evt.Details["reason"], ip, securityevent.ReasonInvalidClient)
			}
		})
	}
}
