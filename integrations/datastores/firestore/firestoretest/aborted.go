package firestoretest

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AbortedError returns a real gRPC Aborted status — the exact shape Firestore
// answers a transaction that lost its commit race with.
//
// It exists so a STORE can force the contention path without taking a direct
// dependency on google.golang.org/grpc. A store module's go.mod requires the
// connector and the vendor client; grpc is an indirect dependency of that
// client and has no business being a direct require of a pocket adapter just so
// one test can construct a status value.
//
// The value matters because the retry gate reads it: the vendor re-runs a
// transaction callback whose error IS or WRAPS an Aborted status
// (cloud.google.com/go/firestore transaction.go, isAborted → status.FromError →
// errors.As), and the connector's MapError PRESERVES the status in the chain.
// So an error built here still drives a retry after it has been mapped to
// sdk.ErrConflict, which is what makes contention on a transactional READ
// recoverable.
//
// Hermetic: no emulator, no network, no configuration.
func AbortedError(msg string) error {
	return status.Error(codes.Aborted, msg)
}
