package pgx

import (
	"context"
	"errors"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/pockets/events/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestLockConformance runs the shared lock suite with two Locks over one pool,
// and the two-poller case over a live outbox store in the same schema.
func TestLockConformance(t *testing.T) {
	dsn := requireDSN(t)
	storetest.RunLock(t, func(t *testing.T) (a, b outbox.Lock) {
		db := openAndMigrate(t, dsn)
		return mustLock(t, db), mustLock(t, db)
	}, func(t *testing.T) outbox.EntryRepository {
		db := openAndMigrate(t, dsn)
		store, err := New(context.Background(), db, storeOptions(t)...)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return store
	})
}

// TestLockAcrossPools stands in for two processes: each Lock has its own pool.
// While A holds the lock, B does not run.
func TestLockAcrossPools(t *testing.T) {
	dsn := requireDSN(t)
	dbA, dbB := openAndMigrate(t, dsn), openAndMigrate(t, dsn)
	a, b := mustLock(t, dbA), mustLock(t, dbB)

	ran, err := a.TryRun(context.Background(), func(ctx context.Context) error {
		bRan, bErr := b.TryRun(ctx, func(context.Context) error {
			t.Error("B ran while A held the lock")
			return nil
		})
		if bRan || bErr != nil {
			t.Errorf("B.TryRun while held = (%v, %v), want (false, nil)", bRan, bErr)
		}
		return nil
	})
	if !ran || err != nil {
		t.Fatalf("A.TryRun = (%v, %v), want (true, nil)", ran, err)
	}
	mustTryRun(t, b)
}

// TestLockHolderBackendTerminated kills the lock transaction's backend while A
// is inside its batch. The lock goes with it: B can run while A is still in fn,
// and A's TryRun still reports its own batch's result.
func TestLockHolderBackendTerminated(t *testing.T) {
	dsn := requireDSN(t)
	dbA, dbB, admin := openAndMigrate(t, dsn), openAndMigrate(t, dsn), openAndMigrate(t, dsn)
	a, b := mustLock(t, dbA), mustLock(t, dbB)
	key := a.(*Lock).key

	ran, err := a.TryRun(context.Background(), func(ctx context.Context) error {
		var pid int32
		if err := admin.QueryRow(ctx, `SELECT l.pid FROM pg_catalog.pg_locks l
			WHERE l.locktype = 'advisory' AND l.granted AND l.objsubid = 1
			AND ((l.classid::bigint << 32) | l.objid::bigint) = pg_catalog.hashtextextended($1, 0)`, key).Scan(&pid); err != nil {
			t.Fatalf("find the lock holder: %v", err)
		}
		var terminated bool
		if err := admin.QueryRow(ctx, `SELECT pg_catalog.pg_terminate_backend($1, 5000)`, pid).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("terminate holder %d = (%v, %v)", pid, terminated, err)
		}
		mustTryRun(t, b)
		return nil
	})
	if !ran || err != nil {
		t.Fatalf("A.TryRun after its backend died = (%v, %v), want (true, nil)", ran, err)
	}
	mustTryRun(t, a)
}

// TestLockSchemasAreIndependent proves outboxes in two schemas lock separately.
// NewLock does not probe, so the schemas need not exist.
func TestLockSchemasAreIndependent(t *testing.T) {
	dsn := requireDSN(t)
	db := openAndMigrate(t, dsn)
	one, err := pgxdb.NewSchema("lock_one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := pgxdb.NewSchema("lock_two")
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewLock(db, WithSchema(one))
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewLock(db, WithSchema(two))
	if err != nil {
		t.Fatal(err)
	}
	ran, err := a.TryRun(context.Background(), func(context.Context) error {
		mustTryRun(t, b)
		return nil
	})
	if !ran || err != nil {
		t.Fatalf("A.TryRun = (%v, %v), want (true, nil)", ran, err)
	}
}

// TestNewLockRejectsInvalidWiring covers a nil database and a nil option.
func TestNewLockRejectsInvalidWiring(t *testing.T) {
	if l, err := NewLock(nil); l != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("NewLock(nil) = (%v, %v), want ErrInvalidInput", l, err)
	}
	dsn := requireDSN(t)
	db := openAndMigrate(t, dsn)
	if l, err := NewLock(db, nil); l != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("NewLock(nil option) = (%v, %v), want ErrInvalidInput", l, err)
	}
}

func mustLock(t *testing.T, db *pgxdb.DB) outbox.Lock {
	t.Helper()
	l, err := NewLock(db, storeOptions(t)...)
	if err != nil {
		t.Fatalf("NewLock: %v", err)
	}
	return l
}

// mustTryRun requires that l takes the lock and runs fn.
func mustTryRun(t *testing.T, l outbox.Lock) {
	t.Helper()
	called := false
	ran, err := l.TryRun(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if !ran || !called || err != nil {
		t.Fatalf("TryRun = (%v, %v) called=%v, want (true, nil) with fn run", ran, err, called)
	}
}
