package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopernicus/gopernicus/sdk"
)

// The vendor client reports a handful of client-side misuses as PLAIN errors
// (errors.New, no gRPC status), so string equality is the only handle on them.
// They are the exact values from cloud.google.com/go/firestore v1.25.0
// transaction.go:116-119; a vendor bump that reworded them turns the mapping
// silently generic, which is why MapError has a test row per constant.
const (
	vendorReadAfterWrite    = "firestore: read after write in transaction"
	vendorWriteReadOnly     = "firestore: write in read-only transaction"
	vendorNestedTransaction = "firestore: nested transaction"
	vendorInvalidReadTime   = "firestore: ReadTime cannot be set via WithReadOptions on a Transaction"
)

// indexConsolePrefix is the prefix EVERY index-creation link the server embeds
// in a missing-index FAILED_PRECONDITION message shares. The HOST is not fixed:
// a database created through Firebase is linked to console.firebase.google.com
// and one created through Google Cloud to console.cloud.google.com, so
// recognizing either host by name would make the mapping depend on how the
// host's database happened to be provisioned. Together with "index" this prefix
// is the second recognizer, for the variants that do not use the "requires an
// index" phrasing (single-field and still-building messages word it
// differently).
const indexConsolePrefix = "https://console."

// urlScheme is what indexURL scans for. A missing-index message carries exactly
// one link, so taking the FIRST https:// token extracts it without knowing the
// host or the wording around it.
const urlScheme = "https://"

// requiresIndexPhrase is the server's canonical missing-composite-index wording:
// "The query requires an index. You can create it here: <url>".
const requiresIndexPhrase = "requires an index"

var (
	// ErrMissingIndex reports a query the target database has no usable index
	// for — either none is defined or it is still building. It wraps
	// sdk.ErrUnavailable because the query is not wrong, the database is not
	// ready to answer it; deploying the index manifest fixes it. Every
	// *MissingIndexError matches it, so callers can errors.Is without naming
	// the type. ProbeIndexes (C5) reports missing indexes with the same sentinel.
	//
	// It is a WIRING error, not a retryable condition, and the sentinel it
	// wraps makes that easy to get wrong: a caller with a generic
	// sdk.ErrUnavailable retry must test errors.Is(err, ErrMissingIndex) FIRST
	// and give up, because no amount of waiting deploys an index. See
	// ProbeIndexes for the branch, spelled out.
	ErrMissingIndex = fmt.Errorf("firestore: query requires an index that does not exist or is not READY: %w", sdk.ErrUnavailable)

	// ErrReadAfterWrite reports the vendor's reads-before-writes rule: inside a
	// transaction, no read may follow a write. It is a programming error in the
	// callback (restructure it to read everything first), not a runtime
	// condition, hence sdk.ErrInvalidInput.
	ErrReadAfterWrite = fmt.Errorf("firestore: read after write in a transaction — all reads must precede all writes: %w", sdk.ErrInvalidInput)

	// ErrWriteInReadOnlyTransaction reports a write attempted inside a read-only
	// transaction — the mechanism that keeps ReadSnapshot write-free by
	// construction. Also a programming error.
	ErrWriteInReadOnlyTransaction = fmt.Errorf("firestore: write inside a read-only transaction: %w", sdk.ErrInvalidInput)

	// ErrCountInTransaction reports Count called on a transactional Reader.
	// Firestore's Go client offers no transaction-guarded aggregation: the
	// AggregationQuery.Transaction escape hatch skips the reads-before-writes
	// check the rest of the transaction surface enforces, so a count issued
	// after a write inside a transaction would silently break the transaction's
	// own contract. A store that needs a count under a snapshot iterates the
	// query through the transactional Reader instead (C-D3).
	ErrCountInTransaction = fmt.Errorf("firestore: Count is unavailable inside a transaction — iterate the query through the transactional Reader: %w", sdk.ErrInvalidInput)
)

