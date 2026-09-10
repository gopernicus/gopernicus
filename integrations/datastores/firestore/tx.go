package firestore

import (
	"context"

	gcfs "cloud.google.com/go/firestore"
)

// txContextKey is the private key an ambient Firestore transaction travels
// under. Private and typed: nothing outside this package can plant a
// transaction in a context, so ReaderFrom/WriterFrom can trust what they find.
type txContextKey struct{}

// ambientTx is what the context actually carries: the vendor transaction plus
// the one fact the vendor's type does not expose — whether it was begun
// read-only. ReadSnapshot begins read-only transactions, and a Writer taken
// from such a context must refuse every write BEFORE reaching the wire, so the
// guarantee ReadSnapshot advertises does not depend on recognizing a vendor
// error string.
type ambientTx struct {
	tx       *gcfs.Transaction
	readOnly bool
}

// withTx returns ctx carrying tx as the ambient transaction. (*DB).Transact
// (readOnly false) and (*DB).ReadSnapshot (readOnly true) are its only
// producers; ReaderFrom/WriterFrom and TxFromContext are its consumers.
func withTx(ctx context.Context, tx *gcfs.Transaction, readOnly bool) context.Context {
	return context.WithValue(ctx, txContextKey{}, ambientTx{tx: tx, readOnly: readOnly})
}

// ambientTxFrom returns the ambient transaction and its read-only flag. A nil
// transaction reports false, so a malformed stash cannot produce a Reader that
// panics on first use.
func ambientTxFrom(ctx context.Context) (ambientTx, bool) {
	ambient, ok := ctx.Value(txContextKey{}).(ambientTx)
	return ambient, ok && ambient.tx != nil
}

// TxFromContext returns the ambient Firestore transaction, if the context
// carries one. It is the seam a store uses to REFUSE to run inside a host's
// transaction (ruling R1): a Firestore transaction sees none of its own pending
// writes, so a store that quietly joined one would report stale reads, and a
// store that quietly used the client beside one would split the host's atomic
// unit. Both are failures worth an error, not a surprise.
//
// It reports a read-only snapshot transaction (ReadSnapshot's) the same way it
// reports a read-write one: a store must refuse both, and the read-only case is
// the one where a silent join would look like it worked.
func TxFromContext(ctx context.Context) (*gcfs.Transaction, bool) {
	ambient, ok := ambientTxFrom(ctx)
	if !ok {
		return nil, false
	}
	return ambient.tx, true
}
