package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/totp"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// RunTOTP exercises Repositories.TOTP — the second-factor aggregate — and its
// coverage by erasure (SOC 2 S4c). It is separate from Run so a store adopts it
// with its TOTP migration (0026); Run is unchanged. Supply fresh disposable
// repositories for each invocation of newRepos. A store whose TOTP repository
// implements totp.SchemaProber must report a migrated store present here, and
// should also run RunTOTPSchemaProbe over an unmigrated one.
func RunTOTP(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("TOTP", func(t *testing.T) {
		if r := newRepos(t); r.TOTP == nil {
			t.Skip("TOTP not wired — second-factor persistence NOT verified")
		}
		for _, tc := range []struct {
			name string
			test func(*testing.T, auth.Repositories)
		}{
			{"BeginUpsertsAndRefusesConfirmed", testTOTPBegin},
			{"ConfirmGuards", testTOTPConfirmGuards},
			{"ConfirmBootstrapClosedByAnAAL2Passkey", testTOTPConfirmBootstrap},
			{"ConfirmIsAtomic", testTOTPConfirmAtomic},
			{"RecordUseCompareAndSwap", testTOTPRecordUse},
			{"ConcurrentRecordUseOneWins", testTOTPConcurrentRecordUse},
			{"RecordFailureLocksAtTheThreshold", testTOTPRecordFailure},
			{"ConcurrentRecordFailureReachesExactlyTheLock", testTOTPConcurrentRecordFailure},
			{"ReplaceRecoveryCodesCompareAndSwap", testTOTPReplace},
			{"ConsumeRecoveryCodeSingleUse", testTOTPConsume},
			{"ConcurrentConsumeOneWins", testTOTPConcurrentConsume},
			{"RemoveBumpsRevisionAndClearsBothTables", testTOTPRemove},
			{"RecoveryCodeKeyIDs", testTOTPKeyIDs},
			{"ConfirmFencedOnThePendingRow", testTOTPConfirmFenced},
			{"ConfirmRefusesInactiveUser", testTOTPConfirmInactive},
			{"RecordUseRefusedWhileLocked", testTOTPRecordUseLocked},
			{"ConsumeRecoveryCodeResetsFailures", testTOTPConsumeResets},
			{"ReplaceExpectedWithEveryCodeSpent", testTOTPReplaceAllSpent},
			{"ConcurrentReplaceOneWins", testTOTPConcurrentReplace},
			{"ConcurrentConfirmOneWins", testTOTPConcurrentConfirm},
			{"ConcurrentConfirmAndRemove", testTOTPConcurrentConfirmRemove},
			{"RemoveOfAPendingRowRevokesNothing", testTOTPRemovePending},
			{"SchemaProbeReportsMigrated", testTOTPProbeMigrated},
		} {
			t.Run(tc.name, func(t *testing.T) { tc.test(t, newRepos(t)) })
		}
		t.Run("EraseDeletesTOTPWhenWired", func(t *testing.T) {
			r := newRepos(t)
			if r.UserErasure == nil {
				t.Skip("UserErasure not wired — TOTP erasure NOT verified")
			}
			testTOTPErase(t, r)
		})
	})
}

// RunTOTPSchemaProbe proves a store's boot probe: unmigrated returns a TOTP
// repository over a store WITHOUT migration 0026, and its ProbeSchema must
// refuse, naming the migration. The pocket probes only when TOTP is wired.
func RunTOTPSchemaProbe(t *testing.T, unmigrated func(*testing.T) totp.Repository) {
	t.Helper()
	t.Run("TOTPSchemaProbe", func(t *testing.T) {
		prober, ok := unmigrated(t).(totp.SchemaProber)
		if !ok {
			t.Fatal("the TOTP repository does not implement totp.SchemaProber")
		}
		err := prober.ProbeSchema(context.Background())
		if err == nil || !strings.Contains(err.Error(), "0026") {
			t.Fatalf("ProbeSchema on an unmigrated store = %v, want an error naming 0026", err)
		}
	})
}

