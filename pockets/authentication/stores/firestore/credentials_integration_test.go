//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/sdk"
)

// The credential-mutation cases the shared conformance suite does not reach.
//
// The suite's CredentialMutations group drives only RemovePassword and
// UnlinkOAuth — the two kinds that touch no identifier row. The other two,
// RetireIdentifier and ChangeIdentifierUses, are the ones that move the
// authentication and active-primary CLAIM documents and the directory
// projection, which is precisely the machinery Firestore has to reproduce by
// hand and the place a silent duplicate is born (SCHEMA.md §5.1, §5.2, §6.3 rows
// 6–9). Every assertion below is on the STORED documents.

// notifyOnly is a contact-only role set: it makes no authentication claim, so
// several subjects may hold the same address.
var notifyOnly = identifier.Uses{Notification: true}

// addIdentifier adds a verified identifier to an existing user through the
// identifier port, returning the new row. The port advances auth_revision, so
// the caller passes the revision it expects and reads the next one back from a
// Snapshot.
func addIdentifier(t *testing.T, r auth.Repositories, userID, value string, uses identifier.Uses, primary bool, expectedRevision int64) identifier.Identifier {
	t.Helper()
	added, err := r.Identifiers.ApplyVerifiedChange(context.Background(), identifier.ApplyVerifiedChangeInput{
		UserID:              userID,
		Kind:                identifier.KindEmail,
		NormalizedValue:     value,
		LoginEnabled:        uses.Login,
		RecoveryEnabled:     uses.Recovery,
		NotificationEnabled: uses.Notification,
		MakePrimary:         primary,
	}, expectedRevision, testBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange(%q): %v", value, err)
	}
	return added
}

// revisionOf reads the user's current auth_revision through the port that owns
// it.
func revisionOf(t *testing.T, r auth.Repositories, userID string) int64 {
	t.Helper()
	set, err := r.CredentialMutations.Snapshot(context.Background(), userID)
	if err != nil {
		t.Fatalf("Snapshot(%q): %v", userID, err)
	}
	return set.AuthRevision
}

// TestApplyRetireIdentifierPromotesTheNamedReplacement is SCHEMA §6.3 row 7. The
// retired row releases BOTH of its claims, the named replacement takes the
// active-primary claim in the same commit, and the projection follows the
// promotion rather than the retirement.
func TestApplyRetireIdentifierPromotesTheNamedReplacement(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, first := seedUser(t, r, identifier.KindEmail, "first@example.com", livePrimaryUses, true, testBase, testBase)
	second := addIdentifier(t, r, u.ID, "second@example.com", livePrimaryUses, false, 0)

	if err := r.CredentialMutations.Apply(ctx, u.ID, revisionOf(t, r, u.ID), credential.RetireIdentifier{
		IdentifierID:         first.ID,
		ReplacementPrimaryID: second.ID,
	}); err != nil {
		t.Fatalf("Apply(RetireIdentifier): %v", err)
	}

	// The retired row is history, not a delete, and it holds nothing.
	retired := readRow(t, db, first.ID)
	if retired.Active || retired.claimsAuth() || retired.claimsPrimary() {
		t.Errorf("retired row = %+v, want inactive and claiming nothing", retired)
	}
	if _, ok := authClaimOwner(t, db, string(identifier.KindEmail), "first@example.com"); ok {
		t.Error("the retired address still holds its authentication claim")
	}
	// The freed address must be claimable by another subject; a leaked claim
	// refuses it forever and no port reports why.
	seedUser(t, r, identifier.KindEmail, "first@example.com", livePrimaryUses, true, testBase, testBase)

	claim, ok := primaryClaimOwner(t, db, u.ID, string(identifier.KindEmail))
	if !ok || claim.IdentifierID != second.ID {
		t.Errorf("primary claim = %+v ok=%v, want the promoted replacement %q", claim, ok, second.ID)
	}
	if promoted := readRow(t, db, second.ID); !promoted.IsPrimary || !promoted.Active {
		t.Errorf("promoted row = %+v, want active and primary", promoted)
	}
	if email, verified := storedProjection(t, db, u.ID); email != "second@example.com" || !verified {
		t.Errorf("projection = (%q, %v), want the promoted address, verified", email, verified)
	}
}

