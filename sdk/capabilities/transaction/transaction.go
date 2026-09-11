// Package transaction defines the callback contract for datastore transactions.
// Connectors own their typed handles and repository participation helpers.
package transaction

import "context"

// Transactor coordinates participating repository operations on one datastore.
// Pass fn's context to repositories backed by the same datastore instance;
// only operations using its transaction handle participate. Other connections,
// other stores and external effects are outside the transaction.
//
// A nil result means the transaction committed. An error or panic aborts an
// uncommitted attempt; implementations preserve panic values and domain error
// matching through errors.Is/errors.As. Cleanup can add an error cause.
// Bundled connectors reject nested Transact calls with a connector error.
//
// An implementation may retry fn. Reset captured results at the start of each
// attempt, and perform external effects only after Transact returns nil. Use a
// durable outbox when effects must survive a crash after commit.
//
// Isolation, read restrictions and cancellation depend on the connector.
// Firestore requires reads before writes and cannot read its pending writes.
// Cancellation racing a commit cannot guarantee undoing a committed write.
// There is no default implementation or shared untyped context handle.
type Transactor interface {
	Transact(ctx context.Context, fn func(ctx context.Context) error) error
}