// MissingIndexError carries the server's missing-index diagnosis verbatim: the
// full FAILED_PRECONDITION message and, when the server supplied one, the
// console URL that creates the index. It is deliberately a value a host can log
// or surface to an operator — retyping the URL by hand is how index deployments
// go wrong.
//
// ProbeIndexes (C5) produces the same type for a manifest index the database
// does not have, so a host handles ONE type whether the diagnosis came from the
// boot probe or from a live query; there the two fields are the connector's own
// description of the gap and the database's console index page.
type MissingIndexError struct {
	// Message is the server's FAILED_PRECONDITION message, unmodified — or, for
	// a ProbeIndexes gap, the connector's description of the missing index.
	Message string
	// URL is the index-creation link the server embedded, unmodified — or, for
	// a ProbeIndexes gap, the console page listing the database's indexes.
	// Empty when neither is available (a still-building index sometimes omits
	// the link).
	URL string

	// cause is the server status this was mapped from, kept in the chain so
	// status.Code recovers FailedPrecondition — the same status-preserving rule
	// every other MapError row follows. A ProbeIndexes gap has no status and
	// leaves it nil.
	cause error
}

// Error names the condition and repeats the server's own message.
func (e *MissingIndexError) Error() string {
	if e.URL != "" {
		return fmt.Sprintf("firestore: query requires an index that does not exist or is not READY: %s (create it: %s)", e.Message, e.URL)
	}
	return fmt.Sprintf("firestore: query requires an index that does not exist or is not READY: %s", e.Message)
}

// Unwrap chains to ErrMissingIndex, which itself wraps sdk.ErrUnavailable — so
// both errors.Is checks hold — and, when the value came from a server response,
// to that response, so status.Code still reports FailedPrecondition.
func (e *MissingIndexError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrMissingIndex}
	}
	return []error{ErrMissingIndex, e.cause}
}

