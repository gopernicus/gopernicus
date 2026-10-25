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
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestTOTPSchema_Postgres pins the owner ruling on 0026 against a live
// database that stopped at 0025: Repositories still constructs (0026 is never
// probed in every mode), the TOTP store's ProbeSchema refuses naming the file,
// the pocket boots with TOTP unwired, and refuses with ErrTOTPSchemaMissing
// once WithTOTP is wired.
func TestTOTPSchema_Postgres(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "totp_unmigrated")
	assertNotOnSearchPath(t, db, schema)
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	for _, table := range totpProbeTables {
		if _, err := db.Exec(ctx, "DROP TABLE "+schema.Table(table)); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0026 = %v, want it to construct (TOTP is opt-in)", err)
	}
	storetest.RunTOTPSchemaProbe(t, func(*testing.T) totp.Repository { return repos.TOTP })
	if err := repos.TOTP.(totp.SchemaProber).ProbeSchema(ctx); err == nil || !strings.Contains(err.Error(), totpMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, totpMigration)
	}

	key := []byte("totp-schema-test-key-32-bytes-ok")
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	ring, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "t1", Keys: map[string][]byte{"t1": []byte(strings.Repeat("t", 32))}})
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
		t.Fatalf("the pocket with TOTP unwired over a database without 0026 = %v, want it to boot", err)
	}
	err = newPocket(auth.WithTOTP(auth.TOTPConfig{Encrypter: ring, Issuer: "Example"}))
	if !errors.Is(err, auth.ErrTOTPSchemaMissing) || !strings.Contains(err.Error(), totpMigration) {
		t.Fatalf("the pocket with TOTP wired over a database without 0026 = %v, want ErrTOTPSchemaMissing naming %s", err, totpMigration)
	}
	t.Logf("wired TOTP refused: %v", err)
}
