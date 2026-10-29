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

// TestCodeAuthenticationNeverBlanketProbed pins 0030 out of Repositories'
// blanket column probe: the columns are required only when the host sets
// OAuth2Config.DelegatedAssurance, and OAuth2Store.ProbeSchema asks then.
func TestCodeAuthenticationNeverBlanketProbed(t *testing.T) {
	for _, pc := range probeColumns {
		if pc.migration == codeAuthenticationMigration || pc.table == oauthCodesTable {
			t.Fatalf("probeColumns lists %+v; 0030 must be probed only through OAuth2Store.ProbeSchema", pc)
		}
	}
	if oauthCodeAuthenticationColumns != oauthCodeColumns+", authenticated_at, authentication_methods, assurance_level" {
		t.Fatalf("oauthCodeAuthenticationColumns = %q, want oauthCodeColumns plus the three 0030 columns", oauthCodeAuthenticationColumns)
	}
}

// TestCodeAuthenticationMigrationShape pins 0030's lock discipline: the lock
// timeout first, then three re-runnable, nullable, default-free ADD COLUMNs.
func TestCodeAuthenticationMigrationShape(t *testing.T) {
	sql := readMigration(t, codeAuthenticationMigration)
	if !strings.HasPrefix(sql, "SET LOCAL lock_timeout = '5s';\n") {
		t.Errorf("%s must begin with SET LOCAL lock_timeout", codeAuthenticationMigration)
	}
	const adds = "\nALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS authenticated_at TIMESTAMPTZ;\n" +
		"ALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS authentication_methods TEXT;\n" +
		"ALTER TABLE oauth_authorization_codes ADD COLUMN IF NOT EXISTS assurance_level TEXT;\n"
	if !strings.HasSuffix(sql, adds) {
		t.Errorf("%s must end with the three nullable ADD COLUMN IF NOT EXISTS", codeAuthenticationMigration)
	}
}

// TestCodeAuthenticationSchema_Postgres pins the conditional rule on a schema
// without 0030: Repositories constructs, the plain CreateCode/GetCode/RedeemCode
// path works without naming the columns, and ProbeSchema refuses with
// sdk.ErrNotFound naming the file. A closed pool is never schema-missing.
func TestCodeAuthenticationSchema_Postgres(t *testing.T) {
	db, schema, _ := activitySchema(t, "oauth_code_authentication_unmigrated")
	ctx := context.Background()
	for _, column := range codeAuthenticationColumnNames {
		if _, err := db.Exec(ctx, "ALTER TABLE "+schema.Table(oauthCodesTable)+" DROP COLUMN "+column); err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0030 = %v, want it to construct (delegated assurance is opt-in)", err)
	}
	store := repos.OAuth2.(*OAuth2Store)
	err = store.ProbeSchema(ctx)
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), codeAuthenticationMigration) {
		t.Fatalf("ProbeSchema without 0030 = %v, want sdk.ErrNotFound naming %s", err, codeAuthenticationMigration)
	}
	t.Logf("unmigrated probe refused: %v", err)

	now := time.Now().UTC().Truncate(time.Microsecond)
	seedIdleUser(t, db, schema, "u-code-plain")
	browser := seedIdleSession(t, repos, "u-code-plain", "tok-code-plain", now)
	code := oauth2.Code{Hash: "code-plain", UserID: "u-code-plain",
		Delegation:  session.Delegation{Issuer: "https://issuer.example", ClientID: "https://client.example/metadata.json", Resource: "https://mcp.example", ExchangeResource: "https://api.example", ExchangeClientID: "mcp"},
		RedirectURI: "https://client.example/callback", CodeChallenge: "challenge", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.CreateCode(ctx, code, browser.ID); err != nil {
		t.Fatalf("plain CreateCode without 0030 = %v, want success", err)
	}
	got, err := store.GetCode(ctx, code.Hash)
	if err != nil || !sameOAuthCode(got, code) {
		t.Fatalf("plain GetCode without 0030 = %+v, %v", got, err)
	}
	proposed, _ := session.NewSession("u-code-plain", time.Hour, now)
	proposed.Profile, proposed.Delegation, proposed.RefreshTokenHash = session.ProfileDelegated, code.Delegation, "refresh-code-plain"
	if _, err := store.RedeemCode(ctx, got, proposed, now); err != nil {
		t.Fatalf("plain RedeemCode without 0030 = %v, want success", err)
	}

	closed := probeDial(t)
	closedStore := NewOAuth2Store(closed, WithSchema(schema))
	_ = closed.Close()
	if err := closedStore.ProbeSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeSchema on a closed pool = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}

	// Apply 0030 onto the stripped table, then once more over its own columns:
	// IF NOT EXISTS makes the re-run a no-op.
	for _, pass := range []string{"apply", "re-run"} {
		if _, err := db.Exec(ctx, "DELETE FROM "+schema.Table("schema_migrations")+" WHERE version = $1", codeAuthenticationMigration); err != nil {
			t.Fatal(err)
		}
		if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
			t.Fatalf("0030 %s = %v, want success", pass, err)
		}
	}
	if err := store.ProbeSchema(ctx); err != nil {
		t.Fatalf("ProbeSchema after 0030 = %v, want nil", err)
	}
}
