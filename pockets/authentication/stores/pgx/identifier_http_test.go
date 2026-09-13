package pgx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Only token cryptography is a stub: the mounted middleware resolves the user,
// live session and recent authentication from the real PostgreSQL repositories.
type identifierHTTPTestSigner struct{ userID, sessionID string }

func (s identifierHTTPTestSigner) Sign(map[string]any, time.Time) (string, error) {
	return "identifier.http.test-token", nil
}

func (s identifierHTTPTestSigner) Verify(token string) (map[string]any, error) {
	if token != "identifier.http.test-token" {
		return nil, sdk.ErrUnauthorized
	}
	return map[string]any{"user_id": s.userID, "session_id": s.sessionID}, nil
}

func TestIdentifierRemovalHTTP_Postgres_PasswordAndDeliveryDisabled(t *testing.T) {
	for _, tc := range []struct {
		name          string
		targetPrimary bool
		replacement   string
		wantStatus    int
	}{
		{"foreign_secondary_reproducer", false, "foreign-secondary", http.StatusNotFound},
		{"foreign_primary_replacement", true, "foreign-secondary", http.StatusNotFound},
		{"valid_same_user_replacement", true, "actor-replacement", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := probeDial(t)
			ctx := context.Background()
			schema := disposableSchema(t, "identifier_http")
			assertNotOnSearchPath(t, db, schema)
			t.Cleanup(func() { dropSchema(t, db, schema) })
			if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
				t.Fatal(err)
			}
			repos, err := Repositories(ctx, db, WithSchema(schema))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			for _, id := range []string{"actor", "foreign"} {
				_, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx,
					user.User{ID: id, Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
					identifier.Identifier{ID: id + "-email", Kind: identifier.KindEmail,
						NormalizedValue: id + "@example.invalid", LoginEnabled: true,
						RecoveryEnabled: true, NotificationEnabled: true, IsPrimary: true,
						VerifiedAt: now, CreatedAt: now, UpdatedAt: now})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = db.Exec(ctx, `INSERT INTO `+schema.Table(identifiersTable)+`
				(id, user_id, kind, normalized_value, is_primary, created_at, updated_at)
				VALUES ('actor-secondary', 'actor', 'phone', '+12025550101', $1, $2, $2),
				('foreign-secondary', 'foreign', 'phone', '+12025550102', FALSE, $2, $2),
				('actor-replacement', 'actor', 'phone', '+12025550103', FALSE, $2, $2)`, tc.targetPrimary, now)
			if err != nil {
				t.Fatal(err)
			}
			sess, _ := session.NewSession("actor", time.Hour, now)
			sess.RefreshTokenHash = "identifier-http-test-refresh"
			sess, err = repos.Sessions.Create(ctx, sess)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(ctx, `UPDATE `+schema.Table(sessionsTable)+`
				SET authenticated_at=$1, assurance_level='aal1',
				authentication_methods='[{"kind":"oauth","assurance":"aal1"}]' WHERE id=$2`, now, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			key := []byte("identifier-http-test-key-32-bytes")
			protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": key}})
			if err != nil {
				t.Fatal(err)
			}
			keyer, err := protection.NewHMACIdentifierKeyer(key)
			if err != nil {
				t.Fatal(err)
			}
			components, err := auth.New(repos, identifierHTTPTestSigner{"actor", sess.ID}, environment.ModeDevelopment, delivery.ModeOff,
				auth.WithPassword(auth.PasswordConfig{PasswordFlowsDisabled: true, RequireVerifiedEmail: true}),
				auth.WithIdentity(auth.IdentityConfig{ChallengeProtector: protector, IdentifierKeyer: keyer}),
				auth.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
			if err != nil {
				t.Fatal(err)
			}
			router := web.NewWebHandler()
			components.HTTP.Register(pockets.Mount{Router: router})
			before := identifierHTTPState(t, db, schema)
			req := httptest.NewRequest(http.MethodDelete, "/auth/identifiers/actor-secondary?replacement="+tc.replacement, nil)
			req.Header.Set("Authorization", "Bearer identifier.http.test-token")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("DELETE status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if after := identifierHTTPState(t, db, schema); after != before {
					t.Error("rejected removal changed persisted credentials, revisions or proof/session state")
				}
				return
			}
			var retired, promoted, foreignPrimary bool
			if err := db.QueryRow(ctx, `SELECT replaced_at IS NOT NULL FROM `+schema.Table(identifiersTable)+` WHERE id='actor-secondary'`).Scan(&retired); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT is_primary FROM `+schema.Table(identifiersTable)+` WHERE id='actor-replacement'`).Scan(&promoted); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT is_primary FROM `+schema.Table(identifiersTable)+` WHERE id='foreign-secondary'`).Scan(&foreignPrimary); err != nil {
				t.Fatal(err)
			}
			actor, err := repos.Users.Get(ctx, "actor")
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := repos.Users.Get(ctx, "foreign")
			if err != nil {
				t.Fatal(err)
			}
			if !retired || !promoted || foreignPrimary || actor.AuthRevision != 1 || foreign.AuthRevision != 0 {
				t.Fatalf("invalid successful removal state: retired=%v promoted=%v foreign-primary=%v revisions=%d/%d", retired, promoted, foreignPrimary, actor.AuthRevision, foreign.AuthRevision)
			}
		})
	}
}

func identifierHTTPState(t *testing.T, db *pgxdb.DB, schema pgxdb.Schema) string {
	t.Helper()
	var snapshot string
	for _, table := range []string{usersTable, identifiersTable, sessionsTable, challengesTable, contactChangesTable, authGrantsTable} {
		var rows string
		if err := db.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id), '[]'::jsonb)::text FROM `+schema.Table(table)+` r`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		snapshot += fmt.Sprintf("%s=%s\n", table, rows)
	}
	return snapshot
}
