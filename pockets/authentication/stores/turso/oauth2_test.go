package turso

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	_ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func openOAuthSQLite(t *testing.T, path string) *tursodb.DB {
	t.Helper()
	db, err := tursodb.Open(t.Context(), tursodb.Config{URL: "file:" + path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func oauthSQLite(t *testing.T) (*tursodb.DB, auth.Repositories, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth.db")
	db := openOAuthSQLite(t, path)
	if err := tursodb.RunMigrations(t.Context(), db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return db, repos, path
}

func TestConformance_LocalSQLite(t *testing.T) {
	storetest.Run(t, func(t *testing.T) auth.Repositories {
		_, repos, _ := oauthSQLite(t)
		return repos
	})
}

func seedOAuthBrowser(t *testing.T, db *tursodb.DB, id string, now time.Time) session.Session {
	t.Helper()
	if _, err := db.Exec(t.Context(), `INSERT INTO users (id, display_name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, id, tursodb.FormatTime(now), tursodb.FormatTime(now)); err != nil {
		t.Fatal(err)
	}
	sess, _ := session.NewSession(id, time.Hour, now)
	sess.RefreshTokenHash = "web-" + sess.ID
	if _, err := NewSessionStore(db).Create(t.Context(), sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func oauthProposal(ownerID, id string, now time.Time) (oauth2.Code, session.Session) {
	delegation := session.Delegation{
		Issuer: "https://issuer.example", ClientID: "https://client.example/metadata.json", Resource: "https://mcp.example",
		ExchangeResource: "https://api.example", ExchangeClientID: "mcp-confidential-client",
	}
	code := oauth2.Code{
		Hash: "code-" + id, UserID: ownerID, Delegation: delegation,
		RedirectURI: "https://client.example/callback", CodeChallenge: "challenge-" + id,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	sess := session.Session{ID: id, UserID: ownerID, Profile: session.ProfileDelegated, Delegation: delegation,
		RefreshTokenHash: "refresh-" + id, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	return code, sess
}

func seedOAuthGrant(t *testing.T, repos auth.Repositories, browser session.Session, id string, now time.Time) session.Session {
	t.Helper()
	code, proposed := oauthProposal(browser.UserID, id, now)
	if err := repos.OAuth2.CreateCode(t.Context(), code, browser.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func requireOAuthLive(t *testing.T, repos auth.Repositories, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := repos.Sessions.Get(t.Context(), id); err != nil {
			t.Fatalf("session %s should remain live: %v", id, err)
		}
	}
}

func TestOAuthSQLiteRefreshHistoryAndIndependentRevocation(t *testing.T) {
	db, repos, path := oauthSQLite(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	web := seedOAuthBrowser(t, db, "owner", now)
	a := seedOAuthGrant(t, repos, web, "a", now)
	b := seedOAuthGrant(t, repos, web, "b", now)
	if err := repos.Sessions.Rotate(t.Context(), a.ID, a.RefreshTokenHash, "legacy-rotation"); !errors.Is(err, session.ErrRotationConflict) {
		t.Fatalf("legacy rotation accepted delegated session: %v", err)
	}
	original := a.RefreshTokenHash
	for i := range 3 {
		next := fmt.Sprintf("rotated-%d", i)
		got, err := repos.OAuth2.RotateRefresh(t.Context(), oauth2.Refresh{Hash: a.RefreshTokenHash, NewHash: next,
			ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: now})
		if err != nil || got.RefreshTokenHash != next || !got.ExpiresAt.Equal(a.ExpiresAt) || got.Delegation != a.Delegation || got.Profile != session.ProfileDelegated {
			t.Fatalf("rotation lost grant binding or horizon: %+v %v", got, err)
		}
		a = got
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openOAuthSQLite(t, path)
	repos, err := Repositories(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.OAuth2.Prune(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []oauth2.Refresh{
		{Hash: original, NewHash: "never", ClientID: "another-client", Resource: a.Delegation.Resource, Now: now},
		{Hash: original, NewHash: "never", ClientID: a.Delegation.ClientID, Resource: "another-resource", Now: now},
	} {
		if _, err := repos.OAuth2.RotateRefresh(t.Context(), bad); !errors.Is(err, oauth2.ErrInvalidGrant) {
			t.Fatalf("foreign binding: %v", err)
		}
		requireOAuthLive(t, repos, web.ID, a.ID, b.ID)
	}
	if err := repos.OAuth2.RevokeRefresh(t.Context(), original, "another-client"); err != nil {
		t.Fatal(err)
	}
	requireOAuthLive(t, repos, a.ID)
	if _, err := repos.OAuth2.RotateRefresh(t.Context(), oauth2.Refresh{Hash: original, NewHash: "never",
		ClientID: a.Delegation.ClientID, Resource: a.Delegation.Resource, Now: now}); !errors.Is(err, oauth2.ErrRefreshReuse) {
		t.Fatalf("oldest spent hash must detect reuse after restart/prune: %v", err)
	}
	if _, err := repos.Sessions.Get(t.Context(), a.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reuse revocation not committed: %v", err)
	}
	requireOAuthLive(t, repos, web.ID, b.ID)
	if err := repos.OAuth2.RevokeRefresh(t.Context(), b.RefreshTokenHash, b.Delegation.ClientID); err != nil {
		t.Fatal(err)
	}
	requireOAuthLive(t, repos, web.ID)
}

func TestOAuthSQLiteCodeFencesAndBrowserIndependence(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	web := seedOAuthBrowser(t, db, "owner", now)
	other := seedOAuthBrowser(t, db, "other", now)
	code, proposed := oauthProposal(web.UserID, "grant", now)
	if err := repos.OAuth2.CreateCode(t.Context(), code, other.ID); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("foreign approving session: %v", err)
	}
	if err := repos.OAuth2.CreateCode(t.Context(), code, web.ID); err != nil {
		t.Fatal(err)
	}
	wrong := code
	wrong.RedirectURI = "https://evil.example/callback"
	if _, err := repos.OAuth2.RedeemCode(t.Context(), wrong, proposed, now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("changed redirect: %v", err)
	}
	if err := repos.SessionManagement.DeleteForUser(t.Context(), web.ID, web.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now); err != nil {
		t.Fatalf("browser logout invalidated independent approval: %v", err)
	}
	if _, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("code redeemed twice: %v", err)
	}
	code2, proposed2 := oauthProposal(other.UserID, "blocked-after-global", now)
	if err := repos.OAuth2.CreateCode(t.Context(), code2, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := repos.SessionManagement.RevokeAllForUser(t.Context(), other.UserID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.OAuth2.RedeemCode(t.Context(), code2, proposed2, now); !errors.Is(err, oauth2.ErrInvalidGrant) {
		t.Fatalf("global revocation allowed stale approval: %v", err)
	}
	requireOAuthLive(t, repos, proposed.ID)
	if _, err := db.Exec(t.Context(), `UPDATE users SET auth_revision = auth_revision + 1 WHERE id = ?`, web.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.OAuth2.RotateRefresh(t.Context(), oauth2.Refresh{Hash: proposed.RefreshTokenHash, NewHash: "after-revision",
		ClientID: proposed.Delegation.ClientID, Resource: proposed.Delegation.Resource, Now: now}); err != nil {
		t.Fatalf("unrelated revision invalidated existing grant: %v", err)
	}
}

func TestOAuthSQLiteConcurrentRedemptionAndRefresh(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	web := seedOAuthBrowser(t, db, "owner", now)
	code, proposed := oauthProposal(web.UserID, "grant", now)
	if err := repos.OAuth2.CreateCode(t.Context(), code, web.ID); err != nil {
		t.Fatal(err)
	}
	run := func(fn func(int) error) []error {
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { <-start; errs[i] = fn(i) })
		}
		close(start)
		wg.Wait()
		return errs
	}
	errs := run(func(int) error { _, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now); return err })
	assertOneOAuthWinner(t, errs, oauth2.ErrInvalidGrant)
	errs = run(func(i int) error {
		_, err := repos.OAuth2.RotateRefresh(t.Context(), oauth2.Refresh{Hash: proposed.RefreshTokenHash, NewHash: fmt.Sprintf("winner-%d", i),
			ClientID: proposed.Delegation.ClientID, Resource: proposed.Delegation.Resource, Now: now})
		return err
	})
	assertOneOAuthWinner(t, errs, oauth2.ErrRefreshReuse)
	if _, err := repos.Sessions.Get(t.Context(), proposed.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("concurrent reuse did not revoke winner: %v", err)
	}
	requireOAuthLive(t, repos, web.ID)
}

func assertOneOAuthWinner(t *testing.T, errs []error, loser error) {
	t.Helper()
	wins, losses := 0, 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if errors.Is(err, loser) {
			losses++
		} else {
			t.Fatalf("unexpected concurrency error: %v", err)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("winners %d / losers %d: %v", wins, losses, errs)
	}
}

func TestOAuthSQLiteAtomicRollbackAndOwnerManagement(t *testing.T) {
	db, repos, _ := oauthSQLite(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	web := seedOAuthBrowser(t, db, "owner", now)
	other := seedOAuthBrowser(t, db, "other", now)
	code, proposed := oauthProposal(web.UserID, "grant", now)
	if err := repos.OAuth2.CreateCode(t.Context(), code, web.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `CREATE TRIGGER fail_code_delete BEFORE DELETE ON oauth_authorization_codes BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err := repos.Sessions.Get(t.Context(), proposed.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("partial session creation: %v", err)
	}
	if _, err := repos.OAuth2.GetCode(t.Context(), code.Hash); err != nil {
		t.Fatalf("failed transaction consumed code: %v", err)
	}
	if _, err := db.Exec(t.Context(), `DROP TRIGGER fail_code_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.OAuth2.RedeemCode(t.Context(), code, proposed, now); err != nil {
		t.Fatal(err)
	}
	if err := repos.SessionManagement.DeleteForUser(t.Context(), proposed.ID, other.UserID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("cross-user delete: %v", err)
	}
	if err := repos.OAuth2.RevokeGrant(t.Context(), proposed.ID, web.UserID, "another-client"); err != nil {
		t.Fatal(err)
	}
	requireOAuthLive(t, repos, proposed.ID)
	page, err := repos.SessionManagement.ListByUser(t.Context(), web.UserID, list.Request{Limit: 1, WithCount: true})
	if err != nil || len(page.Items) != 1 || page.Total == nil || *page.Total != 2 || !page.HasMore {
		t.Fatalf("owner page: %+v %v", page, err)
	}
	second, err := repos.SessionManagement.ListByUser(t.Context(), web.UserID, list.Request{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID || second.Items[0].UserID != web.UserID {
		t.Fatalf("second owner page: %+v %v", second, err)
	}
	if err := repos.SessionManagement.RevokeAllForUser(t.Context(), web.UserID, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{web.ID, proposed.ID} {
		if _, err := repos.Sessions.Get(t.Context(), id); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("global revoke left %s: %v", id, err)
		}
	}
	owner, err := repos.Users.Get(t.Context(), web.UserID)
	if err != nil || owner.AuthRevision != 1 {
		t.Fatalf("global revocation revision: %+v %v", owner, err)
	}
	requireOAuthLive(t, repos, other.ID)
}

func TestOAuthSQLiteUpgradePreservesWebSession(t *testing.T) {
	db := openOAuthSQLite(t, filepath.Join(t.TempDir(), "upgrade.db"))
	prior := fstest.MapFS{}
	for _, name := range migrationNames(t) {
		if name >= "0019_oauth2_sessions.sql" {
			continue
		}
		data, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+name)
		if err != nil {
			t.Fatal(err)
		}
		prior[MigrationsDir+"/"+name] = &fstest.MapFile{Data: data}
	}
	ctx := context.Background()
	if err := tursodb.RunMigrations(ctx, db, prior, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `INSERT INTO sessions (id,user_id,refresh_token_hash,previous_refresh_token_hash,previous_used,rotation_count,created_at,expires_at)
		VALUES ('legacy','owner','legacy-current','legacy-previous',1,7,?,?)`, tursodb.FormatTime(now), tursodb.FormatTime(now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unmigrated boot: %v", err)
	}
	if err := tursodb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repos.Sessions.Get(ctx, "legacy")
	if err != nil || !got.FirstParty() || got.RefreshTokenHash != "legacy-current" || got.PreviousRefreshTokenHash != "legacy-previous" || !got.PreviousUsed || got.RotationCount != 7 || !got.CreatedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("upgrade changed web session: %+v %v", got, err)
	}
	if _, err := db.Exec(ctx, `ALTER TABLE sessions DROP COLUMN delegation`); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "0019_oauth2_sessions.sql") {
		t.Fatalf("missing grant column passed boot: %v", err)
	}
}
