package pgx

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

func oauthPostgresFixture(t *testing.T) (*pgxdb.DB, auth.Repositories, session.Session, session.Session) {
	t.Helper()
	db := probeDial(t)
	schema := testSchema(t)
	probeResetSchema(t, db)
	t.Cleanup(func() { truncate(t, db, schema) })
	repos, err := Repositories(t.Context(), db, storeOpts(schema)...)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	u := user.User{ID: "oauth-owner", Status: user.StatusActive, CreatedAt: now, UpdatedAt: now}
	_, _, err = repos.Users.CreateWithPrimaryIdentifier(t.Context(), u, identifier.Identifier{ID: "oauth-email", Kind: identifier.KindEmail, NormalizedValue: "oauth@example.test", LoginEnabled: true, IsPrimary: true, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	web, _ := session.NewSession(u.ID, time.Hour, now)
	web.RefreshTokenHash = "web-refresh"
	web, err = repos.Sessions.Create(t.Context(), web)
	if err != nil {
		t.Fatal(err)
	}
	delegated, _ := session.NewSession(u.ID, time.Hour, now)
	delegated.Profile = session.ProfileDelegated
	delegated.Delegation = session.Delegation{Issuer: "https://issuer.example", ClientID: "https://client.example/metadata", Resource: "https://mcp.example", ExchangeClientID: "mcp-service", ExchangeResource: "https://api.example"}
	delegated.RefreshTokenHash = "oauth-refresh-0"
	delegated, err = repos.Sessions.Create(t.Context(), delegated)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos, web, delegated
}

func TestOAuth2PostgresReuseCommitsOnlyItsSessionRevocation(t *testing.T) {
	_, repos, web, delegated := oauthPostgresFixture(t)
	ctx := t.Context()
	current := delegated.RefreshTokenHash
	for _, next := range []string{"oauth-refresh-1", "oauth-refresh-2", "oauth-refresh-3"} {
		rotated, err := repos.OAuth2.RotateRefresh(ctx, oauth2.Refresh{Hash: current, NewHash: next, ClientID: delegated.Delegation.ClientID, Resource: delegated.Delegation.Resource, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if !rotated.ExpiresAt.Equal(delegated.ExpiresAt) || rotated.PreviousRefreshTokenHash != "" {
			t.Fatalf("rotation changed fixed horizon or used browser grace: %+v", rotated)
		}
		current = next
	}
	input := oauth2.Refresh{Hash: delegated.RefreshTokenHash, NewHash: "oauth-reuse-result", ClientID: "wrong-client", Resource: delegated.Delegation.Resource, Now: time.Now()}
	if _, err := repos.OAuth2.RotateRefresh(ctx, input); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("wrong client: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, delegated.ID); err != nil {
		t.Fatalf("wrong client revoked connection: %v", err)
	}
	input.ClientID = delegated.Delegation.ClientID
	if _, err := repos.OAuth2.RotateRefresh(ctx, input); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("oldest spent hash: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, delegated.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reuse deletion rolled back: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, web.ID); err != nil {
		t.Fatalf("reuse revoked browser: %v", err)
	}
}

func TestOAuth2PostgresRefreshLocksUserBeforeSession(t *testing.T) {
	db, _, _, delegated := oauthPostgresFixture(t)
	schema := testSchema(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	trace := &passwordlessOwnerLockTrace{usersTable: schema.Table(usersTable), reached: make(chan struct{}, 1)}
	rotationDB, err := pgxdb.Open(ctx, pgxdb.Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), Tracer: trace})
	if err != nil {
		t.Fatal(err)
	}
	defer rotationDB.Close()
	done := make(chan error, 1)
	err = db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM `+schema.Table(usersTable)+` WHERE id=$1 FOR UPDATE`, delegated.UserID); err != nil {
			return err
		}
		go func() {
			_, err := NewOAuth2Store(rotationDB, storeOpts(schema)...).RotateRefresh(ctx, oauth2.Refresh{Hash: delegated.RefreshTokenHash, NewHash: "rotated-lock-proof", ClientID: delegated.Delegation.ClientID, Resource: delegated.Delegation.Resource, Now: time.Now()})
			done <- err
		}()
		select {
		case <-trace.reached:
		case <-ctx.Done():
			return ctx.Err()
		}
		// A globally revoking writer holding the user must remain able to lock the
		// session. A blocked refresh may not hold that later lock first.
		_, err := tx.Exec(ctx, `SELECT id FROM `+schema.Table(sessionsTable)+` WHERE id=$1 FOR UPDATE NOWAIT`, delegated.ID)
		return err
	})
	if err != nil {
		t.Fatalf("refresh acquired session before its user: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestOAuth2PostgresApprovalOutlivesBrowserButNotGlobalRevocation(t *testing.T) {
	_, repos, web, delegated := oauthPostgresFixture(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	code := oauth2.Code{Hash: "consent-code", UserID: web.UserID, AuthRevision: 0, Delegation: delegated.Delegation, RedirectURI: "https://client.example/callback", CodeChallenge: "s256-proof", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := repos.OAuth2.CreateCode(ctx, code, web.ID); err != nil {
		t.Fatal(err)
	}
	if err := repos.SessionManagement.DeleteForUser(ctx, web.ID, web.UserID); err != nil {
		t.Fatal(err)
	}
	proposed := delegated
	proposed.ID = "new-independent-session"
	proposed.RefreshTokenHash = "new-independent-refresh"
	if _, err := repos.OAuth2.RedeemCode(ctx, code, proposed, now); err != nil {
		t.Fatalf("browser logout invalidated approved code: %v", err)
	}
	web.ID = "second-browser-session"
	web.RefreshTokenHash = "second-browser-refresh"
	if _, err := repos.Sessions.Create(ctx, web); err != nil {
		t.Fatal(err)
	}
	code.Hash = "global-fenced-code"
	if err := repos.OAuth2.CreateCode(ctx, code, web.ID); err != nil {
		t.Fatal(err)
	}
	if err := repos.SessionManagement.RevokeAllForUser(ctx, web.UserID, now); err != nil {
		t.Fatal(err)
	}
	proposed.ID = "resurrection-attempt"
	proposed.RefreshTokenHash = "resurrection-refresh"
	if _, err := repos.OAuth2.RedeemCode(ctx, code, proposed, now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("global revoke did not fence approval: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, delegated.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("global revoke left delegated session: %v", err)
	}
}

func TestSessionRowPreservesDelegationAndRejectsCorruption(t *testing.T) {
	d := session.Delegation{AuthRevision: 42, Issuer: "issuer", ClientID: "client", Resource: "mcp", ExchangeClientID: "exchange", ExchangeResource: "api"}
	args, err := sessionArgs(session.Session{Profile: session.ProfileDelegated, Delegation: d})
	if err != nil {
		t.Fatal(err)
	}
	row := sessionRow{Profile: args["session_profile"].(string), Delegation: args["delegation"].(string)}
	got, err := row.toDomain()
	if err != nil || got.Profile != session.ProfileDelegated || got.Delegation != d {
		t.Fatalf("delegation round trip: %+v %v", got, err)
	}
	row.Delegation = "{bad"
	if _, err := row.toDomain(); err == nil {
		t.Fatal("malformed binding accepted")
	}
}
