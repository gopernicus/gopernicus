//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// The consume family's ROLLBACK discipline, which the shared conformance suite
// exercises only at the happy end: a Firestore transaction callback may run more
// than once, so an outcome an ATTEMPT computed is not yet an outcome the
// OPERATION may report (connector README, "a committed outcome is not the
// callback's error"). The two tests that inject an Aborted status drive the real
// helpers through the real retry loop with the world CHANGED between attempts,
// which is the only shape that can tell a reset outcome from a leaked one.

// retryWithInjectedAbort runs body under this store's own contention loop —
// retryContention around db.Transact, exactly as retryTransact composes them —
// aborting the FIRST attempt with a real gRPC Aborted status and running
// betweenAttempts once the aborted transaction has rolled back and released its
// locks. It reports how many attempts ran.
//
// The abort is the genuine article (firestoretest.AbortedError), mapped and
// detached the way this store maps and detaches a contended read, so the loop
// under test is the shipping one and not a rehearsal of it.
func retryWithInjectedAbort(ctx context.Context, db *firestoredb.DB, betweenAttempts func(), body func(ctx context.Context) error) (int, error) {
	attempts := 0
	err := retryContention(ctx, func() error {
		attempts++
		if attempts == 2 {
			betweenAttempts()
		}
		attempt := attempts
		return db.Transact(ctx, func(ctx context.Context) error {
			if err := body(ctx); err != nil {
				return detachVendorRetry(err)
			}
			if attempt == 1 {
				return detachVendorRetry(firestoredb.MapError(firestoretest.AbortedError("a lost commit race")))
			}
			return nil
		})
	})
	return attempts, err
}

// TestStateConsumeResetsItsOutcomePerAttempt: attempt one reads an EXPIRED flow
// secret and queues its deletion; the attempt is aborted, the row is removed out
// of band, and attempt two finds nothing. The operation must answer ABSENT.
//
// Reporting sdk.ErrExpired there would be a rolled-back attempt's opinion
// presented as a committed outcome — the exact bug the reset at the top of
// consumeState prevents, and one no port-level test can distinguish from the
// correct answer without forcing the retry.
func TestStateConsumeResetsItsOutcomePerAttempt(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	st := oauthstate.New("google", oauthstate.PurposeFlow, []byte("payload"), time.Minute, time.Now().Add(-time.Hour))
	if _, err := r.OAuthStates.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var out stateConsumption
	attempts, err := retryWithInjectedAbort(ctx, db,
		func() {
			// The aborted transaction has ended, so this delete takes no lock
			// contention: attempt two opens on a world where the row is gone.
			if err := dropOAuthState(ctx, db, db.WriterFrom(ctx), st.Token); err != nil {
				t.Errorf("out-of-band delete: %v", err)
			}
		},
		func(ctx context.Context) error { return consumeState(ctx, db, st.Token, &out) },
	)
	if err != nil {
		t.Fatalf("the retried consume must succeed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the injected Aborted must re-run the callback)", attempts)
	}
	if out.found || out.expired {
		t.Fatalf("attempt one's outcome leaked into attempt two: %+v, want the absent outcome", out)
	}
}

// TestGrantConsumeResetsItsOutcomePerAttempt is the same proof for the step-up
// grant: an expired match read by a losing attempt must not be reported once the
// committing attempt finds the grant already spent by someone else.
func TestGrantConsumeResetsItsOutcomePerAttempt(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	now := time.Now()
	if _, err := r.AuthenticationGrants.Create(ctx, newTestGrant("s1", "u1", authgrant.PurposeSetPassword, "ctx-A", -time.Minute, now)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var out grantConsumption
	consumeKey := grantConsumeKey("s1", authgrant.PurposeSetPassword, "ctx-A")
	attempts, err := retryWithInjectedAbort(ctx, db,
		func() {
			if err := r.AuthenticationGrants.DeleteBySession(ctx, "s1"); err != nil {
				t.Errorf("out-of-band cascade: %v", err)
			}
		},
		func(ctx context.Context) error { return consumeGrant(ctx, db, consumeKey, now, &out) },
	)
	if err != nil {
		t.Fatalf("the retried consume must succeed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if out.found || out.expired {
		t.Fatalf("attempt one's outcome leaked into attempt two: %+v, want the absent outcome", out)
	}
}

// TestExpiredGrantUnderContentionCommitsExactlyOneOutcome is the same property
// end to end, through the PORT and under real emulator contention rather than an
// injected status: eight consumers race one EXPIRED grant, so every one of them
// reads a row that is present and expired. Exactly one may report sdk.ErrExpired
// — the one whose consumption committed — and the rest must report
// sdk.ErrNotFound, because by then the grant is spent.
//
// It is the committed-outcome rule and the single-winner rule in one case: a
// store that reported the expired outcome from a rolled-back attempt would hand
// eight callers an "I spent it" answer for one spend.
func TestExpiredGrantUnderContentionCommitsExactlyOneOutcome(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	now := time.Now()
	if _, err := r.AuthenticationGrants.Create(ctx, newTestGrant("s1", "u1", authgrant.PurposeSetPassword, "ctx-A", -time.Minute, now)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = r.AuthenticationGrants.Consume(ctx, "s1", authgrant.PurposeSetPassword, "ctx-A", now)
		}(i)
	}
	close(start)
	wg.Wait()

	expired, absent := 0, 0
	for _, err := range errs {
		switch {
		case errors.Is(err, sdk.ErrExpired):
			expired++
		case errors.Is(err, sdk.ErrNotFound):
			absent++
		default:
			t.Errorf("unexpected consume outcome: %v", err)
		}
	}
	if expired != 1 || absent != n-1 {
		t.Fatalf("outcomes = %d expired / %d absent, want 1 / %d (%v)", expired, absent, n-1, errs)
	}
}

// TestExpiredStateConsumeCommitsItsDeletion pins the other half of N-D2 for the
// flow secret: the deletion COMMITS even though the operation reports an error.
// The row must be gone afterwards — an expired secret that survived its own
// consume is replayable, which is what "deleted REGARDLESS of expiry" exists to
// prevent — and the follow-up consume must say absent, not expired.
func TestExpiredStateConsumeCommitsItsDeletion(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	st := oauthstate.New("google", oauthstate.PurposePendingLink, []byte("payload"), time.Minute, time.Now().Add(-time.Hour))
	if _, err := r.OAuthStates.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.OAuthStates.Consume(ctx, st.Token); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("Consume(expired): err=%v, want ErrExpired", err)
	}
	if _, err := readOAuthState(ctx, db, db.ReaderFrom(ctx), st.Token); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("the expired row survived its consume: err=%v, want ErrNotFound", err)
	}
}

// newTestGrant mints a grant the way the conformance suite's fixture does.
func newTestGrant(sessionID, userID, purpose, contextDigest string, ttl time.Duration, now time.Time) authgrant.Grant {
	now = now.UTC()
	return authgrant.Grant{
		SessionID:       sessionID,
		UserID:          userID,
		Purpose:         purpose,
		ContextDigest:   contextDigest,
		Methods:         []session.AuthenticationMethod{{Kind: session.MethodPassword, Assurance: session.AssuranceAAL1}},
		Assurance:       session.AssuranceAAL1,
		AuthenticatedAt: now,
		CreatedAt:       now,
		ExpiresAt:       now.Add(ttl),
	}
}
