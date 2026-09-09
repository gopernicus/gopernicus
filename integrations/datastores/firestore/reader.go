package firestore

import (
	"context"
	"fmt"

	gcfs "cloud.google.com/go/firestore"
)

// countAlias is the aggregation alias Count reads its result back under. The
// server echoes the alias, so it only has to be stable within one call.
const countAlias = "count"

// Reader is the read half of the tx-aware document surface — the Firestore
// analogue of the SQL connectors' Querier. A store accepts a Reader instead of
// a *DB so the same store code runs on the client for a standalone operation
// and inside a transaction (or a ReadSnapshot) for a composed one.
//
// Every method maps its error through MapError before returning, so a store
// never interprets a gRPC status. The two exceptions are documented per method:
// Get returns the snapshot ALONGSIDE its NotFound error, and Documents returns
// a raw iterator whose errors arrive at Next.
type Reader interface {
	// Get reads one document. A missing document returns an error wrapping
	// sdk.ErrNotFound AND a non-nil snapshot whose Exists() is false (the
	// vendor's shape, preserved deliberately: a caller that wants "absent" as a
	// value rather than an error can use the snapshot without a second read).
	Get(ctx context.Context, ref *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error)

	// GetAll reads many documents in one call, returning snapshots in the order
	// of refs. A MISSING document is not an error: its snapshot's Exists() is
	// false, so callers must check Exists() per entry. Only a transport,
	// permission, or transaction failure returns an error.
	GetAll(ctx context.Context, refs []*gcfs.DocumentRef) ([]*gcfs.DocumentSnapshot, error)

	// Documents runs a query. The returned iterator is the vendor's: the caller
	// MUST defer Stop, treat iterator.Done as its loop terminator, and pass any
	// other Next error through MapError at the iteration boundary. Errors
	// deferred to Next include the transaction-side read-after-write refusal.
	Documents(ctx context.Context, q gcfs.Query) *gcfs.DocumentIterator

	// Count returns the number of documents matching q using the server-side
	// count aggregation (one billed read per batch, not per document). Inside a
	// transaction it is unavailable and returns ErrCountInTransaction; count by
	// iterating Documents there.
	Count(ctx context.Context, q gcfs.Query) (int64, error)
}

// Writer is the write half of the tx-aware document surface. Inside a
// transaction the methods QUEUE writes that the vendor commits when the
// callback returns nil — they perform no I/O of their own and, per the vendor's
// contract, a subsequent read in the same transaction fails with
// ErrReadAfterWrite. Outside a transaction each method is one committed RPC.
type Writer interface {
	// Create writes a document that must not already exist. A collision returns
	// an error wrapping sdk.ErrAlreadyExists — the uniqueness mechanism ruling
	// R3 builds deterministic document ids and claim documents on.
	Create(ctx context.Context, ref *gcfs.DocumentRef, data any) error

	// Set writes a document, creating or overwriting it. Pass gcfs.MergeAll or
	// gcfs.Merge to write a subset of fields.
	Set(ctx context.Context, ref *gcfs.DocumentRef, data any, opts ...gcfs.SetOption) error

	// Update applies field-level updates to an existing document. A missing
	// document fails NotFound; a failed precondition (an optimistic-lock
	// UpdateTime guard) fails with sdk.ErrConflict.
	Update(ctx context.Context, ref *gcfs.DocumentRef, updates []gcfs.Update, pre ...gcfs.Precondition) error

	// Delete removes a document. Deleting a document that does not exist is NOT
	// an error unless a precondition demands existence.
	Delete(ctx context.Context, ref *gcfs.DocumentRef, pre ...gcfs.Precondition) error
}

// Compile-time assertions for the two implementations of each half.
var (
	_ Reader = clientReader{}
	_ Reader = txReader{}
	_ Writer = clientWriter{}
	_ Writer = txWriter{}
)

// ReaderFrom returns the Reader for ctx: the ambient Firestore transaction's
// reader when the context carries one (planted by Transact), otherwise the
// client's. A store that calls it on every read cannot accidentally bypass a
// transaction it is running inside.
func (d *DB) ReaderFrom(ctx context.Context) Reader {
	if tx, ok := TxFromContext(ctx); ok {
		return txReader{tx: tx}
	}
	return clientReader{client: d.client}
}

// WriterFrom returns the Writer for ctx: the ambient transaction's writer when
// the context carries one, otherwise the client's.
func (d *DB) WriterFrom(ctx context.Context) Writer {
	if tx, ok := TxFromContext(ctx); ok {
		return txWriter{tx: tx}
	}
	return clientWriter{client: d.client}
}