// pendingTOTP is a pending credential of userID at the owner's revision.
func pendingTOTP(r auth.Repositories, userID, tier string) totp.Credential {
	now := retentionNow()
	expires := now.Add(10 * time.Minute)
	return totp.Credential{
		UserID: userID, Secret: "v1.k1.sealed-" + userID, SessionID: "session-" + userID, Tier: tier,
		AuthRevision: currentAuthRevision(context.Background(), r.Users, userID), ExpiresAt: &expires, CreatedAt: now,
	}
}

// recoveryCodes are n stored codes of userID under keyID.
func recoveryCodes(userID, keyID string, n int, at time.Time) []totp.RecoveryCode {
	out := make([]totp.RecoveryCode, 0, n)
	for i := range n {
		out = append(out, totp.RecoveryCode{ID: ids.MustGenerate(), UserID: userID, KeyID: keyID, Digest: fmt.Sprintf("digest-%s-%d-%d", userID, at.UnixNano(), i), GeneratedAt: at.Truncate(time.Second)})
	}
	return out
}

// confirmation confirms userID's current pending row (its secret and session
// read back as the expectation) at the owner's revision.
func confirmation(r auth.Repositories, userID string, step int64, codes []totp.RecoveryCode) totp.Confirmation {
	pending, _ := r.TOTP.Get(context.Background(), userID)
	return totp.Confirmation{
		UserID: userID, ExpectedSecret: pending.Secret, ExpectedSessionID: pending.SessionID,
		ExpectedAuthRevision: currentAuthRevision(context.Background(), r.Users, userID), Step: step, ConfirmedAt: retentionNow(), Codes: codes,
	}
}

// mustConfirmTOTP seeds a user with a confirmed credential and n codes.
func mustConfirmTOTP(t *testing.T, r auth.Repositories, email string, n int) (string, []totp.RecoveryCode) {
	t.Helper()
	u := seedPasskeyUser(t, r, email)
	if err := r.TOTP.Begin(context.Background(), pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	codes := recoveryCodes(u.ID, "k1", n, retentionNow())
	if err := r.TOTP.Confirm(context.Background(), confirmation(r, u.ID, 100, codes)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return u.ID, codes
}

func candidate(c totp.RecoveryCode) []challenge.DigestCandidate {
	return []challenge.DigestCandidate{{KeyID: "k0", Digest: "nope"}, {KeyID: c.KeyID, Digest: c.Digest}}
}

func testTOTPBegin(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-begin@example.com")
	if _, err := r.TOTP.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Get before Begin = %v, want ErrNotFound", err)
	}
	first := pendingTOTP(r, u.ID, "bootstrap")
	if err := r.TOTP.Begin(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := r.TOTP.Get(ctx, u.ID)
	if err != nil || got.Secret != first.Secret || got.SessionID != first.SessionID || got.Tier != "bootstrap" || got.AuthRevision != first.AuthRevision ||
		got.Confirmed() || got.ExpiresAt == nil || !got.ExpiresAt.Equal(*first.ExpiresAt) || !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("pending read back = %+v, %v", got, err)
	}
	if _, err := r.TOTP.RecordFailure(ctx, u.ID, 5, time.Minute, retentionNow()); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Secret, second.SessionID = "v1.k1.replaced", "session-two"
	if err := r.TOTP.Begin(ctx, second); err != nil {
		t.Fatalf("re-Begin: %v", err)
	}
	if got, _ := r.TOTP.Get(ctx, u.ID); got.Secret != second.Secret || got.SessionID != "session-two" || got.FailedAttempts != 0 {
		t.Fatalf("re-Begin did not replace the pending row: %+v", got)
	}
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 7, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); !errors.Is(err, totp.ErrAlreadyEnrolled) {
		t.Fatalf("Begin over a confirmed row = %v, want ErrAlreadyEnrolled", err)
	}
}

