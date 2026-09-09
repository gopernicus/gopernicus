package firestore

import (
	"context"
	"errors"
	"strings"
	"testing"

	gcfs "cloud.google.com/go/firestore"
)

// TestReaderFromSelectsTheAmbientTransaction proves the seam every store depends
// on: with a transaction in the context both halves come from the transaction,
// without one both come from the client. Hermetic — the emulator address is
// never dialed (gRPC dials lazily and the zero Retry skips boot validation) and
// no method that touches the wire is called.
func TestReaderFromSelectsTheAmbientTransaction(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "127.0.0.1:1")
	db := openForTest(t)

	plain := context.Background()
	if _, ok := ReaderFromIsTx(db.ReaderFrom(plain)); ok {
		t.Error("ReaderFrom returned a transactional Reader without an ambient transaction")
	}
	if _, ok := WriterFromIsTx(db.WriterFrom(plain)); ok {
		t.Error("WriterFrom returned a transactional Writer without an ambient transaction")
	}

	tx := &gcfs.Transaction{}
	ctx := withTx(plain, tx)

	got, ok := TxFromContext(ctx)
	if !ok || got != tx {
		t.Fatalf("TxFromContext = %v, %v; want the planted transaction", got, ok)
	}
	if r, ok := ReaderFromIsTx(db.ReaderFrom(ctx)); !ok || r.tx != tx {
		t.Error("ReaderFrom did not return the ambient transaction's Reader")
	}
	if w, ok := WriterFromIsTx(db.WriterFrom(ctx)); !ok || w.tx != tx {
		t.Error("WriterFrom did not return the ambient transaction's Writer")
	}
}

// TestTxFromContextIgnoresAbsentAndNil pins the two negative cases: a bare
// context, and a context carrying a nil transaction (which must not produce a
// Reader that panics on first use).
func TestTxFromContextIgnoresAbsentAndNil(t *testing.T) {
	if tx, ok := TxFromContext(context.Background()); ok || tx != nil {
		t.Errorf("TxFromContext(background) = %v, %v; want nil, false", tx, ok)
	}
	if tx, ok := TxFromContext(withTx(context.Background(), nil)); ok || tx != nil {
		t.Errorf("TxFromContext(nil transaction) = %v, %v; want nil, false", tx, ok)
	}
}

// TestTransactionalCountRefuses proves the C-D3 refusal is a real error a store
// meets at compile-and-run time, not a silent zero. It needs no server: the
// refusal precedes any I/O.
func TestTransactionalCountRefuses(t *testing.T) {
	count, err := (txReader{tx: &gcfs.Transaction{}}).Count(context.Background(), gcfs.Query{})
	if !errors.Is(err, ErrCountInTransaction) {
		t.Errorf("Count inside a transaction = %v, want ErrCountInTransaction", err)
	}
	if count != 0 {
		t.Errorf("Count inside a transaction = %d, want 0", count)
	}
}

// TestCountFromRejectsAMalformedResult covers the decode failure path; the
// success path is proven against the emulator (TestReaderWriterRoundTrip).
func TestCountFromRejectsAMalformedResult(t *testing.T) {
	if _, err := countFrom(gcfs.AggregationResult{countAlias: "not a protobuf value"}); err == nil {
		t.Fatal("countFrom accepted a malformed aggregation result")
	}
	_, err := countFrom(gcfs.AggregationResult{})
	if err == nil || !strings.Contains(err.Error(), countAlias) {
		t.Fatalf("countFrom on an empty result = %v, want an error naming %q", err, countAlias)
	}
}

// ReaderFromIsTx / WriterFromIsTx are the type assertions the tests above make
// readable; the implementations stay unexported to the outside world.
func ReaderFromIsTx(r Reader) (txReader, bool) {
	tr, ok := r.(txReader)
	return tr, ok
}

func WriterFromIsTx(w Writer) (txWriter, bool) {
	tw, ok := w.(txWriter)
	return tw, ok
}

func openForTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), Config{ProjectID: "gopernicus-test"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
