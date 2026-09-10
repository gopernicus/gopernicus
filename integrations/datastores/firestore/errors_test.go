package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

// requiresIndexMessage is the server's canonical missing-composite-index
// message, with the console URL it embeds.
const requiresIndexMessage = `The query requires an index. You can create it here: ` + indexCreateURL

// buildingIndexMessage is the variant the server returns for a single-field or
// still-building index: it never says "requires an index", it says "index" and
// attaches the same console link.
const buildingIndexMessage = `The query requires a COLLECTION_GROUP_ASC index for collection users and field created_at. That index is not ready yet. See its current status: ` + indexCreateURL

const indexCreateURL = "https://console.firebase.google.com/project/gopernicus-test/database/firestore/indexes?create_composite=ClFwcm9qZWN0cy9nb3Blcm5pY3Vz"

// TestMapError is the C-D5 table: one row per gRPC code and per vendor
// client-side error, plus the three pass-through inputs. Hermetic — every input
// is a locally constructed status or error value, no network.
func TestMapError(t *testing.T) {
	cases := []struct {
		name     string
		in       error
		want     error // errors.Is target; nil means "no sdk sentinel"
		wantSame bool  // the returned error must be the input, byte-identical
		// wantCode is the gRPC code status.Code must still recover from the
		// MAPPED value. Every status-carrying row states one: the sentinel is
		// added to the chain, the status is never removed from it, because the
		// vendor's transaction retry gate reads the status of the error a
		// callback returns (A7 fold, connector C1).
		wantCode codes.Code
	}{
		{name: "nil maps to nil", in: nil},
		{
			name:     "iterator.Done passes through for the caller's loop",
			in:       iterator.Done,
			wantSame: true,
		},
		{
			name:     "an error already carrying a sentinel passes through",
			in:       fmt.Errorf("user %q: %w", "u1", sdk.ErrNotFound),
			want:     sdk.ErrNotFound,
			wantSame: true,
		},
		{
			name:     "a bare sdk sentinel passes through",
			in:       sdk.ErrConflict,
			want:     sdk.ErrConflict,
			wantSame: true,
		},
		{name: "NotFound", in: status.Error(codes.NotFound, `"users/u1" not found`), want: sdk.ErrNotFound, wantCode: codes.NotFound},
		{name: "AlreadyExists", in: status.Error(codes.AlreadyExists, "entity already exists: users/u1"), want: sdk.ErrAlreadyExists, wantCode: codes.AlreadyExists},
		{name: "Aborted (retries exhausted)", in: status.Error(codes.Aborted, "Too much contention"), want: sdk.ErrConflict, wantCode: codes.Aborted},
		{name: "FailedPrecondition without an index message", in: status.Error(codes.FailedPrecondition, "the stored document version does not match"), want: sdk.ErrConflict, wantCode: codes.FailedPrecondition},
		{name: "InvalidArgument", in: status.Error(codes.InvalidArgument, "Collection id is reserved"), want: sdk.ErrInvalidInput, wantCode: codes.InvalidArgument},
		{name: "DeadlineExceeded", in: status.Error(codes.DeadlineExceeded, "context deadline exceeded"), want: sdk.ErrUnavailable, wantCode: codes.DeadlineExceeded},
		{name: "Unavailable", in: status.Error(codes.Unavailable, "connection reset"), want: sdk.ErrUnavailable, wantCode: codes.Unavailable},
		{name: "ResourceExhausted", in: status.Error(codes.ResourceExhausted, "quota exceeded"), want: sdk.ErrUnavailable, wantCode: codes.ResourceExhausted},
		{name: "PermissionDenied", in: status.Error(codes.PermissionDenied, "Missing or insufficient permissions"), want: sdk.ErrForbidden, wantCode: codes.PermissionDenied},
		{name: "Unauthenticated", in: status.Error(codes.Unauthenticated, "invalid credentials"), want: sdk.ErrUnauthorized, wantCode: codes.Unauthenticated},
		{name: "Unknown status gets no sentinel", in: status.Error(codes.Unknown, "surprise")},
		{name: "Internal gets no sentinel", in: status.Error(codes.Internal, "internal error")},
		{name: "a plain non-status error gets no sentinel", in: errors.New("dial tcp: connection refused")},
		{
			name: "vendor read after write in a transaction",
			in:   errors.New("firestore: read after write in transaction"),
			want: firestore.ErrReadAfterWrite,
		},
		{
			name: "vendor read after write, wrapped by a caller",
			in:   fmt.Errorf("loading claim: %w", errors.New("firestore: read after write in transaction")),
			want: firestore.ErrReadAfterWrite,
		},
		{
			name: "vendor write in a read-only transaction",
			in:   errors.New("firestore: write in read-only transaction"),
			want: firestore.ErrWriteInReadOnlyTransaction,
		},
		{
			name: "vendor nested transaction",
			in:   errors.New("firestore: nested transaction"),
			want: sdk.ErrInvalidInput,
		},
		{
			name: "vendor invalid read time",
			in:   errors.New("firestore: ReadTime cannot be set via WithReadOptions on a Transaction. Use TransactionReadTime option when starting the transaction"),
			want: sdk.ErrInvalidInput,
		},
		{
			name: "a bare context deadline (the vendor's own retry outlived it)",
			in:   fmt.Errorf("Get: %w", context.DeadlineExceeded),
			want: sdk.ErrUnavailable,
		},
		{
			name:     "FailedPrecondition, missing composite index",
			in:       status.Error(codes.FailedPrecondition, requiresIndexMessage),
			want:     firestore.ErrMissingIndex,
			wantCode: codes.FailedPrecondition,
		},
		{
			name:     "FailedPrecondition, index still building",
			in:       status.Error(codes.FailedPrecondition, buildingIndexMessage),
			want:     firestore.ErrMissingIndex,
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := firestore.MapError(tc.in)

			if tc.in == nil {
				if got != nil {
					t.Fatalf("MapError(nil) = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("MapError(%v) = nil, want an error", tc.in)
			}
			if tc.wantSame && !errors.Is(got, tc.in) {
				t.Errorf("MapError must return %v unchanged, got %v", tc.in, got)
			}
			if tc.want != nil && !errors.Is(got, tc.want) {
				t.Errorf("MapError(%v) = %v, want it to wrap %v", tc.in, got, tc.want)
			}
			if tc.want == nil && !tc.wantSame && sdk.IsExpected(got) {
				t.Errorf("MapError(%v) = %v, want NO sdk sentinel (an unrecognized failure must reach the caller as a 500)", tc.in, got)
			}
			if !tc.wantSame && !strings.HasPrefix(got.Error(), "firestore:") {
				t.Errorf("MapError(%v) = %q, want a firestore: prefix naming the source", tc.in, got)
			}
			// The status SURVIVES the mapping. Without it, an Aborted a store
			// mapped inside a transaction callback would tell the vendor's
			// retry gate that the transaction is settled — which is the bug
			// this assertion exists to keep fixed.
			if tc.wantCode != codes.OK {
				if code := status.Code(got); code != tc.wantCode {
					t.Errorf("status.Code(MapError(%v)) = %s, want %s — the sentinel is ADDED to the chain, the status is never removed from it", tc.in, code, tc.wantCode)
				}
			}
			// Idempotence: the mapped value maps to itself, so a store may map
			// an error a helper already mapped without flattening it.
			if again := firestore.MapError(got); !errors.Is(again, got) {
				t.Errorf("MapError is not idempotent: MapError(%v) = %v", got, again)
			}
			if tc.wantCode != codes.OK {
				if code := status.Code(firestore.MapError(got)); code != tc.wantCode {
					t.Errorf("re-mapping lost the status: status.Code = %s, want %s", code, tc.wantCode)
				}
			}
		})
	}
}

// TestMapErrorMissingIndex pins the operator-facing half of the missing-index
// mapping: the connector type, the verbatim URL, and BOTH errors.Is targets.
func TestMapErrorMissingIndex(t *testing.T) {
	err := firestore.MapError(status.Error(codes.FailedPrecondition, requiresIndexMessage))

	var missing *firestore.MissingIndexError
	if !errors.As(err, &missing) {
		t.Fatalf("MapError = %v (%T), want a *firestore.MissingIndexError", err, err)
	}
	if missing.URL != indexCreateURL {
		t.Errorf("URL = %q, want the server's link verbatim %q", missing.URL, indexCreateURL)
	}
	if missing.Message != requiresIndexMessage {
		t.Errorf("Message = %q, want the server's message verbatim", missing.Message)
	}
	if !errors.Is(err, firestore.ErrMissingIndex) {
		t.Error("a missing-index error must match ErrMissingIndex")
	}
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Error("a missing-index error must wrap sdk.ErrUnavailable — the query is fine, the database is not ready")
	}
	if errors.Is(err, sdk.ErrConflict) {
		t.Error("a missing-index error must NOT read as a conflict")
	}
	if !strings.Contains(err.Error(), indexCreateURL) {
		t.Errorf("Error() = %q, want it to carry the creation URL an operator can paste", err)
	}
	if !strings.Contains(err.Error(), "index") {
		t.Errorf("Error() = %q, want it to name the missing index", err)
	}
}

