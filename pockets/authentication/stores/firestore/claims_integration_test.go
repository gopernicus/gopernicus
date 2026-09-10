//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// The claim-lifecycle cases. The conformance suite proves the OUTCOMES a port
// can see; these prove the mechanism underneath — that each claim document exists
// exactly while its row satisfies the SQL index's STORED predicate, on every edge
// of that predicate. A store can pass every port case with a claim it never
// releases: the duplicate only appears when the freed key is next claimed, which
// in production is months later and in a test suite is never.
//
// The two predicates, from migration 0010:
//
//	auth claim:    replaced_at IS NULL AND (login_enabled OR recovery_enabled)
//	primary claim: replaced_at IS NULL AND is_primary

const emailKind = string(identifier.KindEmail)

// TestAuthClaimFollowsTheUsePredicate walks the login/recovery conjunct in both
// directions. Disabling BOTH authentication-bearing uses takes the row out of
// the index, so the address must become claimable by someone else; re-enabling
// either one must take the claim back. A store that released nothing would keep
// the address locked forever, and a store that released too eagerly would let a
// second account claim an address the first still logs in with.
func TestAuthClaimFollowsTheUsePredicate(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const address = "predicate@example.com"
	_, ident := seedUser(t, r, identifier.KindEmail, address,
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)

	if _, ok := authClaimOwner(t, db, emailKind, address); !ok {
		t.Fatal("a login-enabled identifier did not take the authentication claim")
	}

	row := readRow(t, db, ident.ID)

	// Both uses off: the row leaves the index.
	notification := row
	notification.LoginEnabled, notification.RecoveryEnabled = false, false
	applyIdentifierUpdate(t, db, row, notification)

	if _, ok := authClaimOwner(t, db, emailKind, address); ok {
		t.Error("the authentication claim survived a transition to notification-only")
	}
	if _, err := r.Identifiers.GetLogin(ctx, emailKind, address); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin after the claim was released: err=%v, want ErrNotFound", err)
	}

	// Recovery alone is enough to re-enter the index: the claim covers the
	// UNION of the two uses.
	recovery := notification
	recovery.RecoveryEnabled = true
	applyIdentifierUpdate(t, db, notification, recovery)

	claim, ok := authClaimOwner(t, db, emailKind, address)
	if !ok {
		t.Fatal("re-enabling recovery did not re-take the authentication claim")
	}
	if claim.IdentifierID != ident.ID {
		t.Errorf("claim holder = %q, want %q", claim.IdentifierID, ident.ID)
	}
	// The claim is back, but it covers the union — the specific use is checked
	// on the row, so login must still refuse.
	if _, err := r.Identifiers.GetLogin(ctx, emailKind, address); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin on a recovery-only identifier: err=%v, want ErrNotFound", err)
	}
	if got, err := r.Identifiers.GetRecovery(ctx, emailKind, address); err != nil || got.ID != ident.ID {
		t.Errorf("GetRecovery on a recovery-only identifier: id=%q err=%v, want %q", got.ID, err, ident.ID)
	}
}

// TestRetirementReleasesBothClaims walks the replaced_at conjunct, which is the
// LEADING term of both predicates: a retired row is out of both indexes at once,
// so its address becomes claimable and its (user, kind) primary slot becomes
// free. Retirement is history-preserving, so the row itself must still be
// readable by id.
func TestRetirementReleasesBothClaims(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const address = "retire@example.com"
	u, ident := seedUser(t, r, identifier.KindEmail, address,
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)

	row := readRow(t, db, ident.ID)
	applyIdentifierUpdate(t, db, row, row.retired(testBase.Add(time.Hour)))

	if _, ok := authClaimOwner(t, db, emailKind, address); ok {
		t.Error("a retired identifier kept its authentication claim — the address would never be claimable again")
	}
	if _, ok := primaryClaimOwner(t, db, u.ID, emailKind); ok {
		t.Error("a retired identifier kept its active-primary claim")
	}
	if got, err := r.Identifiers.Get(ctx, ident.ID); err != nil || got.Active() {
		t.Errorf("Get(retired) = %+v err=%v, want a present, inactive row", got, err)
	}

	// The freed address is claimable by a different subject, which is the whole
	// point of releasing the claim.
	other, _ := seedUser(t, r, identifier.KindEmail, "other@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)
	if _, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          other.ID,
		Kind:            identifier.KindEmail,
		NormalizedValue: address,
		LoginEnabled:    true,
	}, 0, testBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("claiming the address a retirement freed: %v", err)
	}
}

// TestPrimaryClaimMovesAtomically walks the is_primary conjunct: promoting a new
// primary must hand the (user, kind) claim over in ONE transaction, never
// delete-then-create it, and never leave both rows claiming to be primary.
func TestPrimaryClaimMovesAtomically(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, first := seedUser(t, r, identifier.KindEmail, "first@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)

	held, ok := primaryClaimOwner(t, db, u.ID, emailKind)
	if !ok || held.IdentifierID != first.ID {
		t.Fatalf("primary claim = %+v (found=%v), want %q", held, ok, first.ID)
	}

	second, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          u.ID,
		Kind:            identifier.KindEmail,
		NormalizedValue: "second@example.com",
		LoginEnabled:    true,
		MakePrimary:     true,
	}, 0, testBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange(promote): %v", err)
	}

	held, ok = primaryClaimOwner(t, db, u.ID, emailKind)
	if !ok || held.IdentifierID != second.ID {
		t.Fatalf("primary claim after promotion = %+v (found=%v), want %q", held, ok, second.ID)
	}
	// The displaced row is retired, so it holds neither claim any more.
	if _, ok := authClaimOwner(t, db, emailKind, "first@example.com"); ok {
		t.Error("the displaced primary kept its authentication claim")
	}
}

// TestNotificationOnlyTakesNoAuthenticationClaim is N-D1's shared-address rule at
// the document level: a contact-only address makes no authentication claim at
// all, which is what lets one household phone be notification-only on many
// accounts. It is asserted on the CLAIM rather than only through GetLogin,
// because a store that took the claim and then filtered it out on read would
// pass the port case and still refuse the second account.
func TestNotificationOnlyTakesNoAuthenticationClaim(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	const shared = "+15550001111"
	a, _ := seedUser(t, r, identifier.KindEmail, "a@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)
	b, _ := seedUser(t, r, identifier.KindEmail, "b@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, testBase, testBase)

	add := func(userID string) error {
		_, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
			UserID:              userID,
			Kind:                identifier.KindPhone,
			NormalizedValue:     shared,
			NotificationEnabled: true,
		}, 0, testBase.Add(time.Hour))
		return err
	}
	if err := add(a.ID); err != nil {
		t.Fatalf("notification-only phone for A: %v", err)
	}
	if err := add(b.ID); err != nil {
		t.Fatalf("notification-only phone for B (notification is not a claim): %v", err)
	}
	if _, ok := authClaimOwner(t, db, string(identifier.KindPhone), shared); ok {
		t.Error("a notification-only address took an authentication claim")
	}
}