func testTOTPConfirmGuards(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	if err := r.TOTP.Confirm(ctx, totp.Confirmation{UserID: "no-such-user", ConfirmedAt: retentionNow()}); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Confirm of an unknown user = %v, want ErrNotFound", err)
	}
	u := seedPasskeyUser(t, r, "totp-confirm@example.com")
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 1, nil)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Confirm without a pending row = %v, want ErrNotFound", err)
	}
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "bootstrap")); err != nil {
		t.Fatal(err)
	}
	stale := confirmation(r, u.ID, 1, nil)
	stale.ExpectedAuthRevision--
	if err := r.TOTP.Confirm(ctx, stale); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Confirm at a moved revision = %v, want ErrConflict", err)
	}
	before := currentAuthRevision(ctx, r.Users, u.ID)
	codes := recoveryCodes(u.ID, "k1", 10, retentionNow())
	in := confirmation(r, u.ID, 42, codes)
	in.Bootstrap = true
	if err := r.TOTP.Confirm(ctx, in); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	got, err := r.TOTP.Get(ctx, u.ID)
	if err != nil || !got.Confirmed() || !got.ConfirmedAt.Equal(in.ConfirmedAt) || got.LastUsedStep != 42 || got.SessionID != "" || got.Tier != "" || got.ExpiresAt != nil {
		t.Fatalf("confirmed row = %+v, %v", got, err)
	}
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != before+1 {
		t.Errorf("auth_revision after Confirm = %d, want %d", rev, before+1)
	}
	n, at, err := r.TOTP.CountRecoveryCodes(ctx, u.ID)
	if err != nil || n != 10 || at == nil || !at.Equal(codes[0].GeneratedAt) {
		t.Errorf("codes after Confirm = %d, %v, %v", n, at, err)
	}
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 43, nil)); !errors.Is(err, totp.ErrAlreadyEnrolled) {
		t.Errorf("a second Confirm = %v, want ErrAlreadyEnrolled", err)
	}
}

// testTOTPConfirmBootstrap: a bootstrap-tier Confirm for an owner holding an
// AAL2-capable passkey is ErrBootstrapClosed; an AAL2-tier one is not.
func testTOTPConfirmBootstrap(t *testing.T, r auth.Repositories) {
	if r.Passkeys == nil {
		t.Skip("Passkeys not wired — the bootstrap guard NOT verified")
	}
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-bootstrap@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "bootstrap")); err != nil {
		t.Fatal(err)
	}
	mustCreatePasskey(t, r, newPasskey(u.ID, []byte("handle-totp-bootstrap"), "uv", retentionNow()))
	in := confirmation(r, u.ID, 1, nil)
	in.Bootstrap = true
	if err := r.TOTP.Confirm(ctx, in); !errors.Is(err, totp.ErrBootstrapClosed) {
		t.Fatalf("bootstrap Confirm beside an AAL2 passkey = %v, want ErrBootstrapClosed", err)
	}
	if got, _ := r.TOTP.Get(ctx, u.ID); got.Confirmed() {
		t.Fatal("a refused Confirm confirmed the row")
	}
	in.Bootstrap = false
	if err := r.TOTP.Confirm(ctx, in); err != nil {
		t.Fatalf("an AAL2-tier Confirm: %v", err)
	}
}

// testTOTPConfirmAtomic: a Confirm whose codes collide inserts nothing and
// leaves the row pending at the same revision.
func testTOTPConfirmAtomic(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-atomic@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	before := currentAuthRevision(ctx, r.Users, u.ID)
	codes := recoveryCodes(u.ID, "k1", 3, retentionNow())
	codes[2].Digest = codes[0].Digest
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 9, codes)); err == nil {
		t.Fatal("a Confirm with colliding digests succeeded")
	}
	if got, _ := r.TOTP.Get(ctx, u.ID); got.Confirmed() || got.LastUsedStep != 0 {
		t.Fatalf("a failed Confirm left %+v", got)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, u.ID); n != 0 {
		t.Fatalf("a failed Confirm left %d codes", n)
	}
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != before {
		t.Fatalf("a failed Confirm moved auth_revision %d → %d", before, rev)
	}
}

