//go:build integration

// Integration tests hit a live Turso / libSQL database. Run with:
//
//	go test -tags=integration ./...
//
// They require TURSO_DATABASE_URL and TURSO_AUTH_TOKEN in the environment (or a
// .env loaded by the caller). Absent those, the tests skip loudly — a silent
// green here would claim dialect conformance nothing verified.
package turso

import (
	"context"
	"log/slog"
	"os"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// authTables are the pocket's tables in child-before-parent order, so a
// truncation pass respects any conventional user_id references: api_keys before
// service_accounts, and the oauth/audit/invitation tables before users.
var authTables = []string{
	"oauth_clients",
	"oauth_authorization_codes",
	"oauth_refresh_history",
	"external_subjects",
	"passkey_credentials",
	"user_passwords",
	"sessions",
	"api_keys",
	"service_accounts",
	"oauth_accounts",
	"oauth_states",
	"security_events",
	"invitations",
	"user_identifiers",
	"challenges",
	"contact_changes",
	"authentication_grants",
	"users",
}

// TestConformance_Turso runs the shared auth storetest suite against a live
// Turso/libSQL database. Each newRepos call opens a connection, applies the
// canonical migrations, and truncates the pocket's tables so every leaf subtest
// starts from a clean, isolated Repositories (the SQL harness half of the
// newRepos contract).
func TestConformance_Turso(t *testing.T) {
	url := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso conformance NOT verified")
	}

	newRepos := func(t *testing.T) auth.Repositories {
		db, err := tursodb.Open(context.Background(), tursodb.Config{URL: sdk.Secret(url), AuthToken: sdk.Secret(token)})
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { db.Close() })

		if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		truncate(t, db)
		t.Cleanup(func() { truncate(t, db) })

		repos, err := Repositories(context.Background(), db)
		if err != nil {
			t.Fatalf("Repositories: %v", err)
		}
		return repos
	}
	storetest.Run(t, newRepos)
	storetest.RunPasskeys(t, newRepos)
}

// truncate clears every auth table so a Repositories starts empty. The
// security_events immutability triggers refuse DELETE from every connection, so
// it drops them first and re-executes the embedded 0022 after (test-only).
// security_events_retention is not in authTables: its row is configuration —
// the floor — and Repositories refuses a database without it.
func truncate(t *testing.T, db *tursodb.DB) {
	t.Helper()
	ctx := context.Background()
	for _, trigger := range []string{securityEventsImmutableUpdate, securityEventsImmutableDelete} {
		if _, err := db.Exec(ctx, "DROP TRIGGER IF EXISTS "+trigger); err != nil {
			t.Fatalf("drop trigger %s: %v", trigger, err)
		}
	}
	for _, tbl := range authTables {
		if _, err := db.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	if err := execMigration(ctx, db, immutableMigration); err != nil {
		t.Fatalf("re-create the security_events triggers: %v", err)
	}
}

// TestSecurityEventsIntegrity_Turso runs the integrity cases against the live
// database, from an empty schema.
func TestSecurityEventsIntegrity_Turso(t *testing.T) {
	url := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")
	if url == "" || token == "" {
		t.Skip("TURSO_DATABASE_URL/TURSO_AUTH_TOKEN not set — turso audit-rail integrity NOT verified")
	}
	runSecurityEventsIntegrity(t, integrityTarget{
		open: func(t *testing.T, mode environment.Mode, logger *slog.Logger) *tursodb.DB {
			db, err := tursodb.Open(context.Background(), tursodb.Config{URL: sdk.Secret(url), AuthToken: sdk.Secret(token), RuntimeMode: mode, Logger: logger, TLSDisabledAcknowledged: mode != ""})
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			return db
		},
		reset: probeDropAll,
	})
}

// TestFloorRowSurvivesTruncate_Turso proves the harness keeps the floor: after
// truncate() row 1 of security_events_retention still reads 365 and the store
// still constructs.
func TestFloorRowSurvivesTruncate_Turso(t *testing.T) {
	db := probeDial(t)
	if err := tursodb.RunMigrations(context.Background(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	truncate(t, db)
	var days int
	if err := db.QueryRow(context.Background(), `SELECT floor_days FROM `+retentionFloorTable+` WHERE id = 1`).Scan(&days); err != nil || days != 365 {
		t.Fatalf("floor row after truncate = %d, %v; want 365", days, err)
	}
	if _, err := Repositories(context.Background(), db); err != nil {
		t.Fatalf("Repositories after truncate: %v", err)
	}
}