// TestMapErrorMissingIndexRecognizersAreHostAgnostic pins C8's fold: the two
// recognizers must not depend on WHICH console a database is linked to. A
// database provisioned through Firebase gets console.firebase.google.com links
// and one provisioned through Google Cloud gets console.cloud.google.com links,
// for the same query and the same missing index — so a recognizer naming one
// host would map half of production's missing-index failures to a bare
// sdk.ErrConflict with no URL, which reads as "somebody violated a
// precondition" and sends an operator looking in the wrong place.
func TestMapErrorMissingIndexRecognizersAreHostAgnostic(t *testing.T) {
	const cloudURL = "https://console.cloud.google.com/firestore/databases/-default-/indexes?create_composite=ClFwcm9qZWN0cy9nb3Blcm5pY3Vz&project=gopernicus-test"

	cases := []struct {
		name string
		msg  string
		url  string
	}{
		{
			name: "canonical phrasing, firebase console",
			msg:  requiresIndexMessage,
			url:  indexCreateURL,
		},
		{
			name: "canonical phrasing, cloud console",
			msg:  "The query requires an index. You can create it here: " + cloudURL,
			url:  cloudURL,
		},
		{
			name: "still building, firebase console, never says \"requires an index\"",
			msg:  buildingIndexMessage,
			url:  indexCreateURL,
		},
		{
			name: "still building, cloud console, never says \"requires an index\"",
			msg:  "The index for this query is currently building and cannot be used yet. See its status here: " + cloudURL,
			url:  cloudURL,
		},
		{
			name: "trailing sentence period is not part of the link",
			msg:  "The query requires an index. Create it here: " + cloudURL + ".",
			url:  cloudURL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := firestore.MapError(status.Error(codes.FailedPrecondition, tc.msg))

			var missing *firestore.MissingIndexError
			if !errors.As(err, &missing) {
				t.Fatalf("MapError = %v (%T), want a *firestore.MissingIndexError", err, err)
			}
			if missing.URL == "" {
				t.Fatal("URL is empty — the message carried a link and an operator needs it")
			}
			if missing.URL != tc.url {
				t.Errorf("URL = %q, want %q", missing.URL, tc.url)
			}
			if !errors.Is(err, firestore.ErrMissingIndex) || !errors.Is(err, sdk.ErrUnavailable) {
				t.Errorf("MapError = %v, want ErrMissingIndex over sdk.ErrUnavailable", err)
			}
			if errors.Is(err, sdk.ErrConflict) {
				t.Error("a missing index must not read as a precondition conflict")
			}
		})
	}
}