// TestApplyRetireIdentifierWithoutReplacementClearsTheProjection is SCHEMA §6.3
// row 6, and it is the case a store passes accidentally by only ever WRITING the
// projection when it has an address to write: retiring the sole primary email
// must CLEAR both fields, leaving the subject in the directory with no address
// on file.
func TestApplyRetireIdentifierWithoutReplacementClearsTheProjection(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, only := seedUser(t, r, identifier.KindEmail, "sole@example.com", livePrimaryUses, true, testBase, testBase)

	if err := r.CredentialMutations.Apply(ctx, u.ID, 0, credential.RetireIdentifier{IdentifierID: only.ID}); err != nil {
		t.Fatalf("Apply(RetireIdentifier): %v", err)
	}

	if email, verified := storedProjection(t, db, u.ID); email != "" || verified {
		t.Errorf("projection = (%q, %v), want cleared", email, verified)
	}
	if _, ok := primaryClaimOwner(t, db, u.ID, string(identifier.KindEmail)); ok {
		t.Error("the retired row still holds the active-primary claim")
	}
	summary, err := r.UserAdmin.GetSummary(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary.PrimaryEmail != "" || summary.EmailVerified {
		t.Errorf("directory summary = %+v, want no address on file", summary)
	}
}

// TestApplyChangeIdentifierUsesCrossesTheClaimPredicate walks the authentication
// claim's stored predicate in both directions through the port. The claim covers
// the UNION of login and recovery, so enabling either takes it and dropping both
// releases it — and a notification-only address holds nothing, which is what
// lets a shared household address stay contactable on many accounts.
func TestApplyChangeIdentifierUsesCrossesTheClaimPredicate(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, _ := seedUser(t, r, identifier.KindEmail, "primary@example.com", livePrimaryUses, true, testBase, testBase)
	shared := addIdentifier(t, r, u.ID, "shared@example.com", notifyOnly, false, 0)

	if _, ok := authClaimOwner(t, db, string(identifier.KindEmail), "shared@example.com"); ok {
		t.Fatal("a notification-only address took an authentication claim")
	}

	// Recovery ALONE takes the claim.
	if err := r.CredentialMutations.Apply(ctx, u.ID, revisionOf(t, r, u.ID), credential.ChangeIdentifierUses{
		IdentifierID: shared.ID,
		Uses:         credential.IdentifierUses{Recovery: true, Notification: true},
	}); err != nil {
		t.Fatalf("Apply(enable recovery): %v", err)
	}
	claim, ok := authClaimOwner(t, db, string(identifier.KindEmail), "shared@example.com")
	if !ok || claim.IdentifierID != shared.ID {
		t.Fatalf("claim after enabling recovery = %+v ok=%v, want the row", claim, ok)
	}
	// The claim covers the union; the specific use is on the row, so a LOGIN
	// lookup must still refuse.
	if _, err := r.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "shared@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin on a recovery-only address: err=%v, want ErrNotFound", err)
	}
	if _, err := r.Identifiers.GetRecovery(ctx, string(identifier.KindEmail), "shared@example.com"); err != nil {
		t.Errorf("GetRecovery on a recovery-enabled address: %v", err)
	}

	// Dropping both uses RELEASES it, and the address becomes claimable again.
	if err := r.CredentialMutations.Apply(ctx, u.ID, revisionOf(t, r, u.ID), credential.ChangeIdentifierUses{
		IdentifierID: shared.ID,
		Uses:         credential.IdentifierUses{Notification: true},
	}); err != nil {
		t.Fatalf("Apply(back to notification-only): %v", err)
	}
	if _, ok := authClaimOwner(t, db, string(identifier.KindEmail), "shared@example.com"); ok {
		t.Error("the claim survived the row leaving the predicate")
	}
	seedUser(t, r, identifier.KindEmail, "shared@example.com", livePrimaryUses, true, testBase, testBase)
}

