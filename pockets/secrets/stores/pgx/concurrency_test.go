package pgx

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
)

// raceTimeout bounds each two-connection race: the store call, the barrier
// and the writer's commit.
const raceTimeout = 10 * time.Second

// blockedOnSQL returns the statement of a backend waiting on a lock the
// backend $1 holds, or no row while none is.
const blockedOnSQL = `SELECT query FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND $1 = ANY (pg_blocking_pids(pid)) LIMIT 1`

// TestLive_ConcurrentReplaceOwnerKey is the compare-and-swap proof: N rotators
// racing on one owner key with the same expectation produce exactly one
// replacement and N-1 sdk.ErrConflict, and exactly one audit record.
func TestLive_ConcurrentReplaceOwnerKey(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	repo := NewRepository(db, opts...)
	ctx := context.Background()
	owner := vault.Owner{Type: "tenant", ID: "race"}
	now := time.Now()
	if err := repo.CreateOwnerKey(ctx, vault.OwnerKey{Owner: owner, KeyID: "k1", WrappedKey: "w1", WrapperKeyID: "m1", CreatedAt: now, RotatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}

	const racers = 16
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		won       int
		conflicts int
		others    []error
	)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			record, err := vault.NewAuditRecord(time.Now(), vault.ActionOwnerKeyRewrapped, owner, "", 0, "k1", vault.Source{System: "race"})
			if err != nil {
				t.Error(err)
				return
			}
			key := vault.OwnerKey{Owner: owner, KeyID: "k2", WrappedKey: "w2", WrapperKeyID: "m2", CreatedAt: now, RotatedAt: now.Add(time.Duration(i) * time.Millisecond)}
			err = repo.ReplaceOwnerKey(ctx, key, "k1", nil, []vault.AuditRecord{record})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.Is(err, sdk.ErrConflict):
				conflicts++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won != 1 || conflicts != racers-1 || len(others) != 0 {
		t.Fatalf("won=%d conflicts=%d others=%v", won, conflicts, others)
	}
	audit, _, err := repo.ListAudit(ctx, vault.AuditFilter{Owner: &owner}, "", 100)
	if err != nil || len(audit) != 1 {
		t.Fatalf("audit: %+v %v", audit, err)
	}
}

// TestLive_DeleteOwnerVersusPutSecret is the lock-order proof against a
// write: connection A inserts a third secret, its foreign-key check holding
// FOR KEY SHARE on the owner key, while DeleteOwner runs. DeleteOwner waits on
// A, sees the committed secret and answers sdk.ErrConflict with nothing
// removed, never sdk.ErrInvalidReference, so the vault's relist-and-retry
// absorbs it. Every connection runs READ COMMITTED, the server default
// pgxdb.InTx inherits; under repeatable read the race is 40001 instead, which
// nothing retries.
func TestLive_DeleteOwnerVersusPutSecret(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	repo := NewRepository(db, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), raceTimeout)
	defer cancel()
	owner := vault.Owner{Type: "tenant", ID: "race"}
	seedOwner(t, ctx, repo, owner, "a", "b")

	writer, holder := beginWriter(t, ctx, db)
	insertSecret(t, ctx, writer, repo, owner, "c")
	records := deleteOwnerRecords(t, owner, "a", "b")
	result := race(func() error { return repo.DeleteOwner(ctx, owner, []string{"a", "b"}, records) })
	awaitBlockedOn(t, ctx, db, holder, result)
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit the racing write: %v", err)
	}
	if err := <-result; !errors.Is(err, sdk.ErrConflict) || errors.Is(err, sdk.ErrInvalidReference) {
		t.Fatalf("DeleteOwner behind a committed write: %v, want sdk.ErrConflict", err)
	}
	if _, err := repo.GetOwnerKey(ctx, owner); err != nil {
		t.Fatalf("a conflicting delete removed the owner key: %v", err)
	}
	if left := secretNames(t, ctx, repo, owner); len(left) != 3 {
		t.Fatalf("a conflicting delete removed secrets: %v", left)
	}
	if got := deletionRecords(t, ctx, repo, owner); len(got) != 0 {
		t.Fatalf("a conflicting delete recorded %+v", got)
	}

	// The vault over the same store: its first attempt waits behind a second
	// writer, conflicts when that writer commits, and the relist deletes all.
	svc, err := vault.NewService(repo, ringWrapper(t))
	if err != nil {
		t.Fatal(err)
	}
	second, holder := beginWriter(t, ctx, db)
	insertSecret(t, ctx, second, repo, owner, "d")
	result = race(func() error { return svc.DeleteOwner(ctx, owner, vault.Source{System: "race"}) })
	awaitBlockedOn(t, ctx, db, holder, result)
	if err := second.Commit(); err != nil {
		t.Fatalf("commit the second racing write: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("vault DeleteOwner did not absorb the conflict: %v", err)
	}
	requireOwnerGone(t, ctx, repo, owner, 4)
}