// TestMapErrorMissingIndexWithoutURL proves the message-only variant still maps
// (the server occasionally omits the link) and Error() degrades cleanly.
func TestMapErrorMissingIndexWithoutURL(t *testing.T) {
	const msg = "The query requires an index."
	err := firestore.MapError(status.Error(codes.FailedPrecondition, msg))

	var missing *firestore.MissingIndexError
	if !errors.As(err, &missing) {
		t.Fatalf("MapError = %v (%T), want a *firestore.MissingIndexError", err, err)
	}
	if missing.URL != "" {
		t.Errorf("URL = %q, want empty", missing.URL)
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("Error() = %q, want the server message", err)
	}
}

// TestConnectorSentinelsWrapSDKSentinels keeps the connector's own errors inside
// the sdk vocabulary: a host that only knows sdk sentinels still gets a sane
// HTTP status from each of them.
func TestConnectorSentinelsWrapSDKSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"ErrMissingIndex", firestore.ErrMissingIndex, sdk.ErrUnavailable},
		{"ErrReadAfterWrite", firestore.ErrReadAfterWrite, sdk.ErrInvalidInput},
		{"ErrWriteInReadOnlyTransaction", firestore.ErrWriteInReadOnlyTransaction, sdk.ErrInvalidInput},
		{"ErrCountInTransaction", firestore.ErrCountInTransaction, sdk.ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.want) {
				t.Errorf("%v does not wrap %v", tc.err, tc.want)
			}
			if !strings.HasPrefix(tc.err.Error(), "firestore:") {
				t.Errorf("%v: want a firestore: prefix", tc.err)
			}
		})
	}
}