func testTOTPRecordUse(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	pending := seedPasskeyUser(t, r, "totp-use-pending@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, pending.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	if err := r.TOTP.RecordUse(ctx, pending.ID, 0, 5, retentionNow()); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("RecordUse on a pending row = %v, want ErrConflict", err)
	}
	userID, _ := mustConfirmTOTP(t, r, "totp-use@example.com", 0)
	if _, err := r.TOTP.RecordFailure(ctx, userID, 5, time.Minute, retentionNow()); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ expected, step int64 }{
		"an equal step": {100, 100}, "a lower step": {100, 99}, "a stale expectation": {99, 101},
	} {
		if err := r.TOTP.RecordUse(ctx, userID, tc.expected, tc.step, retentionNow()); !errors.Is(err, sdk.ErrConflict) {
			t.Errorf("RecordUse with %s = %v, want ErrConflict", name, err)
		}
	}
	at := retentionNow()
	if err := r.TOTP.RecordUse(ctx, userID, 100, 101, at); err != nil {
		t.Fatalf("RecordUse: %v", err)
	}
	got, _ := r.TOTP.Get(ctx, userID)
	if got.LastUsedStep != 101 || got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) || got.FailedAttempts != 0 || got.LockedUntil != nil {
		t.Fatalf("after RecordUse = %+v", got)
	}
	if err := r.TOTP.RecordUse(ctx, "no-such-user", 0, 1, at); !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("RecordUse with no row = %v, want ErrConflict", err)
	}
}

func testTOTPConcurrentRecordUse(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, _ := mustConfirmTOTP(t, r, "totp-use-race@example.com", 0)
	oneWins(t, race(4, func(i int) error {
		return r.TOTP.RecordUse(ctx, userID, 100, int64(101+i), retentionNow())
	}), sdk.ErrConflict)
}

func testTOTPRecordFailure(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	if _, err := r.TOTP.RecordFailure(ctx, "no-such-user", 5, time.Minute, retentionNow()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("RecordFailure with no row = %v, want ErrNotFound", err)
	}
	userID, _ := mustConfirmTOTP(t, r, "totp-fail@example.com", 0)
	now := retentionNow()
	for i := 1; i < 3; i++ {
		f, err := r.TOTP.RecordFailure(ctx, userID, 3, 5*time.Minute, now)
		if err != nil || f.Attempts != i || f.LockedUntil != nil {
			t.Fatalf("failure %d = %+v, %v", i, f, err)
		}
	}
	f, err := r.TOTP.RecordFailure(ctx, userID, 3, 5*time.Minute, now)
	if err != nil || f.Attempts != 3 || f.LockedUntil == nil || !f.LockedUntil.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("the locking failure = %+v, %v", f, err)
	}
	got, _ := r.TOTP.Get(ctx, userID)
	if got.FailedAttempts != 0 || got.LockedUntil == nil || !got.Locked(now) {
		t.Fatalf("after the lock = %+v", got)
	}
}

// testTOTPConcurrentRecordFailure: lockAt racers from zero — exactly one sets
// the lock (BE3: the increment reads the row it writes).
func testTOTPConcurrentRecordFailure(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, _ := mustConfirmTOTP(t, r, "totp-fail-race@example.com", 0)
	const lockAt = 5
	locks := make([]bool, lockAt)
	errs := race(lockAt, func(i int) error {
		f, err := r.TOTP.RecordFailure(ctx, userID, lockAt, 5*time.Minute, retentionNow())
		locks[i] = f.LockedUntil != nil
		return err
	})
	n := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
		if locks[i] {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("racers that set the lock = %d, want exactly 1", n)
	}
	if got, _ := r.TOTP.Get(ctx, userID); got.LockedUntil == nil || got.FailedAttempts != 0 {
		t.Fatalf("after the race = %+v", got)
	}
}

func testTOTPReplace(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	pending := seedPasskeyUser(t, r, "totp-replace-pending@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, pending.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, pending.ID, recoveryCodes(pending.ID, "k1", 2, retentionNow()), nil); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Replace for a pending credential = %v, want ErrNotFound", err)
	}
	userID, old := mustConfirmTOTP(t, r, "totp-replace@example.com", 4)
	current := old[0].GeneratedAt
	stale := current.Add(-time.Hour)
	next := recoveryCodes(userID, "k2", 10, retentionNow().Add(time.Hour))
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, userID, next, &stale); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("Replace with a stale generation = %v, want ErrConflict", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, userID); n != 4 {
		t.Fatalf("a refused Replace left %d codes, want 4", n)
	}
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, userID, next, &current); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	n, at, _ := r.TOTP.CountRecoveryCodes(ctx, userID)
	if n != 10 || at == nil || !at.Equal(next[0].GeneratedAt) {
		t.Fatalf("after Replace = %d, %v", n, at)
	}
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(old[0]), retentionNow()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a replaced code = %v, want ErrNotFound", err)
	}
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, userID, recoveryCodes(userID, "k2", 1, retentionNow().Add(2*time.Hour)), nil); err != nil {
		t.Fatalf("an unconditional Replace: %v", err)
	}
}

