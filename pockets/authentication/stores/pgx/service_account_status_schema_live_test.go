package pgx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestServiceAccountStatusSchema_Postgres pins the owner ruling on 0027
// against a live database without it: Repositories still constructs (0027 is
// never probed in every mode), the status store's ProbeSchema refuses naming
// the file, the pocket boots with the status unwired, and refuses with
// ErrServiceAccountStatusSchemaMissing once AdministrationConfig.
// ServiceAccountStatus is set.
func TestServiceAccountStatusSchema_Postgres(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "sa_status_unmigrated")
	assertNotOnSearchPath(t, db, schema)
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DROP TABLE "+schema.Table(serviceAccountStatusTable)); err != nil {
		t.Fatalf("drop %s: %v", serviceAccountStatusTable, err)
	}

	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0027 = %v, want it to construct (the status is opt-in)", err)
	}
	storetest.RunServiceAccountStatusSchemaProbe(t, func(*testing.T) serviceaccount.StatusRepository { return repos.ServiceAccountStatus })
	if err := repos.ServiceAccountStatus.ProbeSchema(ctx); err == nil || !strings.Contains(err.Error(), serviceAccountStatusMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, serviceAccountStatusMigration)
	}

	key := []byte("sa-status-schema-test-key-32-byt")
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	newPocket := func(opts ...auth.Option) error {
		_, err := auth.New(repos, identifierHTTPTestSigner{}, environment.ModeDevelopment, delivery.ModeOff, append([]auth.Option{
			auth.WithPassword(auth.PasswordConfig{PasswordFlowsDisabled: true, RequireVerifiedEmail: true}),
			auth.WithIdentity(auth.IdentityConfig{ChallengeProtector: protector}),
			auth.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		}, opts...)...)
		return err
	}
	if err := newPocket(); err != nil {
		t.Fatalf("the pocket with the status unwired over a database without 0027 = %v, want it to boot", err)
	}
	err = newPocket(auth.WithAdministration(auth.AdministrationConfig{ServiceAccountStatus: true}))
	if !errors.Is(err, auth.ErrServiceAccountStatusSchemaMissing) || !strings.Contains(err.Error(), serviceAccountStatusMigration) {
		t.Fatalf("the pocket with the status wired over a database without 0027 = %v, want ErrServiceAccountStatusSchemaMissing naming %s", err, serviceAccountStatusMigration)
	}
	t.Logf("wired service-account status refused: %v", err)
}
