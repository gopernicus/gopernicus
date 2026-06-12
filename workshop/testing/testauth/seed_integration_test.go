//go:build integration

// The Phase A acceptance test for the authenticated e2e stack: a session
// row seeded through the generated fixtures with a REAL token hash must
// authenticate through the engine end to end — fixture overrides +
// authentication.HashToken + AuthenticatorWithRepositories are the three
// primitives the generated e2e bootstrap composes.
package testauth_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/core/auth/authentication/satisfiers"
	"github.com/gopernicus/gopernicus/core/repositories/auth/sessions"
	"github.com/gopernicus/gopernicus/core/repositories/auth/sessions/sessionspgx"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users/userspgx"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/workshop/testing/fixtures"
	"github.com/gopernicus/gopernicus/workshop/testing/testauth"
	"github.com/gopernicus/gopernicus/workshop/testing/testpgx"
	"github.com/stretchr/testify/require"
)

func migrateTestDB(ctx context.Context, pool *pgxdb.Pool) error {
	return pgxdb.RunMigrations(ctx, pool, os.DirFS(projectRoot()), "workshop/migrations/primary")
}

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func TestSeededSessionAuthenticates(t *testing.T) {
	ctx := context.Background()
	db := testpgx.SetupTestPGX(t, ctx, testpgx.WithMigrations(migrateTestDB))
	log := slog.New(slog.DiscardHandler)

	usersRepo := users.NewRepository(userspgx.NewStore(log, db.Pool))
	sessionsRepo := sessions.NewRepository(sessionspgx.NewStore(log, db.Pool))
	repos := authentication.NewRepositories(
		satisfiers.NewUserSatisfier(usersRepo),
		nil,
		satisfiers.NewSessionSatisfier(sessionsRepo),
		nil,
		nil,
	)
	auth, signer := testauth.AuthenticatorWithRepositories("seedtest", repos)

	user := fixtures.CreateTestUserWithDefaults(t, ctx, db)

	// Seed a session whose stored hash matches a real minted token — the
	// override path; the generic default hash can never authenticate.
	token := testauth.MintAccessToken(signer, user.UserID)
	hash, err := authentication.HashToken(token)
	require.NoError(t, err)
	fixtures.CreateTestSession(t, ctx, db, user.UserID, map[string]any{
		"session_token_hash": hash,
	})

	gotUser, gotSession, err := auth.AuthenticateSession(ctx, token)
	require.NoError(t, err, "seeded session must authenticate end to end")
	require.Equal(t, user.UserID, gotUser.UserID)
	require.Equal(t, user.UserID, gotSession.UserID)

	// Controls use distinct users: same-user tokens minted within the same
	// second are byte-identical JWTs (second-resolution exp) and would
	// collide with the seeded row.

	// A valid JWT with no matching session row must fail — proves the row
	// lookup is real, not signature-only.
	orphanUser := fixtures.CreateTestUserWithDefaults(t, ctx, db)
	orphan := testauth.MintAccessToken(signer, orphanUser.UserID)
	_, _, err = auth.AuthenticateSession(ctx, orphan)
	require.Error(t, err, "token without a seeded session row must not authenticate")

	// The fixture's generic dummy hash must not authenticate — the exact
	// gap the overrides close.
	dummyUser := fixtures.CreateTestUserWithDefaults(t, ctx, db)
	fixtures.CreateTestSession(t, ctx, db, dummyUser.UserID)
	dummy := testauth.MintAccessToken(signer, dummyUser.UserID)
	_, _, err = auth.AuthenticateSession(ctx, dummy)
	require.Error(t, err, "dummy-hash fixture session must not match a real token")
}
