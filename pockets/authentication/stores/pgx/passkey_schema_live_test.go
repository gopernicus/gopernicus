package pgx

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passkey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/authentication/webauthn"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// plainHasher is a reversible test hasher: the live leg proves the store, not
// password hashing.
type plainHasher struct{}

func (plainHasher) HashPassword(password string) (string, error) { return "plain:" + password, nil }

func (plainHasher) VerifyPassword(hash, password string) error {
	if hash != "plain:"+password {
		return sdk.ErrUnauthorized
	}
	return nil
}

// stubRelyingParty is enough relying party for New to wire passkeys; no
// ceremony runs.
type stubRelyingParty struct {
	webauthn.RelyingParty
	cfg webauthn.Config
}

func (r stubRelyingParty) Config() webauthn.Config { return r.cfg }

// TestPasskeySchema_Postgres pins the owner ruling on 0025 against a live
// database migrated through 0024 only (no 0025, no 0026): Repositories
// constructs, the passkey store's ProbeSchema refuses naming the file, the
// credential rail and erasure work with passkeys off, the pocket boots with
// passkeys and TOTP unwired and serves a password change, the
// method inventory and an erasure, and refuses with ErrPasskeySchemaMissing
// once WithPasskeys is wired.
func TestPasskeySchema_Postgres(t *testing.T) {
	db := probeDial(t)
	ctx := context.Background()
	schema := disposableSchema(t, "passkey_unmigrated")
	assertNotOnSearchPath(t, db, schema)
	dropSchema(t, db, schema)
	t.Cleanup(func() { dropSchema(t, db, schema) })
	through0024 := fstest.MapFS{}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= passkeyMigration {
			continue
		}
		name := MigrationsDir + "/" + entry.Name()
		data, err := fs.ReadFile(MigrationsFS, name)
		if err != nil {
			t.Fatal(err)
		}
		through0024[name] = &fstest.MapFile{Data: data}
	}
	if err := pgxdb.RunMigrations(ctx, db, through0024, MigrationsDir, pgxdb.WithSchema(schema)); err != nil {
		t.Fatal(err)
	}
	for _, table := range append(append([]string{}, passkeyProbeTables...), totpProbeTables...) {
		var reg *string
		if err := db.QueryRow(ctx, `SELECT to_regclass($1)::text`, schema.Table(table)).Scan(&reg); err != nil || reg != nil {
			t.Fatalf("%s present after migrating through 0024 (%v)", table, err)
		}
	}

	repos, err := Repositories(ctx, db, WithSchema(schema))
	if err != nil {
		t.Fatalf("Repositories without 0025 = %v, want it to construct (passkeys are opt-in)", err)
	}
	storetest.RunPasskeySchemaProbe(t, func(*testing.T) passkey.Repository { return repos.Passkeys })
	if err := repos.Passkeys.(passkey.SchemaProber).ProbeSchema(ctx); err == nil || !strings.Contains(err.Error(), passkeyMigration) {
		t.Fatalf("ProbeSchema = %v, want an error naming %s", err, passkeyMigration)
	}
	storetest.RunPasskeysUnwired(t, func(*testing.T) auth.Repositories { return repos })

	key := []byte("passkey-schema-test-key-32-bytes")
	protector, err := protection.NewHMACChallengeProtector(protection.HMACKeyRing{Active: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	newPocket := func(opts ...auth.Option) (*auth.Components, error) {
		return auth.New(repos, identifierHTTPTestSigner{}, environment.ModeDevelopment, delivery.ModeOff, append([]auth.Option{
			auth.WithPassword(auth.PasswordConfig{Hasher: plainHasher{}}),
			auth.WithIdentity(auth.IdentityConfig{ChallengeProtector: protector, SingleFactorAcknowledged: true}),
			auth.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		}, opts...)...)
	}
	components, err := newPocket()
	if err != nil {
		t.Fatalf("the pocket with passkeys and TOTP unwired over a database through 0024 = %v, want it to boot", err)
	}
	svc := components.Authentication
	now := time.Now().UTC()
	u, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx,
		user.User{ID: "unwired-0025", Status: user.StatusActive, CreatedAt: now, UpdatedAt: now},
		identifier.Identifier{ID: "unwired-0025-email", Kind: identifier.KindEmail,
			NormalizedValue: "unwired-0025@example.invalid", LoginEnabled: true,
			RecoveryEnabled: true, NotificationEnabled: true, IsPrimary: true,
			VerifiedAt: now, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := repos.Passwords.Set(ctx, u.ID, "plain:first-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePassword(ctx, u.ID, "first-password-123", "second-password-456"); err != nil {
		t.Fatalf("ChangePassword without 0025: %v", err)
	}
	view, err := svc.Methods(ctx, u.ID)
	if err != nil || !view.HasPassword || view.Passkeys != nil {
		t.Fatalf("Methods without 0025 = %+v, %v; want the password and no passkeys", view, err)
	}
	_, erasure, err := svc.EraseUser(ctx, authlogic.Principal{Type: authlogic.PrincipalUser, ID: "operator"}, u.ID)
	if err != nil || !erasure.Changed || erasure.Counts.Passkeys != 0 || erasure.Counts.Passwords != 1 {
		t.Fatalf("EraseUser without 0025 = %+v, %v; want it erased with no passkeys counted", erasure, err)
	}
	t.Logf("unwired over 0024: changed the password, listed methods, erased (%+v)", erasure.Counts)

	origins, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{"https://example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	rp := stubRelyingParty{cfg: webauthn.Config{RPID: "example.com", RPName: "Example", Origins: origins}}
	_, err = newPocket(auth.WithPasskeys(auth.PasskeyConfig{RelyingParty: rp}))
	if !errors.Is(err, auth.ErrPasskeySchemaMissing) || !strings.Contains(err.Error(), passkeyMigration) {
		t.Fatalf("the pocket with passkeys wired over a database without 0025 = %v, want ErrPasskeySchemaMissing naming %s", err, passkeyMigration)
	}
	t.Logf("wired passkeys refused: %v", err)
}
