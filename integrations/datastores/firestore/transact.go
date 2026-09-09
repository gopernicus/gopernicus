package firestore

import (
	"context"
	"errors"

	gcfs "cloud.google.com/go/firestore"

	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// ErrNestedTransact is returned when Transact is called from inside a context
// that already carries a connector transaction — a Transact within a Transact,
// or a Transact within a ReadSnapshot. Nesting was left EXPLICITLY UNPINNED by
// the sdk seam until its first consumer arrived; that consumer's invariant is
// exactly one transaction per workflow, so a nested begin would silently split
// the atomicity the caller believes it has. Fail loud: an error can graduate
// into defined nesting behavior later without breaking anyone; silent behavior
// cannot become an error.
//
// It is raised BEFORE the vendor is called. The vendor refuses nesting too
// (errNestedTransaction), but only for its own in-progress marker, and its
// message names nothing a caller can act on.
var ErrNestedTransact = errors.New("firestore: nested Transact — the workflow already holds a transaction; pass the ambient ctx to the repositories instead")

// errPanicInTransact is the error the callback wrapper returns to the vendor
// after recovering a panic, so the vendor rolls the transaction back before
// Transact re-panics. It never leaves this package: the panic value does.
var errPanicInTransact = errors.New("firestore: the Transact callback panicked")

// Compile-time proof the connector satisfies the sdk transaction seam.
var _ crud.Transactor = (*DB)(nil)

// Transact implements sdk/foundation/crud.Transactor over a Firestore
// read-write transaction: it calls fn with a context carrying the transaction,
// COMMITS when fn returns nil, ROLLS BACK and returns fn's error unwrapped when
// it does not, and re-panics after rolling back when fn panics. Repositories
// inside fn reach the transaction through ReaderFrom/WriterFrom (or refuse it
// through TxFromContext).
//
// # THE CALLBACK MAY RUN MORE THAN ONCE
//
// This is the one contract difference from the SQL connectors, and it is not a
// detail. Firestore commits optimistically: when the commit loses a race with
// another writer the server answers Aborted and the vendor RE-RUNS fn from the
// top, up to Config.MaxAttempts times (DefaultMaxAttempts, 5, by default).
// Therefore:
//
//   - fn must be idempotent in every side effect OUTSIDE Firestore. Sending a
//     mail, charging a card, or appending to a log from inside fn will happen
//     once per attempt. Do it after Transact returns nil.
//   - Any state fn writes into variables of its enclosing scope must be RESET
//     at the top of fn, not initialized once outside it. A result recorded on a
//     losing attempt is not the result of the transaction that committed.
//   - Writes queued by a losing attempt are discarded by the vendor; only the
//     winning attempt's writes commit. Nothing fn read on a losing attempt is
//     still known to be true.
//
// # Reads before writes
//
// Firestore refuses any read issued after the first write in the same
// transaction, and a transaction never observes its OWN pending writes. Read
// everything first, decide, then write. A read after a write fails with
// ErrReadAfterWrite — and the vendor re-checks it even if fn swallows the
// error, so it cannot be hidden. Count is unavailable inside a transaction
// (ErrCountInTransaction); count by iterating the transactional Reader.
//
// # A committed outcome is not the callback's error
//
// The callback's return value decides COMMIT or ROLLBACK, and nothing else. It
// is not the operation's domain answer, and conflating the two silently
// discards work. Two shapes, both legitimate, chosen per operation:
//
//	// Commit, THEN report. Consuming an expired single-use token still has to
//	// delete it: the callback returns nil so the delete commits, and the
//	// caller reports the domain outcome afterwards.
//	var expired bool
//	if err := db.Transact(ctx, func(ctx context.Context) error {
//	    snap, err := db.ReaderFrom(ctx).Get(ctx, ref)
//	    if err != nil { return err }
//	    expired = isExpired(snap)                       // reset every attempt
//	    return db.WriterFrom(ctx).Delete(ctx, ref)
//	}); err != nil {
//	    return err                                      // nothing committed
//	}
//	if expired { return sdk.ErrExpired }                // committed, then reported
//
//	// Reject and roll back. A stable rejection that must leave the database
//	// untouched returns the domain error from the callback; Transact hands it
//	// back byte-identical and every queued write is discarded.
//	return db.Transact(ctx, func(ctx context.Context) error {
//	    ...
//	    return ErrPasswordlessRejected
//	})
//
// # Errors
//
//   - fn's error is returned UNWRAPPED — the identical value, never remapped.
//     A caller can compare it with == or errors.Is against its own sentinel.
//   - A vendor error (begin, commit, or the read-after-write re-check) is
//     returned through MapError. Retries exhausted by a losing COMMIT surface
//     as the server's Aborted, hence sdk.ErrConflict: the caller is the
//     contention loser and may retry the whole workflow.
//   - Nesting returns ErrNestedTransact, before the vendor is called.
//
// One sharp edge, worth knowing before it bites (C0 finding N4): the vendor
// decides whether to retry by asking whether the error IS or WRAPS a gRPC
// Aborted status — including an error fn returned. A callback that hands back a
// raw vendor Aborted status therefore causes a retry, which is usually what a
// contention-aware store wants but is surprising if the error came from
// somewhere unrelated. Passing vendor errors through MapError before returning
// them settles it: MapError keeps the server's message but not its status, so a
// mapped error ends the transaction instead of re-running it.
func (d *DB) Transact(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ambientTxFrom(ctx); ok {
		return ErrNestedTransact
	}

	run := &attemptState{}
	err := d.client.RunTransaction(ctx, func(txCtx context.Context, tx *gcfs.Transaction) (rerr error) {
		defer run.beginAttempt(&rerr)()
		return fn(withTx(txCtx, tx, false))
	}, gcfs.MaxAttempts(d.attempts()))

	return run.result(err)
}

// ReadSnapshot runs fn against ONE server-selected snapshot: every read fn
// issues — through the Reader it is handed, or through ReaderFrom on the
// context it is handed — observes the same instant, no matter how many queries,
// chunks, hops, or probes the operation takes. It is the seam an operation uses
// when several reads must agree with each other; a plain Reader gives each read
// its own fresh timestamp, which is how a graph walk reads a half-applied
// change.
//
// It is a READ-ONLY transaction, and that has three consequences worth stating:
//
//   - No write is possible. WriterFrom on fn's context returns a Writer whose
//     every method fails with ErrWriteInReadOnlyTransaction, and the vendor
//     refuses the write underneath that as well. This holds on the reuse path
//     below too: a snapshot standing inside a read-write Transact still hands
//     fn a write-refusing context, while the enclosing Transact's own context
//     keeps writing.
//   - It is never retried, so unlike Transact's, fn runs exactly ONCE.
//   - Count is unavailable (ErrCountInTransaction, as in any transaction);
//     count under a snapshot by iterating the query.
//
// Called from inside an existing connector transaction — a mutation helper that
// wants a consistent read while its caller holds a Transact — it REUSES that
// transaction's snapshot and Reader instead of nesting: the vendor refuses a
// nested transaction outright, and a Firestore read-write transaction already
// reads one snapshot. That is the one nesting case the connector resolves
// rather than refusing, because the caller's intent ("read consistently") is
// already satisfied by the transaction it is standing in. Transact inside a
// ReadSnapshot is still ErrNestedTransact — a snapshot cannot grow a write.
//
// fn's error is returned unwrapped; vendor errors go through MapError; a panic
// re-panics after the transaction is released.
func (d *DB) ReadSnapshot(ctx context.Context, fn func(ctx context.Context, r Reader) error) error {
	if ambient, ok := ambientTxFrom(ctx); ok {
		// The context handed to fn is re-stashed READ-ONLY, even when the
		// enclosing transaction is a read-write Transact. Inside a
		// ReadSnapshot, WriterFrom must refuse — that is the guarantee this
		// seam advertises, and it cannot depend on whether the snapshot began
		// its own transaction or reused one. The enclosing Transact's own
		// context is untouched, so it keeps writing after fn returns.
		return fn(withTx(ctx, ambient.tx, true), txReader{tx: ambient.tx})
	}

	run := &attemptState{}
	err := d.client.RunTransaction(ctx, func(txCtx context.Context, tx *gcfs.Transaction) (rerr error) {
		defer run.beginAttempt(&rerr)()
		return fn(withTx(txCtx, tx, true), txReader{tx: tx})
	}, gcfs.ReadOnly)

	return run.result(err)
}

// attempts resolves the configured retry cap, defending against a DB built
// outside Open (the zero value would make the vendor's loop run zero times and
// return nil having committed nothing).
func (d *DB) attempts() int {
	if d.maxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return d.maxAttempts
}

// attemptState carries what the callback wrapper learns across the vendor's
// retry loop so Transact/ReadSnapshot can tell three outcomes apart after
// RunTransaction returns: the callback failed (return its error untouched), the
// callback panicked (re-panic), or the vendor failed (map it).
//
// Every field is reset at the top of each attempt, which is the same discipline
// the callback itself owes its own state.
type attemptState struct {
	failed     bool
	err        error
	panicked   bool
	panicValue any
}

// beginAttempt resets the attempt-local state and returns the deferred function
// that records how the attempt ended. It is written as a defer so a panic is
// converted into an error for the vendor — which then rolls the transaction
// back instead of leaving it open until the server's lock timeout — while the
// panic value survives to be re-raised by result.
func (s *attemptState) beginAttempt(rerr *error) func() {
	s.failed, s.err, s.panicked, s.panicValue = false, nil, false, nil
	return func() {
		if r := recover(); r != nil {
			s.panicked, s.panicValue = true, r
			*rerr = errPanicInTransact
			return
		}
		if *rerr != nil {
			s.failed, s.err = true, *rerr
		}
	}
}

// result turns the vendor's return value into the connector's, given what the
// last attempt did.
func (s *attemptState) result(err error) error {
	if s.panicked {
		// The vendor has rolled back by now (it saw errPanicInTransact). The
		// original stack is gone — recovering is the price of not leaving a
		// transaction open — but the panic VALUE is re-raised unchanged, so a
		// caller's recover sees exactly what its callback panicked with.
		panic(s.panicValue)
	}
	if err != nil && s.failed {
		// The last attempt's callback failed, so the vendor's error IS that
		// error: it returns a callback error untouched. One corner is worth
		// naming — if the context is cancelled during the retry backoff the
		// vendor returns the cancellation instead, and this returns the
		// callback error anyway. That is deliberate: the callback error is why
		// the transaction did not commit, and ctx.Err() still tells the caller
		// the rest.
		return s.err
	}
	return MapError(err)
}