func testTOTPConsume(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, codes := mustConfirmTOTP(t, r, "totp-consume@example.com", 3)
	otherID, others := mustConfirmTOTP(t, r, "totp-consume-other@example.com", 2)
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(others[0]), retentionNow()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("another user's code = %v, want ErrNotFound", err)
	}
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, []challenge.DigestCandidate{{KeyID: "k9", Digest: codes[1].Digest}}, retentionNow()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a digest under another key id = %v, want ErrNotFound", err)
	}
	remaining, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(codes[1]), retentionNow())
	if err != nil || remaining != 2 {
		t.Fatalf("Consume = %d, %v; want 2 remaining", remaining, err)
	}
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(codes[1]), retentionNow()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a spent code = %v, want ErrNotFound", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, otherID); n != 2 {
		t.Errorf("the other user's codes = %d, want 2", n)
	}
}

func testTOTPConcurrentConsume(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, codes := mustConfirmTOTP(t, r, "totp-consume-race@example.com", 2)
	oneWins(t, race(4, func(int) error {
		_, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(codes[0]), retentionNow())
		return err
	}), sdk.ErrNotFound)
}

func testTOTPRemove(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	if _, err := r.TOTP.Remove(ctx, "no-such-user", 0); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Remove of an unknown user = %v, want ErrNotFound", err)
	}
	bare := seedPasskeyUser(t, r, "totp-remove-bare@example.com")
	bareRev := currentAuthRevision(ctx, r.Users, bare.ID)
	bareSession := createSession(t, r, bare.ID, "totp-remove-bare")
	if got, err := r.TOTP.Remove(ctx, bare.ID, bareRev); err != nil || got.Credential {
		t.Fatalf("Remove with no row = %+v, %v", got, err)
	}
	if rev := currentAuthRevision(ctx, r.Users, bare.ID); rev != bareRev {
		t.Errorf("Remove with no row moved auth_revision")
	}
	if _, err := r.Sessions.Get(ctx, bareSession.ID); err != nil {
		t.Errorf("Remove with no row revoked a session: %v", err)
	}
	userID, _ := mustConfirmTOTP(t, r, "totp-remove@example.com", 6)
	otherID, _ := mustConfirmTOTP(t, r, "totp-remove-other@example.com", 2)
	s := createSession(t, r, userID, "totp-remove")
	grant := mustCreateGrant(t, r, userID, s.ID)
	rev := currentAuthRevision(ctx, r.Users, userID)
	if _, err := r.TOTP.Remove(ctx, userID, rev-1); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("Remove at a stale revision = %v, want ErrConflict", err)
	}
	got, err := r.TOTP.Remove(ctx, userID, rev)
	if err != nil || !got.Credential || got.RecoveryCodes != 6 {
		t.Fatalf("Remove = %+v, %v", got, err)
	}
	if _, err := r.TOTP.Get(ctx, userID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the credential after Remove = %v", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, userID); n != 0 {
		t.Errorf("codes after Remove = %d", n)
	}
	if now := currentAuthRevision(ctx, r.Users, userID); now != rev+1 {
		t.Errorf("auth_revision after Remove = %d, want %d", now, rev+1)
	}
	if _, err := r.Sessions.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a session after Remove = %v, want revoked", err)
	}
	if grantSurvives(t, r, grant) {
		t.Errorf("an authentication grant survived Remove")
	}
	if _, err := r.TOTP.Get(ctx, otherID); err != nil {
		t.Errorf("the other user's credential: %v", err)
	}
}

