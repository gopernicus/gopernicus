package firestore

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The retry classifier, hermetically. It decides whether an error leaving a
// transaction callback is a RACE (re-run it) or an ANSWER (return it), and it
// gets exactly one chance: detachVendorRetry re-roots anything it calls a race
// on sdk.ErrConflict alone, which DROPS the domain sentinel the caller was going
// to branch on. A misclassified answer therefore does not merely spin six times,
// it arrives unrecognizable.

// TestDomainAnswersAreNeverRetried enumerates every domain sentinel this package
// can hand back from a callback — including the ones that wrap sdk.ErrConflict
// and would otherwise pass the first rule.
func TestDomainAnswersAreNeverRetried(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"stale auth revision (credential CAS)", errStaleAuthRevision},
		{"refresh rotation CAS", session.ErrRotationConflict},
		{"owning user is not active", session.ErrUserNotActive},
		{"passwordless stable rejection", passwordless.ErrRedemption},
		{"identifier verification required", identifier.ErrVerificationRequired},
		{"credential: no login method", credential.ErrNoLoginMethod},
		{"credential: no recovery method", credential.ErrNoRecoveryMethod},
		{"credential: insufficient recovery", credential.ErrInsufficientRecovery},
		{"credential: recovery requires non-PSTN", credential.ErrRecoveryRequiresNonPSTN},
		{"credential: insufficient assurance", credential.ErrInsufficientAssurance},
		{"lost claim", errClaimLost},
		{"ambient transaction refused", ErrAmbientTransactionUnsupported},
		{"not found", sdk.ErrNotFound},
		{"already exists", sdk.ErrAlreadyExists},
		{"expired", sdk.ErrExpired},
		{"invalid input", sdk.ErrInvalidInput},
		{"unauthorized", sdk.ErrUnauthorized},
		{"forbidden", sdk.ErrForbidden},
		{"unavailable", sdk.ErrUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if retryableConflict(tc.err) {
				t.Errorf("retryableConflict(%v) = true — a domain answer would be re-run and then re-rooted on a bare sdk.ErrConflict, losing the sentinel the caller branches on", tc.err)
			}
			// Wrapped the way a call site actually returns it.
			wrapped := fmt.Errorf("authentication firestore store: doing the thing: %w", tc.err)
			if retryableConflict(wrapped) {
				t.Errorf("retryableConflict(wrapped %v) = true", tc.err)
			}
		})
	}
}

// TestContentionIsRetried is the other half: a mapped Aborted — the condition
// the whole loop exists for — must still be recognized, whether it surfaced from
// a transactional read or from a losing commit.
//
// The status value comes from the connector's firestoretest.AbortedError rather
// than from google.golang.org/grpc, which a pocket store adapter must not import
// (guard G24: the connector is the isolation unit for the client library and the
// companion modules its API forces on a caller).
func TestContentionIsRetried(t *testing.T) {
	mapped := firestoredb.MapError(firestoretest.AbortedError("Transaction lock timeout"))
	if !errors.Is(mapped, sdk.ErrConflict) {
		t.Fatalf("MapError(Aborted) = %v, want an sdk.ErrConflict", mapped)
	}
	if !retryableConflict(mapped) {
		t.Error("retryableConflict(mapped Aborted) = false — the contention loop would never retry, and the concurrency family resolves to an infrastructure error instead of a winner")
	}
	if !retryableConflict(detachVendorRetry(mapped)) {
		t.Error("a detached conflict is no longer recognized as retryable — this store's own loop would stop retrying it")
	}
}

// TestDetachVendorRetryDropsTheAbortedStatus pins the seam's mechanism.
//
// detachVendorRetry formats the cause with %s and NOT %w on purpose: %w would
// leave the gRPC status in the chain, the vendor's own retry gate (isAborted →
// status.FromError → errors.As, which walks Unwrap) would find it, and BOTH
// loops would fire on one error — the un-jittered vendor backoff re-running N
// contenders in the same order, which is the starvation the authorization train
// measured. If this test fails after a change to %w, the change is the
// regression, not the test.
func TestDetachVendorRetryDropsTheAbortedStatus(t *testing.T) {
	// The vendor's gate is status.FromError, which walks Unwrap with errors.As;
	// "is the status still in the chain" is therefore exactly "is the original
	// value still in the chain", and that question needs no grpc import (G24).
	aborted := firestoretest.AbortedError("Transaction lock timeout")
	mapped := firestoredb.MapError(aborted)
	if !errors.Is(mapped, aborted) {
		t.Fatal("MapError no longer keeps the vendor status in the chain — the fixture no longer reproduces the condition this seam exists for")
	}

	detached := detachVendorRetry(mapped)
	if errors.Is(detached, aborted) {
		t.Error("the detached error still carries the vendor's Aborted status: the vendor's own retry loop will fire on it again, un-jittered, beside this store's")
	}
	if !strings.Contains(detached.Error(), "Transaction lock timeout") {
		t.Errorf("the detached error lost the cause's message (%q) — the status leaves, the message stays", detached)
	}
	if !errors.Is(detached, sdk.ErrConflict) {
		t.Error("the detached error no longer matches sdk.ErrConflict")
	}
}

// TestDetachVendorRetryPassesAnswersThrough proves the seam is inert for
// everything else: a domain answer must leave the callback BYTE-IDENTICAL, or
// errors.Is at the port boundary stops working.
func TestDetachVendorRetryPassesAnswersThrough(t *testing.T) {
	for _, err := range []error{session.ErrRotationConflict, passwordless.ErrRedemption, errStaleAuthRevision, sdk.ErrNotFound, nil} {
		if got := detachVendorRetry(err); got != err {
			t.Errorf("detachVendorRetry(%v) = %v, want the error unchanged", err, got)
		}
	}
}

// TestLostClaimLeaksNoLayoutOrFingerprint is the disclosure rule for the one
// error a caller sees most often under contention.
//
// The vendor's AlreadyExists names the losing document:
// "…/identifier_claims/<64 hex>". That string carries this store's collection
// layout AND a SHA-256 fingerprint of the very address, token or secret digest
// the operation was about — an attacker-supplied value, echoed into a host's
// error log at whatever rate a probe can drive it. errClaimLost keeps the
// sentinel and drops both.
func TestLostClaimLeaksNoLayoutOrFingerprint(t *testing.T) {
	if !errors.Is(errClaimLost, sdk.ErrAlreadyExists) {
		t.Fatal("errClaimLost no longer matches sdk.ErrAlreadyExists — every duplicate-create conformance case asserts that sentinel")
	}

	msg := errClaimLost.Error()
	for _, collection := range []string{
		collectionIdentifierClaims, collectionIdentifierPrimaries, collectionRefreshHashClaims,
		collectionAPIKeyHashClaims, collectionInvitationTokens, collectionInvitationPending,
		collectionChallengeDigests,
	} {
		if strings.Contains(msg, collection) {
			t.Errorf("the lost-claim error names the %s collection: %q", collection, msg)
		}
	}
	if regexp.MustCompile(`[0-9a-f]{64}`).MatchString(msg) {
		t.Errorf("the lost-claim error carries a 64-hex key fingerprint: %q", msg)
	}
}
