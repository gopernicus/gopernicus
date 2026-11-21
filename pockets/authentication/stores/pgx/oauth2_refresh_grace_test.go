package pgx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestRefreshGraceNeverBlanketProbed pins 0031 out of Repositories' blanket
// column probe: the columns are required only when the host sets
// oauth2.Config.RefreshReuseGrace, and OAuth2Store.ProbeRefreshGraceSchema
// asks then.
func TestRefreshGraceNeverBlanketProbed(t *testing.T) {
	for _, pc := range probeColumns {
		if pc.migration == refreshSuccessorMigration || pc.table == oauthHistoryTable {
			t.Fatalf("probeColumns lists %+v; 0031 must be probed only through OAuth2Store.ProbeRefreshGraceSchema", pc)
		}
	}
}

// TestRefreshGraceMigrationShape pins 0031's lock discipline: the lock
// timeout first, then two re-runnable, nullable, default-free ADD COLUMNs.
func TestRefreshGraceMigrationShape(t *testing.T) {
	sql := readMigration(t, refreshSuccessorMigration)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", refreshSuccessorMigration)
	}
	const adds = "\nALTER TABLE oauth_refresh_history ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ;\n" +
		"ALTER TABLE oauth_refresh_history ADD COLUMN IF NOT EXISTS successor_ciphertext TEXT;\n"
	if !strings.HasSuffix(sql, adds) {
		t.Errorf("%s must end with the two nullable ADD COLUMN IF NOT EXISTS", refreshSuccessorMigration)
	}
}

// TestRefreshGraceSchema_Postgres pins the conditional rule on a schema
// without 0031: Repositories constructs, the plain rotation and Prune work
// without naming the columns, and ProbeRefreshGraceSchema refuses with
// sdk.ErrNotFound naming the file. A closed pool is never schema-missing, and
// the file applies and re-runs over the stripped table.
func TestRefreshGraceSchema_Postgres(t *testing.T) {
	db, schema, _ := activitySchema(t, "oauth_refresh_grace_unmigrated")
	ctx := context.Background()
	for _, column := range refreshSuccessorColumnNames {
		if _, err := db.Exec(ctx, "ALTER TABLE "+schema.Table(oauthHistoryTable)+" DROP COLUMN "+column); err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0031 = %v, want it to construct (the grace is opt-in)", err)
	}
	store := repos.OAuth2.(*OAuth2Store)
	err = store.ProbeRefreshGraceSchema(ctx)
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), refreshSuccessorMigration) {
		t.Fatalf("ProbeRefreshGraceSchema without 0031 = %v, want sdk.ErrNotFound naming %s", err, refreshSuccessorMigration)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	seedIdleUser(t, db, schema, "u-grace-plain")
	browser := seedIdleSession(t, repos, "u-grace-plain", "tok-grace-plain", now)
	code := oauth2.Code{Hash: "code-grace-plain", UserID: "u-grace-plain",
		Delegation:  session.Delegation{Issuer: "https://issuer.example", ClientID: "https://client.example/metadata.json", Resource: "https://mcp.example", ExchangeResource: "https://api.example", ExchangeClientID: "mcp"},
		RedirectURI: "https://client.example/callback", CodeChallenge: "challenge", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.CreateCode(ctx, code, browser.ID); err != nil {
		t.Fatal(err)
	}
	proposed, _ := session.NewSession("u-grace-plain", time.Hour, now)
	proposed.Profile, proposed.Delegation, proposed.RefreshTokenHash = session.ProfileDelegated, code.Delegation, "refresh-grace-plain"
	if _, err := store.RedeemCode(ctx, code, proposed, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateRefresh(ctx, oauth2.Refresh{Hash: "refresh-grace-plain", NewHash: "refresh-grace-plain-next", ClientID: code.Delegation.ClientID, Resource: code.Delegation.Resource, Now: now}); err != nil {
		t.Fatalf("plain RotateRefresh without 0031 = %v, want success", err)
	}
	if err := store.Prune(ctx, now); err != nil {
		t.Fatalf("Prune without 0031 = %v, want success", err)
	}

	closed := probeDial(t)
	closedStore := NewOAuth2Store(closed, WithSchema(schema))
	_ = closed.Close()
	if err := closedStore.ProbeRefreshGraceSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeRefreshGraceSchema on a closed pool = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}

	for _, pass := range []string{"apply", "re-run"} {
		if _, err := db.Exec(ctx, "DELETE FROM "+schema.Table("schema_migrations")+" WHERE version = $1", refreshSuccessorMigration); err != nil {
			t.Fatal(err)
		}
		if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
			t.Fatalf("0031 %s = %v, want success", pass, err)
		}
	}
	if err := store.ProbeRefreshGraceSchema(ctx); err != nil {
		t.Fatalf("ProbeRefreshGraceSchema after 0031 = %v, want nil", err)
	}
}