func testTOTPKeyIDs(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u1, _ := mustConfirmTOTP(t, r, "totp-keys-1@example.com", 2)
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, u1, append(recoveryCodes(u1, "k1", 1, retentionNow()), recoveryCodes(u1, "k0", 1, retentionNow().Add(time.Second))...), nil); err != nil {
		t.Fatal(err)
	}
	mustConfirmTOTP(t, r, "totp-keys-2@example.com", 1)
	got, err := r.TOTP.RecoveryCodeKeyIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	if len(seen) != 2 || seen["k0"] != 1 || seen["k1"] != 1 {
		t.Fatalf("RecoveryCodeKeyIDs = %v, want distinct [k0 k1]", got)
	}
}

func testTOTPProbeMigrated(t *testing.T, r auth.Repositories) {
	prober, ok := r.TOTP.(totp.SchemaProber)
	if !ok {
		t.Skip("the TOTP repository does not probe its schema")
	}
	if err := prober.ProbeSchema(context.Background()); err != nil {
		t.Fatalf("ProbeSchema on a migrated store = %v", err)
	}
}

// testTOTPErase: with TOTP wired erasure deletes the credential and codes and
// counts them; with TOTP off it touches neither (owner ruling: 0026 is required
// only when TOTP is wired).
func testTOTPErase(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, _ := mustConfirmTOTP(t, r, "totp-erase@example.com", 10)
	bystander, _ := mustConfirmTOTP(t, r, "totp-erase-bystander@example.com", 3)
	in := eraseInput(userID, retentionNow())
	in.TOTP = true
	got := mustErase(t, r, in)
	if got.Counts.TOTP != 1 || got.Counts.RecoveryCodes != 10 {
		t.Errorf("EraseCounts TOTP/RecoveryCodes = %d/%d, want 1/10", got.Counts.TOTP, got.Counts.RecoveryCodes)
	}
	if _, err := r.TOTP.Get(ctx, userID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the erased user's credential = %v, want ErrNotFound", err)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, userID); n != 0 {
		t.Errorf("the erased user's codes = %d, want 0", n)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, bystander); n != 3 {
		t.Errorf("the bystander's codes = %d, want 3", n)
	}

	off, _ := mustConfirmTOTP(t, r, "totp-erase-off@example.com", 2)
	got = mustErase(t, r, eraseInput(off, retentionNow()))
	if got.Counts.TOTP != 0 || got.Counts.RecoveryCodes != 0 {
		t.Errorf("an erase with TOTP off counted %d/%d", got.Counts.TOTP, got.Counts.RecoveryCodes)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, off); n != 2 {
		t.Errorf("an erase with TOTP off touched the codes: %d", n)
	}
}

// testTOTPConfirmFenced (B1): a Confirm checked against one pending row never
// confirms a row a concurrent Begin swapped in.
func testTOTPConfirmFenced(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-fence@example.com")
	p1 := pendingTOTP(r, u.ID, "aal2")
	if err := r.TOTP.Begin(ctx, p1); err != nil {
		t.Fatal(err)
	}
	stale := confirmation(r, u.ID, 5, nil)
	p2 := p1
	p2.Secret, p2.SessionID = "v1.k1.swapped", "session-swapped"
	if err := r.TOTP.Begin(ctx, p2); err != nil {
		t.Fatal(err)
	}
	if err := r.TOTP.Confirm(ctx, stale); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Confirm against the replaced row = %v, want ErrNotFound", err)
	}
	got, _ := r.TOTP.Get(ctx, u.ID)
	if got.Confirmed() || got.Secret != p2.Secret || got.SessionID != p2.SessionID {
		t.Fatalf("after the refused Confirm = %+v, want P2 still pending", got)
	}
	sameSecret := stale
	sameSecret.ExpectedSecret = p2.Secret
	if err := r.TOTP.Confirm(ctx, sameSecret); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Confirm with the right secret and another session = %v, want ErrNotFound", err)
	}
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 5, nil)); err != nil {
		t.Fatalf("Confirm of the current row: %v", err)
	}
}

