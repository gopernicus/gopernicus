package turso

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestRefreshGraceNeverBlanketProbed pins 0031 out of Repositories' blanket
// column probe: the columns are required only when the host sets
// oauth2.Config.RefreshReuseGrace, and OAuth2Store.ProbeRefreshGraceSchema
// asks then.
func TestRefreshGraceNeverBlanketProbed(t *testing.T) {
	for _, pc := range probeColumns {
		if pc.migration == refreshSuccessorMigration || pc.table == "oauth_refresh_history" {
			t.Fatalf("probeColumns lists %+v; 0031 must be probed only through OAuth2Store.ProbeRefreshGraceSchema", pc)
		}
	}
}

// TestRefreshGraceSchema_LocalSQLite pins the conditional rule on a database
// without 0031: Repositories constructs, the plain rotation and Prune work
// without naming the columns, and ProbeRefreshGraceSchema refuses with
// sdk.ErrNotFound naming the file. A closed connection is never
// schema-missing.
func TestRefreshGraceSchema_LocalSQLite(t *testing.T) {
	db, _, _ := oauthSQLite(t)
	ctx := t.Context()
	for _, column := range refreshSuccessorColumnNames {
		if _, err := db.Exec(ctx, "ALTER TABLE oauth_refresh_history DROP COLUMN "+column); err != nil {
			t.Fatalf("drop %s: %v", column, err)
		}
	}
	repos, err := Repositories(ctx, db)
	if err != nil {
		t.Fatalf("Repositories without 0031 = %v, want it to construct (the grace is opt-in)", err)
	}
	store := repos.OAuth2.(*OAuth2Store)
	err = store.ProbeRefreshGraceSchema(ctx)
	if !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), refreshSuccessorMigration) {
		t.Fatalf("ProbeRefreshGraceSchema without 0031 = %v, want sdk.ErrNotFound naming %s", err, refreshSuccessorMigration)
	}

	now := time.Now().UTC()
	grant := seedOAuthGrant(t, repos, seedOAuthBrowser(t, db, "u-grace-plain", now), "grace-plain", now)
	if _, err := store.RotateRefresh(ctx, oauth2.Refresh{Hash: grant.RefreshTokenHash, NewHash: "grace-plain-next", ClientID: grant.Delegation.ClientID, Resource: grant.Delegation.Resource, Now: now}); err != nil {
		t.Fatalf("plain RotateRefresh without 0031 = %v, want success", err)
	}
	if err := store.Prune(ctx, now); err != nil {
		t.Fatalf("Prune without 0031 = %v, want success", err)
	}

	db.Close()
	if err := store.ProbeRefreshGraceSchema(ctx); err == nil || errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ProbeRefreshGraceSchema on a closed connection = %v, want an infrastructure error, never sdk.ErrNotFound", err)
	}
}