// TestLive_DeleteOwnerVersusRotation is the lock-order proof against a
// resealing ReplaceOwnerKey, which locks the owner key and then its values:
// connection A runs those two updates around DeleteOwner. DeleteOwner locks
// in the same order, so it waits for A and deletes what A wrote, never 40P01.
func TestLive_DeleteOwnerVersusRotation(t *testing.T) {
	dsn := requireDSN(t)
	db, opts := openAndMigrate(t, dsn)
	repo := NewRepository(db, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), raceTimeout)
	defer cancel()
	owner := vault.Owner{Type: "tenant", ID: "race"}
	seedOwner(t, ctx, repo, owner, "a", "b")

	writer, holder := beginWriter(t, ctx, db)
	if _, err := writer.Exec(ctx, `UPDATE `+repo.table("secrets_owner_keys")+` SET key_id = 'k2', wrapped_key = 'w2' WHERE owner_type = $1 AND owner_id = $2`, owner.Type, owner.ID); err != nil {
		t.Fatalf("the rotation's key update: %v", err)
	}
	records := deleteOwnerRecords(t, owner, "a", "b")
	result := race(func() error { return repo.DeleteOwner(ctx, owner, []string{"a", "b"}, records) })
	awaitBlockedOn(t, ctx, db, holder, result)
	if _, err := writer.Exec(ctx, `UPDATE `+repo.table("secrets_values")+` SET key_id = 'k2', ciphertext = 'resealed' WHERE owner_type = $1 AND owner_id = $2 AND name = 'a'`, owner.Type, owner.ID); err != nil {
		t.Fatalf("the rotation's reseal: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit the rotation: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("DeleteOwner behind a resealing rotation: %v", err)
	}
	requireOwnerGone(t, ctx, repo, owner, 2)
}