func testTOTPConfirmInactive(t *testing.T, r auth.Repositories) {
	if r.UserAdmin == nil {
		t.Skip("UserAdmin not wired — the inactive-owner refusal NOT verified")
	}
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-inactive@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	deactivate(t, r, u.ID)
	if err := r.TOTP.Confirm(ctx, confirmation(r, u.ID, 1, nil)); !errors.Is(err, session.ErrUserNotActive) {
		t.Fatalf("Confirm for an inactive owner = %v, want ErrUserNotActive", err)
	}
}

// testTOTPRecordUseLocked (M1): RecordUse refuses while a lockout is in force.
func testTOTPRecordUseLocked(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, _ := mustConfirmTOTP(t, r, "totp-use-locked@example.com", 0)
	now := retentionNow()
	if f, err := r.TOTP.RecordFailure(ctx, userID, 1, 5*time.Minute, now); err != nil || f.LockedUntil == nil {
		t.Fatalf("lock = %+v, %v", f, err)
	}
	if err := r.TOTP.RecordUse(ctx, userID, 100, 101, now.Add(time.Minute)); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("RecordUse while locked = %v, want ErrConflict", err)
	}
	if err := r.TOTP.RecordUse(ctx, userID, 100, 101, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("RecordUse after the lockout = %v", err)
	}
}

func testTOTPConsumeResets(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, codes := mustConfirmTOTP(t, r, "totp-consume-reset@example.com", 2)
	if _, err := r.TOTP.RecordFailure(ctx, userID, 5, time.Minute, retentionNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(codes[0]), retentionNow()); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.TOTP.Get(ctx, userID); got.FailedAttempts != 0 {
		t.Fatalf("FailedAttempts after a recovery-code use = %d, want 0", got.FailedAttempts)
	}
}

// testTOTPReplaceAllSpent: with every code consumed there is no current
// generation, so a Replace expecting one conflicts.
func testTOTPReplaceAllSpent(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, codes := mustConfirmTOTP(t, r, "totp-replace-spent@example.com", 1)
	generated := codes[0].GeneratedAt
	if _, err := r.TOTP.ConsumeRecoveryCode(ctx, userID, candidate(codes[0]), retentionNow()); err != nil {
		t.Fatal(err)
	}
	if err := r.TOTP.ReplaceRecoveryCodes(ctx, userID, recoveryCodes(userID, "k1", 2, retentionNow().Add(time.Hour)), &generated); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("Replace expecting a spent generation = %v, want ErrConflict", err)
	}
}

func testTOTPConcurrentReplace(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, codes := mustConfirmTOTP(t, r, "totp-replace-race@example.com", 2)
	expected := codes[0].GeneratedAt
	oneWins(t, race(4, func(i int) error {
		return r.TOTP.ReplaceRecoveryCodes(ctx, userID, recoveryCodes(userID, "k1", 3, retentionNow().Add(time.Duration(i+1)*time.Hour)), &expected)
	}), sdk.ErrConflict)
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, userID); n != 3 {
		t.Fatalf("codes after the race = %d, want 3", n)
	}
}

func testTOTPConcurrentConfirm(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-confirm-race@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	in := confirmation(r, u.ID, 9, nil)
	before := in.ExpectedAuthRevision
	errs := race(3, func(i int) error {
		c := in
		c.Codes = recoveryCodes(u.ID, "k1", 2, retentionNow().Add(time.Duration(i)*time.Second))
		return r.TOTP.Confirm(ctx, c)
	})
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, totp.ErrAlreadyEnrolled) && !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("a losing Confirm = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("Confirm winners = %d, want 1 (%v)", wins, errs)
	}
	if rev := currentAuthRevision(ctx, r.Users, u.ID); rev != before+1 {
		t.Fatalf("auth_revision = %d, want %d", rev, before+1)
	}
	if n, _, _ := r.TOTP.CountRecoveryCodes(ctx, u.ID); n != 2 {
		t.Fatalf("codes after the race = %d, want one set of 2", n)
	}
}