// MapError translates a Firestore error into the sdk sentinel vocabulary the
// pockets' ports are written against (C-D5). It is the ONLY place vendor error
// shapes are interpreted: every Reader/Writer method returns through it, and a
// store maps its own iterator errors with it at the iteration boundary.
//
// The mapping:
//
//	NotFound                                     → sdk.ErrNotFound
//	AlreadyExists                                → sdk.ErrAlreadyExists
//	Aborted (transaction retries exhausted)      → sdk.ErrConflict
//	FailedPrecondition, missing index            → *MissingIndexError (ErrMissingIndex → sdk.ErrUnavailable)
//	FailedPrecondition, otherwise                → sdk.ErrConflict
//	InvalidArgument                              → sdk.ErrInvalidInput
//	DeadlineExceeded, Unavailable, ResourceExhausted → sdk.ErrUnavailable
//	context.DeadlineExceeded (no status attached)  → sdk.ErrUnavailable
//	PermissionDenied                             → sdk.ErrForbidden
//	Unauthenticated                              → sdk.ErrUnauthorized
//	read after write / write in a read-only tx   → the connector sentinels above
//	nested transaction / invalid read time       → sdk.ErrInvalidInput
//
// Three inputs pass through byte-identical, on purpose:
//
//   - nil, so callers can map unconditionally;
//   - iterator.Done, which the iterating caller CONSUMES as its loop terminator
//     and must never see rewritten into a port error;
//   - anything that already carries an sdk sentinel — a store's own domain error
//     handed back through a helper, or a value MapError already produced. That
//     makes MapError idempotent.
//
// Anything else is returned wrapped with a "firestore:" prefix and NO sentinel,
// so an unrecognized failure surfaces as a 500 rather than being flattened into
// a plausible-looking domain outcome.
//
// # The gRPC status SURVIVES the mapping
//
// Every status-code row wraps BOTH the original error and the sdk sentinel (Go
// 1.20 multi-%w), so the mapped value answers `errors.Is(err, sdk.ErrConflict)`
// AND `status.Code(err)` still reports Aborted. That is not a nicety: the
// vendor's transaction retry gate asks `status.FromError` about the error a
// callback returned (transaction.go:245), so an Aborted flattened into a bare
// sentinel would tell the vendor "this transaction is settled" when the truth
// is contention. A store that maps a transactional read's error — the only
// honest thing to do with it — must not lose the retry that way. Recover the
// code with `status.Code(err)` or `status.FromError(err)`; both walk the chain
// through errors.As.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, iterator.Done) {
		return err
	}
	if sdk.IsExpected(err) {
		return err
	}
	// A timed-out RPC does not always arrive as a gRPC status: the vendor
	// client retries a refused connection internally and, when the caller's
	// deadline expires first, hands back the bare context error. That is the
	// same condition as codes.DeadlineExceeded above and gets the same
	// sentinel, so a health check against an unreachable database reports
	// "unavailable" instead of an unrecognized 500. context.Canceled is
	// deliberately NOT mapped: the caller withdrew the request, which is not a
	// statement about the database.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("firestore: %s: %w", err, sdk.ErrUnavailable)
	}

	if mapped := mapVendorError(err); mapped != nil {
		return mapped
	}

	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("firestore: %w", err)
	}
	msg := st.Message()
	switch st.Code() {
	case codes.NotFound:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrNotFound)
	case codes.AlreadyExists:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrAlreadyExists)
	case codes.Aborted:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrConflict)
	case codes.FailedPrecondition:
		if isMissingIndex(msg) {
			return &MissingIndexError{Message: msg, URL: indexURL(msg), cause: err}
		}
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrConflict)
	case codes.InvalidArgument:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrInvalidInput)
	case codes.DeadlineExceeded, codes.Unavailable, codes.ResourceExhausted:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrUnavailable)
	case codes.PermissionDenied:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrForbidden)
	case codes.Unauthenticated:
		return fmt.Errorf("firestore: %w: %w", err, sdk.ErrUnauthorized)
	}
	return fmt.Errorf("firestore: %w", err)
}

// mapVendorError recognizes the vendor's plain (non-status) client-side errors,
// returning nil when err is none of them.
func mapVendorError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, vendorReadAfterWrite):
		return ErrReadAfterWrite
	case strings.Contains(msg, vendorWriteReadOnly):
		return ErrWriteInReadOnlyTransaction
	case strings.Contains(msg, vendorNestedTransaction):
		return fmt.Errorf("firestore: %s: %w", msg, sdk.ErrInvalidInput)
	case strings.Contains(msg, vendorInvalidReadTime):
		return fmt.Errorf("firestore: %s: %w", msg, sdk.ErrInvalidInput)
	}
	return nil
}

// isMissingIndex reports whether a FAILED_PRECONDITION message is the server's
// "no usable index" diagnosis. Two recognizers, both host-agnostic: the
// canonical composite-index phrasing, and "index" plus the console link the
// server attaches to every index-related variant (single-field, still-building)
// whose wording differs.
func isMissingIndex(msg string) bool {
	if strings.Contains(msg, requiresIndexPhrase) {
		return true
	}
	return strings.Contains(msg, "index") && strings.Contains(msg, indexConsolePrefix)
}

// indexURL extracts the console link from a missing-index message verbatim,
// returning "" when there is none. It takes the FIRST https:// token — the
// message carries one link and its host varies with how the database was
// provisioned, so scanning for a fixed host would drop the URL from half the
// messages. The URL runs to the first whitespace; a trailing sentence period is
// trimmed because the server sometimes ends the sentence right after the link.
func indexURL(msg string) string {
	start := strings.Index(msg, urlScheme)
	if start < 0 {
		return ""
	}
	url := msg[start:]
	if end := strings.IndexAny(url, " \t\n\r"); end >= 0 {
		url = url[:end]
	}
	return strings.TrimRight(url, ".")
}