// clientReader reads through the client: each call is its own RPC at its own
// read timestamp. Use ReadSnapshot when several reads must agree.
type clientReader struct {
	client *gcfs.Client
}

func (r clientReader) Get(ctx context.Context, ref *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error) {
	snap, err := ref.Get(ctx)
	return snap, MapError(err)
}

func (r clientReader) GetAll(ctx context.Context, refs []*gcfs.DocumentRef) ([]*gcfs.DocumentSnapshot, error) {
	snaps, err := r.client.GetAll(ctx, refs)
	if err != nil {
		return nil, MapError(err)
	}
	return snaps, nil
}

func (r clientReader) Documents(ctx context.Context, q gcfs.Query) *gcfs.DocumentIterator {
	return q.Documents(ctx)
}

func (r clientReader) Count(ctx context.Context, q gcfs.Query) (int64, error) {
	result, err := q.NewAggregationQuery().WithCount(countAlias).Get(ctx)
	if err != nil {
		return 0, MapError(err)
	}
	return countFrom(result)
}

// txReader reads inside a transaction: every read carries the transaction id,
// so all of them observe ONE server-selected snapshot, and reads are refused
// once the transaction has queued a write.
type txReader struct {
	tx *gcfs.Transaction
}

func (r txReader) Get(_ context.Context, ref *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error) {
	snap, err := r.tx.Get(ref)
	return snap, MapError(err)
}

func (r txReader) GetAll(_ context.Context, refs []*gcfs.DocumentRef) ([]*gcfs.DocumentSnapshot, error) {
	snaps, err := r.tx.GetAll(refs)
	if err != nil {
		return nil, MapError(err)
	}
	return snaps, nil
}

// Documents ignores ctx: a transactional query runs under the context the
// transaction was begun with, which the vendor holds. The signature keeps the
// Reader interface uniform.
func (r txReader) Documents(_ context.Context, q gcfs.Query) *gcfs.DocumentIterator {
	return r.tx.Documents(q)
}

// Count is unavailable inside a transaction; see ErrCountInTransaction.
func (r txReader) Count(context.Context, gcfs.Query) (int64, error) {
	return 0, ErrCountInTransaction
}

// clientWriter writes through the client: each call commits on its own.
type clientWriter struct {
	client *gcfs.Client
}

func (w clientWriter) Create(ctx context.Context, ref *gcfs.DocumentRef, data any) error {
	_, err := ref.Create(ctx, data)
	return MapError(err)
}

func (w clientWriter) Set(ctx context.Context, ref *gcfs.DocumentRef, data any, opts ...gcfs.SetOption) error {
	_, err := ref.Set(ctx, data, opts...)
	return MapError(err)
}

func (w clientWriter) Update(ctx context.Context, ref *gcfs.DocumentRef, updates []gcfs.Update, pre ...gcfs.Precondition) error {
	_, err := ref.Update(ctx, updates, pre...)
	return MapError(err)
}

func (w clientWriter) Delete(ctx context.Context, ref *gcfs.DocumentRef, pre ...gcfs.Precondition) error {
	_, err := ref.Delete(ctx, pre...)
	return MapError(err)
}

// txWriter queues writes on a transaction; the vendor commits them atomically
// when the callback returns nil.
type txWriter struct {
	tx *gcfs.Transaction
}

func (w txWriter) Create(_ context.Context, ref *gcfs.DocumentRef, data any) error {
	return MapError(w.tx.Create(ref, data))
}

func (w txWriter) Set(_ context.Context, ref *gcfs.DocumentRef, data any, opts ...gcfs.SetOption) error {
	return MapError(w.tx.Set(ref, data, opts...))
}

func (w txWriter) Update(_ context.Context, ref *gcfs.DocumentRef, updates []gcfs.Update, pre ...gcfs.Precondition) error {
	return MapError(w.tx.Update(ref, updates, pre...))
}

func (w txWriter) Delete(_ context.Context, ref *gcfs.DocumentRef, pre ...gcfs.Precondition) error {
	return MapError(w.tx.Delete(ref, pre...))
}

// countFrom reads the count aggregation's value out of the vendor's result map.
// DataTo converts the protobuf values to native Go types, which keeps the
// firestorepb import out of this module.
func countFrom(result gcfs.AggregationResult) (int64, error) {
	var values map[string]any
	if err := result.DataTo(&values); err != nil {
		return 0, fmt.Errorf("firestore: decoding count aggregation: %w", err)
	}
	value, ok := values[countAlias]
	if !ok {
		return 0, fmt.Errorf("firestore: count aggregation returned no %q value", countAlias)
	}
	count, ok := value.(int64)
	if !ok {
		return 0, fmt.Errorf("firestore: count aggregation returned %T, want int64", value)
	}
	return count, nil
}
