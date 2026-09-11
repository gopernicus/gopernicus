package pgx

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	jackpgx "github.com/jackc/pgx/v5"
)

type passwordlessOwnerLockTrace struct {
	usersTable string
	reached    chan struct{}
}

func (tr *passwordlessOwnerLockTrace) TraceQueryStart(ctx context.Context, _ *jackpgx.Conn, q jackpgx.TraceQueryStartData) context.Context {
	if strings.Contains(q.SQL, "FROM "+tr.usersTable) && strings.Contains(q.SQL, "FOR UPDATE") {
		select {
		case tr.reached <- struct{}{}:
		default:
		}
	}
	return ctx
}
func (*passwordlessOwnerLockTrace) TraceQueryEnd(context.Context, *jackpgx.Conn, jackpgx.TraceQueryEndData) {
}

func TestPasswordlessLocksOwnerBeforeIdentifierAndChallenge(t *testing.T) {
	db := probeDial(t)
	schema := testSchema(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pgxdb.RunMigrations(ctx, db, MigrationsFS, MigrationsDir, migrateOpts(schema)...); err != nil {
		t.Fatal(err)
	}
	truncate(t, db, schema)
	t.Cleanup(func() { truncate(t, db, schema) })
	repos, err := Repositories(context.Background(), db, storeOpts(schema)...)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	owner := user.User{ID: "lock-order-owner", Status: user.StatusActive, CreatedAt: now, UpdatedAt: now}
	ident := identifier.Identifier{ID: "lock-order-identifier", Kind: identifier.KindEmail, NormalizedValue: "locks@example.com", LoginEnabled: true, RecoveryEnabled: true, IsPrimary: true, VerifiedAt: now, CreatedAt: now, UpdatedAt: now}
	owner, ident, err = repos.Users.CreateWithPrimaryIdentifier(ctx, owner, ident)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal(passwordless.Binding{Version: passwordless.BindingVersion, Kind: "email", NormalizedValue: ident.NormalizedValue, UserID: owner.ID, IdentifierID: ident.ID, AuthRevision: &owner.AuthRevision})
	ch, err := repos.Challenges.Replace(ctx, challenge.Challenge{UserID: owner.ID, Purpose: challenge.PurposeLoginMagicLink, SecretDigest: "lock-proof", Context: binding, ExpiresAt: now.Add(time.Hour), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	trace := &passwordlessOwnerLockTrace{usersTable: schema.Table(usersTable), reached: make(chan struct{}, 1)}
	redeemDB, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: os.Getenv("POSTGRES_TEST_DSN"), Tracer: trace})
	if err != nil {
		t.Fatal(err)
	}
	defer redeemDB.Close()
	proposed, _ := session.NewSession(owner.ID, time.Hour, now)
	proposed.RefreshTokenHash = "lock-order-refresh"
	done := make(chan error, 1)
	err = db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT id FROM "+schema.Table(usersTable)+" WHERE id=$1 FOR UPDATE", owner.ID); err != nil {
			return err
		}
		go func() {
			_, err := NewPasswordlessStore(redeemDB, storeOpts(schema)...).Redeem(ctx, passwordless.RedeemInput{Purpose: challenge.PurposeLoginMagicLink, TokenDigest: ch.SecretDigest, Session: proposed, Now: now})
			done <- err
		}()
		select {
		case <-trace.reached:
		case <-ctx.Done():
			return ctx.Err()
		}
		// The blocked redemption must hold neither of these locks. Taking them
		// NOWAIT also proves a credential writer can finish and release the owner.
		if _, err := tx.Exec(ctx, "SELECT id FROM "+schema.Table(identifiersTable)+" WHERE id=$1 FOR UPDATE NOWAIT", ident.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "SELECT id FROM "+schema.Table(challengesTable)+" WHERE id=$1 FOR UPDATE NOWAIT", ch.ID)
		return err
	})
	if err != nil {
		t.Fatalf("redemption held a later lock before its owner: %v", err)
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