// beginWriter opens connection A's transaction and returns it with its
// backend pid. The rollback is registered after openAndMigrate's, so it runs
// first: a failed assertion never leaves the truncate waiting on A.
func beginWriter(t *testing.T, ctx context.Context, db *pgxdb.DB) (*pgxdb.Tx, int) {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

// race runs call in a goroutine and returns the channel its error arrives on.
func race(call func() error) <-chan error {
	result := make(chan error, 1)
	go func() { result <- call() }()
	return result
}

// awaitBlockedOn returns once a backend is waiting on a lock holder holds:
// the raced call, the only other client of these tables. It polls without
// sleeping; a call that returns first fails the test with its error.
func awaitBlockedOn(t *testing.T, ctx context.Context, db *pgxdb.DB, holder int, result <-chan error) {
	t.Helper()
	for {
		select {
		case err := <-result:
			t.Fatalf("the raced call returned before it waited on the writer: %v", err)
		default:
		}
		var query string
		err := db.QueryRow(ctx, blockedOnSQL, holder).Scan(&query)
		if err == nil {
			t.Logf("waiting on the writer: %s", query)
			return
		}
		if !errors.Is(pgxdb.MapError(err), sdk.ErrNotFound) {
			t.Fatalf("barrier: %v", err)
		}
	}
}

// seedOwner creates owner's key k1 and one secret per name.
func seedOwner(t *testing.T, ctx context.Context, repo *Repository, owner vault.Owner, names ...string) {
	t.Helper()
	now := time.Now()
	if err := repo.CreateOwnerKey(ctx, vault.OwnerKey{Owner: owner, KeyID: "k1", WrappedKey: "w1", WrapperKeyID: "m1", CreatedAt: now, RotatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		record, err := vault.NewAuditRecord(now, vault.ActionSecretPut, owner, name, 0, "k1", vault.Source{System: "race"})
		if err != nil {
			t.Fatal(err)
		}
		sealed := vault.SealedSecret{Ref: vault.Ref{Owner: owner, Name: name}, KeyID: "k1", Ciphertext: "ct", CreatedAt: now, UpdatedAt: now}
		if _, err := repo.PutSecret(ctx, sealed, record); err != nil {
			t.Fatal(err)
		}
	}
}

// insertSecret writes a secret inside writer, as PutSecret's insert does.
func insertSecret(t *testing.T, ctx context.Context, writer *pgxdb.Tx, repo *Repository, owner vault.Owner, name string) {
	t.Helper()
	if _, err := writer.Exec(ctx,
		`INSERT INTO `+repo.table("secrets_values")+` (owner_type, owner_id, name, version, key_id, ciphertext, created_at, updated_at) VALUES ($1, $2, $3, 1, 'k1', 'ct', now(), now())`,
		owner.Type, owner.ID, name); err != nil {
		t.Fatalf("the racing write: %v", err)
	}
}

// deleteOwnerRecords is what the vault hands DeleteOwner for names.
func deleteOwnerRecords(t *testing.T, owner vault.Owner, names ...string) []vault.AuditRecord {
	t.Helper()
	now := time.Now()
	var records []vault.AuditRecord
	for _, name := range names {
		record, err := vault.NewAuditRecord(now, vault.ActionSecretDeleted, owner, name, 0, "", vault.Source{System: "race"})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	record, err := vault.NewAuditRecord(now, vault.ActionOwnerKeyDestroyed, owner, "", 0, "k1", vault.Source{System: "race"})
	if err != nil {
		t.Fatal(err)
	}
	return append(records, record)
}

// requireOwnerGone checks owner's key and secrets are deleted, with one
// secret_deleted record per secret and one owner_key_destroyed record.
func requireOwnerGone(t *testing.T, ctx context.Context, repo *Repository, owner vault.Owner, secrets int) {
	t.Helper()
	if _, err := repo.GetOwnerKey(ctx, owner); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("owner key after DeleteOwner: %v", err)
	}
	if left := secretNames(t, ctx, repo, owner); len(left) != 0 {
		t.Fatalf("secrets after DeleteOwner: %v", left)
	}
	var deleted, destroyed int
	for _, rec := range deletionRecords(t, ctx, repo, owner) {
		switch rec.Action {
		case vault.ActionSecretDeleted:
			deleted++
		case vault.ActionOwnerKeyDestroyed:
			destroyed++
		}
	}
	if deleted != secrets || destroyed != 1 {
		t.Fatalf("recorded %d secret_deleted and %d owner_key_destroyed, want %d and 1", deleted, destroyed, secrets)
	}
}

func secretNames(t *testing.T, ctx context.Context, repo *Repository, owner vault.Owner) []string {
	t.Helper()
	secrets, err := repo.ListSecrets(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(secrets))
	for i, sec := range secrets {
		names[i] = sec.Ref.Name
	}
	return names
}

// deletionRecords returns owner's secret_deleted and owner_key_destroyed
// records.
func deletionRecords(t *testing.T, ctx context.Context, repo *Repository, owner vault.Owner) []vault.AuditRecord {
	t.Helper()
	var out []vault.AuditRecord
	for _, action := range []vault.Action{vault.ActionSecretDeleted, vault.ActionOwnerKeyDestroyed} {
		got, _, err := repo.ListAudit(ctx, vault.AuditFilter{Owner: &owner, Action: action}, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, got...)
	}
	return out
}

func ringWrapper(t *testing.T) vault.Wrapper {
	t.Helper()
	enc, err := cryptids.NewAESGCMRing(cryptids.KeyRing{Active: "m1", Keys: map[string][]byte{"m1": bytes.Repeat([]byte{1}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	w, err := vault.NewRingWrapper(enc)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
