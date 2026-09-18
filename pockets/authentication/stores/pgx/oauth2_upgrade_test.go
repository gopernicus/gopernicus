package pgx

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestOAuth2SessionUpgradePreservesBrowserSession(t *testing.T) {
	db := probeDial(t)
	schema := testSchema(t)
	ctx := t.Context()
	probeDropAll(t, db)
	t.Cleanup(func() { probeResetSchema(t, db) })
	prior := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "0019_oauth2_sessions.sql" {
			continue
		}
		name := MigrationsDir + "/" + entry.Name()
		data, err := fs.ReadFile(MigrationsFS, name)
		if err != nil {
			t.Fatal(err)
		}
		prior[name] = &fstest.MapFile{Data: data}
	}
	if err := pgxdb.RunMigrations(ctx, db, prior, MigrationsDir, migrateOpts(schema)...); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, `INSERT INTO `+schema.Table(sessionsTable)+` (id,user_id,refresh_token_hash,previous_refresh_token_hash,previous_used,rotation_count,created_at,expires_at)
 VALUES ('legacy-browser','legacy-user','current-browser-hash','previous-browser-hash',true,7,$1,$2)`, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := Repositories(ctx, db, storeOpts(schema)...); !errors.Is(err, sdk.ErrNotFound) || !strings.Contains(err.Error(), "0019_oauth2_sessions.sql") {
		t.Fatalf("boot without OAuth migration: %v", err)
	}
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, migrateOpts(schema)...); err != nil {
		t.Fatal(err)
	}
	repos, err := Repositories(ctx, db, storeOpts(schema)...)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repos.Sessions.Get(ctx, "legacy-browser")
	if err != nil || !got.FirstParty() || got.Profile != session.ProfileFirstParty || got.Delegation != (session.Delegation{}) || got.RefreshTokenHash != "current-browser-hash" || got.PreviousRefreshTokenHash != "previous-browser-hash" || !got.PreviousUsed || got.RotationCount != 7 || !got.CreatedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("migration changed legacy browser: %+v %v", got, err)
	}
}