// testTOTPConcurrentConfirmRemove: a Confirm racing a Remove at one revision —
// whichever wins, the other is refused and no half state remains.
func testTOTPConcurrentConfirmRemove(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	userID, _ := mustConfirmTOTP(t, r, "totp-cr-anchor@example.com", 0)
	_ = userID
	u := seedPasskeyUser(t, r, "totp-confirm-remove@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	in := confirmation(r, u.ID, 9, recoveryCodes(u.ID, "k1", 2, retentionNow()))
	errs := race(2, func(i int) error {
		if i == 0 {
			return r.TOTP.Confirm(ctx, in)
		}
		_, err := r.TOTP.Remove(ctx, u.ID, in.ExpectedAuthRevision)
		return err
	})
	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("both lost: %v", errs)
	}
	got, err := r.TOTP.Get(ctx, u.ID)
	n, _, _ := r.TOTP.CountRecoveryCodes(ctx, u.ID)
	switch {
	case errors.Is(err, sdk.ErrNotFound):
		if n != 0 {
			t.Fatalf("removed, yet %d codes remain", n)
		}
	case err == nil && got.Confirmed():
		if n != 2 {
			t.Fatalf("confirmed with %d codes, want 2", n)
		}
	default:
		t.Fatalf("after the race = %+v, %v", got, err)
	}
}

// testTOTPRemovePending (N3): removing a pending-only row deletes it and
// touches neither the revision nor any session.
func testTOTPRemovePending(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	u := seedPasskeyUser(t, r, "totp-remove-pending@example.com")
	if err := r.TOTP.Begin(ctx, pendingTOTP(r, u.ID, "aal2")); err != nil {
		t.Fatal(err)
	}
	s := createSession(t, r, u.ID, "totp-remove-pending")
	rev := currentAuthRevision(ctx, r.Users, u.ID)
	got, err := r.TOTP.Remove(ctx, u.ID, rev)
	if err != nil || !got.Pending || got.Credential {
		t.Fatalf("Remove of a pending row = %+v, %v", got, err)
	}
	if _, err := r.TOTP.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the pending row survived: %v", err)
	}
	if now := currentAuthRevision(ctx, r.Users, u.ID); now != rev {
		t.Errorf("auth_revision moved %d → %d", rev, now)
	}
	if _, err := r.Sessions.Get(ctx, s.ID); err != nil {
		t.Errorf("a session was revoked: %v", err)
	}
}

// mustCreateGrant stores an AAL1 remove_totp grant of userID on sessionID.
func mustCreateGrant(t *testing.T, r auth.Repositories, userID, sessionID string) authgrant.Grant {
	t.Helper()
	now := time.Now().UTC()
	g, err := r.AuthenticationGrants.Create(context.Background(), authgrant.Grant{
		SessionID: sessionID, UserID: userID, Purpose: authgrant.PurposeRemoveTOTP, ContextDigest: "totp-grant",
		Assurance: session.AssuranceAAL1, AuthenticatedAt: now, ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
	}, currentAuthRevision(context.Background(), r.Users, userID), now)
	if err != nil {
		t.Fatalf("AuthenticationGrants.Create: %v", err)
	}
	return g
}

// grantSurvives reports whether g can still be consumed, after restoring its
// session (Remove revoked it) so only the grant row decides.
func grantSurvives(t *testing.T, r auth.Repositories, g authgrant.Grant) bool {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := r.Sessions.Create(ctx, session.Session{ID: g.SessionID, UserID: g.UserID, RefreshTokenHash: "totp-grant-restored-" + g.SessionID, ExpiresAt: now.Add(time.Hour), CreatedAt: now}); err != nil {
		t.Fatalf("restore the session: %v", err)
	}
	_, err := r.AuthenticationGrants.Consume(ctx, authgrant.Requirement{
		SessionID: g.SessionID, UserID: g.UserID, Purpose: g.Purpose, ContextDigest: g.ContextDigest,
		AuthenticatedAfter: now.Add(-5 * time.Minute), MinAssurance: session.AssuranceAAL1,
	}, now)
	return err == nil
}

// deactivate sets userID deactivated through the administration port.
func deactivate(t *testing.T, r auth.Repositories, userID string) {
	t.Helper()
	if _, err := r.UserAdmin.SetStatus(context.Background(), userID, user.StatusDeactivated, time.Now().UTC()); err != nil {
		t.Fatalf("SetStatus(deactivated): %v", err)
	}
}
