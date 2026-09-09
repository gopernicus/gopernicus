package firestore

import (
	"context"

	gcfs "cloud.google.com/go/firestore"
)

// txContextKey is the private key an ambient Firestore transaction travels
// under. Private and typed: nothing outside this package can plant a
// transaction in a context, so ReaderFrom/WriterFrom can trust what they find.
type txContextKey struct{}

// withTx returns ctx carrying tx as the ambient transaction. (*DB).Transact is
// its only producer; ReaderFrom/WriterFrom and TxFromContext are its consumers.
func withTx(ctx context.Context, tx *gcfs.Transaction) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFromContext returns the ambient Firestore transaction, if the context
// carries one. It is the seam a store uses to REFUSE to run inside a host's
// transaction (ruling R1): a Firestore transaction sees none of its own pending
// writes, so a store that quietly joined one would report stale reads, and a
// store that quietly used the client beside one would split the host's atomic
// unit. Both are failures worth an error, not a surprise.
func TxFromContext(ctx context.Context) (*gcfs.Transaction, bool) {
	tx, ok := ctx.Value(txContextKey{}).(*gcfs.Transaction)
	return tx, ok && tx != nil
}