// TestApplyChangeIdentifierUsesPromotionDemotesAndReprojects is SCHEMA §6.3
// row 8. Promotion is a HAND-OVER of one claim document: the displaced row is
// demoted (not retired — it keeps its own authentication claim), the promoted
// row takes the (user, kind) primary claim, and the projection is recomputed
// from both.
func TestApplyChangeIdentifierUsesPromotionDemotesAndReprojects(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, first := seedUser(t, r, identifier.KindEmail, "old@example.com", livePrimaryUses, true, testBase, testBase)
	second := addIdentifier(t, r, u.ID, "new@example.com", notifyOnly, false, 0)

	if err := r.CredentialMutations.Apply(ctx, u.ID, revisionOf(t, r, u.ID), credential.ChangeIdentifierUses{
		IdentifierID: second.ID,
		Uses:         credential.IdentifierUses{Login: true, Recovery: true, Notification: true},
		MakePrimary:  true,
	}); err != nil {
		t.Fatalf("Apply(promote): %v", err)
	}

	demoted := readRow(t, db, first.ID)
	if !demoted.Active || demoted.IsPrimary {
		t.Errorf("displaced row = %+v, want active and no longer primary", demoted)
	}
	// Demotion is not retirement: the old address is still a login credential.
	if owner, ok := authClaimOwner(t, db, string(identifier.KindEmail), "old@example.com"); !ok || owner.IdentifierID != first.ID {
		t.Errorf("the demoted row lost its authentication claim: %+v ok=%v", owner, ok)
	}
	claim, ok := primaryClaimOwner(t, db, u.ID, string(identifier.KindEmail))
	if !ok || claim.IdentifierID != second.ID {
		t.Errorf("primary claim = %+v ok=%v, want the promoted row %q", claim, ok, second.ID)
	}
	if email, verified := storedProjection(t, db, u.ID); email != "new@example.com" || !verified {
		t.Errorf("projection = (%q, %v), want the promoted address, verified", email, verified)
	}
}

// TestApplyIntoAClaimedAddressRollsBackWhole is ruling R3 seen from the
// credential rail: enabling login on an address ANOTHER subject already
// authenticates with must lose at the server and leave nothing behind — not the
// use change, and not the revision increment, which the caller would otherwise
// have to reconcile against a mutation that never happened.
func TestApplyIntoAClaimedAddressRollsBackWhole(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	holder, _ := seedUser(t, r, identifier.KindEmail, "contested@example.com", livePrimaryUses, true, testBase, testBase)
	challenger, _ := seedUser(t, r, identifier.KindEmail, "challenger@example.com", livePrimaryUses, true, testBase, testBase)
	// The same address, notification-only: legal, and holding no claim.
	shared := addIdentifier(t, r, challenger.ID, "contested@example.com", notifyOnly, false, 0)

	err := r.CredentialMutations.Apply(ctx, challenger.ID, revisionOf(t, r, challenger.ID), credential.ChangeIdentifierUses{
		IdentifierID: shared.ID,
		Uses:         credential.IdentifierUses{Login: true, Notification: true},
	})
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("Apply into a claimed address: err=%v, want ErrAlreadyExists", err)
	}

	if row := readRow(t, db, shared.ID); row.LoginEnabled {
		t.Error("the losing mutation was applied anyway")
	}
	if rev := revisionOf(t, r, challenger.ID); rev != 1 {
		t.Errorf("auth_revision = %d after a rolled-back mutation, want the pre-mutation 1", rev)
	}
	owner, ok := authClaimOwner(t, db, string(identifier.KindEmail), "contested@example.com")
	if !ok {
		t.Fatal("the contested claim vanished")
	}
	if held := readRow(t, db, owner.IdentifierID); held.UserID != holder.ID {
		t.Errorf("the contested claim now belongs to %q, want the original holder %q", held.UserID, holder.ID)
	}
}

