package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// TestAppendTx proves the dialect-typed transactional appender (design §5): a
// record written via AppendTx inside an InTx block is visible after commit and
// invisible when the surrounding transaction rolls back. This is the outbox
// atomicity a future emitting store rides — domain rows and outbox rows share one
// commit. The shared storetest suite cannot cover it: AppendTx takes a *pgxdb.Tx
// the dialect-blind port never sees. Env-gated on POSTGRES_TEST_DSN (loud skip),
// matching the sibling pgx stores' plain gating.
func TestAppendTx(t *testing.T) {
	dsn := requireDSN(t)
	db := openAndMigrate(t, dsn)
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	// Committed transaction: the appended record is visible afterward.
	committed := rec("appendtx-committed")
	if err := db.InTx(ctx, func(tx *pgxdb.Tx) error {
		return store.AppendTx(ctx, tx, committed)
	}); err != nil {
		t.Fatalf("InTx(commit): %v", err)
	}
	if !hasEntry(t, store, committed.EventID) {
		t.Fatalf("record %q not visible after commit", committed.EventID)
	}

	// Rolled-back transaction: the appended record leaves no row. A sentinel
	// error returned from the InTx func triggers the rollback.
	rollbackErr := errors.New("force rollback")
	rolled := rec("appendtx-rolledback")
	err = db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if err := store.AppendTx(ctx, tx, rolled); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("InTx(rollback) err = %v, want the sentinel", err)
	}
	if hasEntry(t, store, rolled.EventID) {
		t.Fatalf("record %q visible after rollback — AppendTx did not honor the caller's tx", rolled.EventID)
	}
}

// rec builds a durable envelope keyed by eventID for the appender test.
func rec(eventID string) sdkevents.Record {
	return sdkevents.Record{
		EventID:    eventID,
		Type:       "test.appendtx",
		OccurredAt: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
		Payload:    []byte(`{"eid":"` + eventID + `"}`),
	}
}

// hasEntry reports whether an unpublished entry with eventID is present.
func hasEntry(t *testing.T, store *Store, eventID string) bool {
	t.Helper()
	entries, err := store.ListUnpublished(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListUnpublished: %v", err)
	}
	for _, e := range entries {
		if e.EventID == eventID {
			return true
		}
	}
	return false
}