// TestApplyPasswordAndOAuthKindsLeaveTheProjectionAlone is SCHEMA §6.3 row 9 —
// the audited no-op. Both kinds bump auth_revision through the SAME field update
// that carries the projection, so a store that passed the zero value there would
// blank the operator directory on a password removal and nothing but a
// directory read would ever notice.
func TestApplyPasswordAndOAuthKindsLeaveTheProjectionAlone(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, _ := seedUser(t, r, identifier.KindEmail, "credentials@example.com", livePrimaryUses, true, testBase, testBase)
	if err := r.Passwords.Set(ctx, u.ID, "hash"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	link, err := oauthaccount.New(u.ID, "google", "google-subject", testBase)
	if err != nil {
		t.Fatalf("oauthaccount.New: %v", err)
	}
	if _, err := r.OAuthAccounts.Create(ctx, link); err != nil {
		t.Fatalf("OAuthAccounts.Create: %v", err)
	}

	if err := r.CredentialMutations.Apply(ctx, u.ID, 1, credential.RemovePassword{}); err != nil {
		t.Fatalf("Apply(RemovePassword): %v", err)
	}
	if email, verified := storedProjection(t, db, u.ID); email != "credentials@example.com" || !verified {
		t.Errorf("projection after RemovePassword = (%q, %v), want it untouched", email, verified)
	}

	if err := r.CredentialMutations.Apply(ctx, u.ID, 2, credential.UnlinkOAuth{Provider: "google"}); err != nil {
		t.Fatalf("Apply(UnlinkOAuth): %v", err)
	}
	if email, verified := storedProjection(t, db, u.ID); email != "credentials@example.com" || !verified {
		t.Errorf("projection after UnlinkOAuth = (%q, %v), want it untouched", email, verified)
	}

	set, err := r.CredentialMutations.Snapshot(ctx, u.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if set.HasPassword || len(set.OAuth) != 0 || set.AuthRevision != 3 {
		t.Errorf("snapshot = %+v, want no password, no links, revision 3", set)
	}
	// Absent targets are successful no-ops that still advance the revision, the
	// same answer the SQL adapters' zero-row DELETE gives.
	if err := r.CredentialMutations.Apply(ctx, u.ID, 3, credential.UnlinkOAuth{Provider: "google"}); err != nil {
		t.Errorf("Apply(unlink an absent provider): %v, want success", err)
	}
	if rev := revisionOf(t, r, u.ID); rev != 4 {
		t.Errorf("auth_revision = %d after a no-op mutation, want 4", rev)
	}
}

// TestApplyRollsBackWholeAndRetriesClean drives the REAL attempt function
// through this store's real contention loop with a genuine Aborted status ending
// attempt one, and asserts the two properties the port surface cannot see: the
// aborted attempt wrote NOTHING (the password it queued for deletion is still
// there, at the unchanged revision), and the retry applies the mutation exactly
// once rather than compounding the increment.
func TestApplyRollsBackWholeAndRetriesClean(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	u, _ := seedUser(t, r, identifier.KindEmail, "retry@example.com", livePrimaryUses, true, testBase, testBase)
	if err := r.Passwords.Set(ctx, u.ID, "hash"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}

	now := time.Now()
	sess := seedSession(t, r, u.ID, "mutation-rollback-refresh", time.Hour)
	grant := newTestGrant(sess.ID, u.ID, "set_password", "ctx", time.Hour, now)
	if _, err := r.AuthenticationGrants.Create(ctx, grant, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(u.ID, challenge.PurposePasswordReset, "", "mutation-rollback-proof", nil, 0, time.Hour, now)); err != nil {
		t.Fatal(err)
	}
	assertRevocations := func(wantPresent bool) {
		t.Helper()
		rows, grants, claims := storedRevocationState(t, db, u.ID, []string{sess.RefreshTokenHash})
		want := 0
		if wantPresent {
			want = 1
		}
		if rows != want || grants != want || claims != want {
			t.Errorf("revocation state sessions=%d grants=%d claims=%d; want %d each", rows, grants, claims, want)
		}
		_, rowPresent := storedChallenge(t, db, u.ID, challenge.PurposePasswordReset)
		_, claimPresent := digestClaimHolder(t, db, challenge.PurposePasswordReset, "mutation-rollback-proof")
		if rowPresent != wantPresent || claimPresent != wantPresent {
			t.Errorf("reset proof row=%v claim=%v; want %v", rowPresent, claimPresent, wantPresent)
		}
	}
	assertRevocations(true)
	mutations := newCredentialMutationStore(db)

	attempts, err := retryWithInjectedAbort(ctx, db,
		func() {
			assertRevocations(true)
			set, err := r.CredentialMutations.Snapshot(ctx, u.ID)
			if err != nil {
				t.Errorf("Snapshot between attempts: %v", err)
				return
			}
			if !set.HasPassword || set.AuthRevision != 1 {
				t.Errorf("the rolled-back attempt left %+v; want the password intact at revision 1", set)
			}
		},
		func(ctx context.Context) error {
			return mutations.apply(ctx, u.ID, 1, credential.RemovePassword{}, testBase.Add(time.Hour))
		},
	)
	if err != nil {
		t.Fatalf("the retried Apply must succeed: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the injected Aborted must re-run the callback)", attempts)
	}

	assertRevocations(false)
	set, err := r.CredentialMutations.Snapshot(ctx, u.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if set.HasPassword || set.AuthRevision != 2 {
		t.Errorf("after the retry: %+v, want no password at exactly revision 2", set)
	}
}

func TestApplyRejectsForeignIdentifierTargetsWithoutChanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutation func(own, foreign string) credential.Mutation
	}{
		{"retire foreign", func(_, foreign string) credential.Mutation { return credential.RetireIdentifier{IdentifierID: foreign} }},
		{"promote foreign replacement", func(own, foreign string) credential.Mutation {
			return credential.RetireIdentifier{IdentifierID: own, ReplacementPrimaryID: foreign}
		}},
		{"change foreign uses", func(_, foreign string) credential.Mutation {
			return credential.ChangeIdentifierUses{IdentifierID: foreign, Uses: credential.IdentifierUses{Notification: true}, MakePrimary: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := openRepos(t)
			acting, own := seedUser(t, r, identifier.KindEmail, "acting@example.com", livePrimaryUses, true, testBase, testBase)
			other, foreign := seedUser(t, r, identifier.KindEmail, "foreign@example.com", livePrimaryUses, true, testBase, testBase)
			beforeOwn, beforeForeign := readRow(t, db, own.ID), readRow(t, db, foreign.ID)
			if err := r.CredentialMutations.Apply(t.Context(), acting.ID, acting.AuthRevision, tc.mutation(own.ID, foreign.ID)); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Apply: %v, want ErrInvalidInput", err)
			}
			if got := readRow(t, db, own.ID); !reflect.DeepEqual(got, beforeOwn) {
				t.Error("acting user's identifier changed")
			}
			if got := readRow(t, db, foreign.ID); !reflect.DeepEqual(got, beforeForeign) {
				t.Error("foreign user's identifier changed")
			}
			for _, owner := range []user.User{acting, other} {
				if got := revisionOf(t, r, owner.ID); got != owner.AuthRevision {
					t.Errorf("owner revision changed: %d", got)
				}
				want := "acting@example.com"
				if owner.ID == other.ID {
					want = "foreign@example.com"
				}
				if got, verified := storedProjection(t, db, owner.ID); got != want || !verified {
					t.Errorf("projection = %q verified=%v; want unchanged %q", got, verified, want)
				}
				claim, ok := authClaimOwner(t, db, string(identifier.KindEmail), want)
				if !ok || (owner.ID == acting.ID && claim.IdentifierID != own.ID) || (owner.ID == other.ID && claim.IdentifierID != foreign.ID) {
					t.Error("authentication claim changed")
				}
			}
		})
	}
}

func TestApplyRejectsNilMutationsAndAcceptsPointerVariants(t *testing.T) {
	r, _ := openRepos(t)
	owner, _ := seedUser(t, r, identifier.KindEmail, "nil-mutation@example.com", livePrimaryUses, true, testBase, testBase)
	if err := r.Passwords.Set(t.Context(), owner.ID, "keep"); err != nil {
		t.Fatal(err)
	}
	revision := revisionOf(t, r, owner.ID)
	for _, mutation := range []credential.Mutation{nil, (*credential.RemovePassword)(nil), (*credential.UnlinkOAuth)(nil), (*credential.RetireIdentifier)(nil), (*credential.ChangeIdentifierUses)(nil)} {
		if err := r.CredentialMutations.Apply(t.Context(), owner.ID, revision, mutation); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("Apply(%T)=%v; want ErrInvalidInput", mutation, err)
		}
		if got := revisionOf(t, r, owner.ID); got != revision {
			t.Errorf("invalid mutation advanced revision to %d", got)
		}
		if hash, err := r.Passwords.Get(t.Context(), owner.ID); err != nil || hash != "keep" {
			t.Errorf("invalid mutation changed password: %v", err)
		}
	}
	if err := r.CredentialMutations.Apply(t.Context(), owner.ID, revision, &credential.RemovePassword{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Passwords.Get(t.Context(), owner.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("pointer mutation did not remove password: %v", err)
	}
	if got := revisionOf(t, r, owner.ID); got != revision+1 {
		t.Errorf("pointer mutation revision=%d; want %d", got, revision+1)
	}
}
