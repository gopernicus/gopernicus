//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitation "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
)

// THE CLAIM AUDITOR — the completeness gate the milestone's risk register asks
// for by name ("claim lifecycle gaps are the failure mode that produces a
// duplicate-looking store months later").
//
// Every other test in this package asserts ONE transition. This one asserts the
// INVARIANT that has to hold after any sequence of them: recompute all seven of
// SCHEMA.md §5's stored predicates from the raw row documents, and diff the
// result against the claim collections in BOTH directions.
//
//   - A MISSING claim is a row that satisfies the predicate and holds nothing:
//     the uniqueness rule it reproduces is not being enforced, so the store will
//     accept a duplicate login address, refresh hash or pending invitation.
//   - A STRANDED claim is a claim no row justifies: the key it holds can never
//     be used again by anybody, and the failure surfaces later as an
//     unexplainable sdk.ErrAlreadyExists.
//
// Neither is visible from any port — no port returns a claim — and neither
// breaks the operation that caused it. They break the NEXT one, arbitrarily far
// away, which is why this is a sweep rather than an assertion at a call site.
//
// The sweep below is scripted through the PORTS only, so it audits the store a
// host actually drives, and it deliberately walks the transitions where a claim
// moves rather than merely appears: replacement, promotion, demotion,
// retirement, rotation, grace consumption, deletion, deactivation and
// reactivation, resend, decline, challenge replacement, a wrong attempt, a
// purge, a token consumption, provisioning, adoption and revocation.

// claimPredicate is one claim collection's stored predicate, recomputed from the
// rows: given every row document of the owning collection, it returns the claim
// document ids that MUST exist.
type claimPredicate struct {
	collection string
	rows       string
	expected   func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string
}

// TestClaimsMatchTheStoredPredicatesAfterAFullLifecycleSweep is the audit.
func TestClaimsMatchTheStoredPredicatesAfterAFullLifecycleSweep(t *testing.T) {
	r, db := openRepos(t)
	sweepLifecycle(t, r, db)
	auditClaims(t, db)
}

// auditClaims recomputes every §5 predicate and diffs both ways.
func auditClaims(t *testing.T, db *firestoredb.DB) {
	t.Helper()

	for _, p := range claimPredicates() {
		rows := readAll(t, db, p.rows)
		want := p.expected(t, rows)
		have := map[string]bool{}
		for _, snap := range readAll(t, db, p.collection) {
			have[snap.Ref.ID] = true
		}

		for id, why := range want {
			if !have[id] {
				t.Errorf("MISSING claim %s/%s — %s satisfies the stored predicate and holds no claim, so its uniqueness rule is not enforced", p.collection, id, why)
			}
		}
		for id := range have {
			if _, justified := want[id]; !justified {
				t.Errorf("STRANDED claim %s/%s — no row justifies it, so the key it holds is unusable forever and the next legitimate use fails as a duplicate", p.collection, id)
			}
		}
		if len(want) == 0 && len(have) == 0 {
			t.Errorf("%s: the sweep left NO rows and NO claims — the audit would pass vacuously", p.collection)
		}
	}
}

// claimPredicates is SCHEMA.md §5, executable. Each entry recomputes a claim
// collection's contents from the rows that own it, using the row's STORED state
// — never a helper the write path also uses, so a helper that is wrong in one
// place is not wrong in both.
func claimPredicates() []claimPredicate {
	return []claimPredicate{
		{
			// §5.1 (kind, normalized_value) WHERE replaced_at IS NULL AND
			// (login_enabled OR recovery_enabled).
			collection: collectionIdentifierClaims,
			rows:       collectionIdentifiers,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[identifierDoc](t, snaps) {
					if row.Active && (row.LoginEnabled || row.RecoveryEnabled) {
						out[identifierClaimDocID(row.Kind, row.NormalizedValue)] = fmt.Sprintf("identifier %s (%s %s)", row.ID, row.Kind, row.NormalizedValue)
					}
				}
				return out
			},
		},
		{
			// §5.2 (user_id, kind) WHERE replaced_at IS NULL AND is_primary.
			collection: collectionIdentifierPrimaries,
			rows:       collectionIdentifiers,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[identifierDoc](t, snaps) {
					if row.Active && row.IsPrimary {
						out[identifierPrimaryDocID(row.UserID, row.Kind)] = fmt.Sprintf("identifier %s is user %s's active primary %s", row.ID, row.UserID, row.Kind)
					}
				}
				return out
			},
		},
		{
			// §5.3 the CURRENT refresh hash of every session. The grace slot
			// deliberately claims nothing.
			collection: collectionRefreshHashClaims,
			rows:       collectionSessions,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[sessionDoc](t, snaps) {
					out[refreshHashClaimDocID(row.RefreshTokenHash)] = "session " + row.ID
				}
				return out
			},
		},
		{
			// §5.4 unconditional: a revoked key keeps its hash reserved.
			collection: collectionAPIKeyHashClaims,
			rows:       collectionAPIKeys,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[apiKeyDoc](t, snaps) {
					out[apiKeyHashClaimDocID(row.KeyHash)] = "api key " + row.ID
				}
				return out
			},
		},
		{
			// §5.5 unconditional on the invitation's CURRENT token hash: a
			// resend re-points it, a status transition does not release it.
			collection: collectionInvitationTokens,
			rows:       collectionInvitations,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[invitationDoc](t, snaps) {
					out[invitationTokenClaimDocID(row.TokenHash)] = "invitation " + row.ID
				}
				return out
			},
		},
		{
			// §5.6 the five-column tuple WHERE status is pending or accepting —
			// never the read clock.
			collection: collectionInvitationPending,
			rows:       collectionInvitations,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[invitationDoc](t, snaps) {
					if row.Status != invitation.StatusPending && row.Status != invitation.StatusAccepting {
						continue
					}
					id := invitationPendingClaimDocID(row.ResourceType, row.ResourceID, row.IdentifierKind, row.Identifier, row.Relation)
					out[id] = fmt.Sprintf("invitation %s holds the active tuple", row.ID)
				}
				return out
			},
		},
		{
			// §5.7 (purpose, secret_digest) for every live challenge.
			collection: collectionChallengeDigests,
			rows:       collectionChallenges,
			expected: func(t *testing.T, snaps []*gcfs.DocumentSnapshot) map[string]string {
				out := map[string]string{}
				for _, row := range decodeAll[challengeDoc](t, snaps) {
					out[challengeDigestClaimDocID(row.Purpose, row.SecretDigest)] = "challenge " + row.ID
				}
				return out
			},
		},
	}
}

// sweepLifecycle drives the store through every transition in which a claim is
// taken, moved or released — through the PORTS, because the audit is about the
// store a host drives.
func sweepLifecycle(t *testing.T, r auth.Repositories, db *firestoredb.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	// CREATE: a subject with a verified, login-enabled primary email.
	subject, first := seedUser(t, r, identifier.KindEmail, "sweep-one@example.com", identifier.Uses{Login: true, Recovery: true}, true, now, now)

	// REPLACE + PROMOTE + RETIRE, in one atomic change: the new address takes
	// both claims and the replaced row releases both.
	second, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:               subject.ID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      "sweep-two@example.com",
		LoginEnabled:         true,
		RecoveryEnabled:      true,
		MakePrimary:          true,
		ReplacesIdentifierID: first.ID,
	}, subject.AuthRevision, now)
	if err != nil {
		t.Fatalf("ApplyVerifiedChange (replace): %v", err)
	}

	// A pure ADD of a second kind, primary for its own kind.
	phone, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          subject.ID,
		Kind:            identifier.KindPhone,
		NormalizedValue: "+15550100",
		RecoveryEnabled: true,
		MakePrimary:     true,
	}, subject.AuthRevision+1, now)
	if err != nil {
		t.Fatalf("ApplyVerifiedChange (add phone): %v", err)
	}

	// DEMOTE the phone out of the authentication predicate entirely: a
	// notification-only row holds NO auth claim, and it stays primary, so the
	// primary claim must survive the same write that releases the other.
	if err := r.CredentialMutations.Apply(ctx, subject.ID, subject.AuthRevision+2, credential.ChangeIdentifierUses{
		IdentifierID: phone.ID,
		Uses:         credential.IdentifierUses{Notification: true},
		MakePrimary:  true,
	}); err != nil {
		t.Fatalf("Apply (change uses): %v", err)
	}

	// A third email, then RETIRE the projected primary and promote the third in
	// the same mutation.
	third, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:          subject.ID,
		Kind:            identifier.KindEmail,
		NormalizedValue: "sweep-three@example.com",
		LoginEnabled:    true,
	}, subject.AuthRevision+3, now)
	if err != nil {
		t.Fatalf("ApplyVerifiedChange (add third): %v", err)
	}
	if err := r.CredentialMutations.Apply(ctx, subject.ID, subject.AuthRevision+4, credential.RetireIdentifier{
		IdentifierID:         second.ID,
		ReplacementPrimaryID: third.ID,
	}); err != nil {
		t.Fatalf("Apply (retire + promote): %v", err)
	}

	// SESSIONS: create, ROTATE (the claim moves), CONSUME the grace slot (the
	// claim stays), DELETE one, and leave one alive.
	live := newSweepSession(t, r, subject.ID, "sweep-refresh-a", now)
	if err := r.Sessions.Rotate(ctx, live.ID, "sweep-refresh-a", "sweep-refresh-b"); err != nil {
		t.Fatalf("Sessions.Rotate: %v", err)
	}
	if err := r.Sessions.ConsumeGrace(ctx, live.ID, "sweep-refresh-a"); err != nil {
		t.Fatalf("Sessions.ConsumeGrace: %v", err)
	}
	doomed := newSweepSession(t, r, subject.ID, "sweep-refresh-c", now)
	if err := r.Sessions.Delete(ctx, doomed.ID); err != nil {
		t.Fatalf("Sessions.Delete: %v", err)
	}

	// GRANTS on the surviving session, then a session-scoped revocation.
	grant, err := r.AuthenticationGrants.Create(ctx, authgrant.Grant{
		SessionID: live.ID, UserID: subject.ID, Purpose: "set_password", ContextDigest: "ctx",
		Assurance: session.AssuranceAAL1, AuthenticatedAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, subject.AuthRevision+5, now)
	if err != nil {
		t.Fatalf("AuthenticationGrants.Create: %v", err)
	}
	if _, err := r.AuthenticationGrants.Consume(ctx, authgrant.Requirement{UserID: grant.UserID, SessionID: grant.SessionID, Purpose: grant.Purpose, ContextDigest: grant.ContextDigest, MinAssurance: session.AssuranceAAL1, AuthenticatedAfter: now.Add(-time.Hour)}, now); err != nil {
		t.Fatalf("AuthenticationGrants.Consume: %v", err)
	}

	// OAUTH: link and unlink (no claims of its own — a control for the audit's
	// stranded direction).
	if _, err := r.OAuthAccounts.Create(ctx, oauthaccount.OAuthAccount{
		Provider: "google", ProviderUserID: "sweep-google", UserID: subject.ID, LinkedAt: now,
	}); err != nil {
		t.Fatalf("OAuthAccounts.Create: %v", err)
	}
	if err := r.CredentialMutations.Apply(ctx, subject.ID, subject.AuthRevision+5, credential.UnlinkOAuth{Provider: "google"}); err != nil {
		t.Fatalf("Apply (unlink): %v", err)
	}

	// DEACTIVATE (which revokes every session and grant, releasing every refresh
	// claim) and REACTIVATE, then mint again so the collection is not empty.
	if _, err := r.UserAdmin.SetStatus(ctx, subject.ID, user.StatusDeactivated, now); err != nil {
		t.Fatalf("SetStatus(deactivated): %v", err)
	}
	if _, err := r.UserAdmin.SetStatus(ctx, subject.ID, user.StatusActive, now); err != nil {
		t.Fatalf("SetStatus(active): %v", err)
	}
	revived, _ := session.NewSession(subject.ID, time.Hour, now)
	revived.RefreshTokenHash = "sweep-refresh-revived"
	if _, err := r.ActiveSessions.CreateForActiveUser(ctx, revived, subject.AuthRevision+8); err != nil {
		t.Fatalf("CreateForActiveUser: %v", err)
	}

	// API KEYS: create two, revoke one — the hash claim is unconditional, so
	// BOTH must still hold theirs.
	sa := newSweepServiceAccount(t, r, now)
	kept := createKey(t, r, sa, "kept", "sweep-key-kept", now)
	revoked := createKey(t, r, sa, "revoked", "sweep-key-revoked", now)
	if err := r.APIKeys.Revoke(ctx, revoked.ID, now); err != nil {
		t.Fatalf("APIKeys.Revoke: %v", err)
	}
	_ = kept

	// INVITATIONS: create, RESEND (the token claim re-points), DECLINE (the
	// pending claim is released, the token claim is NOT), and leave one pending.
	resent := newSweepInvitation(t, r, "member", "sweep-invitee@example.com", "sweep-token-a", now)
	if _, err := r.Invitations.UpdateStatus(ctx, resent.ID, invitation.StatusUpdate{
		ExpectedTokenHash: resent.TokenHash,
		Status:            invitation.StatusPending, TokenHash: "sweep-token-a2",
		ExpiresAt: now.Add(48 * time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Invitations.UpdateStatus (resend): %v", err)
	}
	declined := newSweepInvitation(t, r, "viewer", "sweep-declined@example.com", "sweep-token-b", now)
	if _, err := r.Invitations.UpdateStatus(ctx, declined.ID, invitation.StatusUpdate{
		ExpectedTokenHash: declined.TokenHash,
		Status:            invitation.StatusDeclined, TokenHash: declined.TokenHash,
		ExpiresAt: declined.ExpiresAt, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Invitations.UpdateStatus (decline): %v", err)
	}
	newSweepInvitation(t, r, "admin", "sweep-pending@example.com", "sweep-token-c", now)

	for _, state := range []string{"accepting", "accepted"} {
		inv := newSweepInvitation(t, r, "member", "sweep-"+state+"@example.com", "sweep-token-"+state, now)
		claim := invitation.Acceptance{TokenHash: inv.TokenHash, SubjectType: "user", SubjectID: subject.ID, Now: now}
		if _, err := r.Invitations.ClaimAcceptance(ctx, inv.ID, claim); err != nil {
			t.Fatal(err)
		}
		if state == "accepted" {
			if _, err := r.Invitations.CompleteAcceptance(ctx, inv.ID, claim); err != nil {
				t.Fatal(err)
			}
		}
	}

	// CHALLENGES: replacement (the displaced digest claim is released), a wrong
	// attempt (the row survives with its claim), a token consumption, and a
	// purge of an expired row.
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject.ID, challenge.PurposeLoginOTP, "k1", "sweep-otp-old", nil, 0, time.Hour, now)); err != nil {
		t.Fatalf("Challenges.Replace (first): %v", err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject.ID, challenge.PurposeLoginOTP, "k1", "sweep-otp-live", nil, 0, time.Hour, now)); err != nil {
		t.Fatalf("Challenges.Replace (replacement): %v", err)
	}
	if _, outcome, err := r.Challenges.ConsumeCode(ctx, subject.ID, challenge.PurposeLoginOTP,
		[]challenge.DigestCandidate{{KeyID: "k1", Digest: "sweep-otp-wrong"}}, "", 5, now); err != nil || outcome != challenge.OutcomeRejected {
		t.Fatalf("ConsumeCode (wrong attempt) = %v, %v", outcome, err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject.ID, challenge.PurposePasswordReset, "k1", "sweep-consumed", nil, 0, time.Hour, now)); err != nil {
		t.Fatalf("Challenges.Replace (consumed): %v", err)
	}
	if _, err := r.Challenges.ConsumeToken(ctx, challenge.PurposePasswordReset, "sweep-consumed", now); err != nil {
		t.Fatalf("Challenges.ConsumeToken: %v", err)
	}
	if _, err := r.Challenges.Replace(ctx, challengeFixture(subject.ID, challenge.PurposeVerifyRegistration, "k1", "sweep-expired", nil, 0, -time.Hour, now)); err != nil {
		t.Fatalf("Challenges.Replace (expired): %v", err)
	}
	purged, err := r.Challenges.PurgeExpired(ctx, now, 0)
	if err != nil {
		t.Fatalf("Challenges.PurgeExpired: %v", err)
	}
	if purged != 1 {
		t.Fatalf("PurgeExpired removed %d rows, want the one expired row", purged)
	}

	// PROVISION and ADOPT, the two redemptions that write a whole subject.
	provisioned := redeemSweepLink(t, r, "sweep-provision@example.com", "sweep-digest-provision", "sweep-refresh-provisioned", now, false)
	if provisioned.Outcome != passwordlessProvisioned {
		t.Fatalf("provision outcome = %q", provisioned.Outcome)
	}
	adopted := redeemSweepLink(t, r, "sweep-adopt@example.com", "sweep-digest-adopt", "sweep-refresh-adopted", now, true)
	if adopted.Outcome != passwordlessAdopted {
		t.Fatalf("adopt outcome = %q", adopted.Outcome)
	}

	// A final control: the audit must see rows in every collection it checks.
	for _, collection := range []string{
		collectionIdentifiers, collectionSessions, collectionAPIKeys, collectionInvitations, collectionChallenges,
	} {
		if len(readAll(t, db, collection)) == 0 {
			t.Fatalf("the sweep left %s empty — the audit would pass vacuously on it", collection)
		}
	}
}

// The two redemption outcomes the sweep asserts, named so the sweep reads as
// prose.
const (
	passwordlessProvisioned = "provision_new"
	passwordlessAdopted     = "verify_and_adopt_existing_unverified"
)

// newSweepSession creates one session through the port.
func newSweepSession(t *testing.T, r auth.Repositories, userID, hash string, now time.Time) session.Session {
	t.Helper()
	sess, _ := session.NewSession(userID, time.Hour, now)
	sess.RefreshTokenHash = hash
	created, err := r.Sessions.Create(context.Background(), sess)
	if err != nil {
		t.Fatalf("Sessions.Create(%q): %v", hash, err)
	}
	return created
}

// newSweepServiceAccount creates the parent the API keys hang off.
func newSweepServiceAccount(t *testing.T, r auth.Repositories, now time.Time) string {
	t.Helper()
	sa, err := serviceaccount.New(dbGenerated, "sweep-service-account", "audit sweep", "operator-1", false, "", now)
	if err != nil {
		t.Fatalf("serviceaccount.New: %v", err)
	}
	created, err := r.ServiceAccounts.Create(context.Background(), sa)
	if err != nil {
		t.Fatalf("ServiceAccounts.Create: %v", err)
	}
	return created.ID
}

// newSweepInvitation creates one pending invitation through the port.
func newSweepInvitation(t *testing.T, r auth.Repositories, relation, address, tokenHash string, now time.Time) invitation.Invitation {
	t.Helper()
	inv := invitationFixture(t, relation, address, string(identifier.KindEmail), tokenHash, 48*time.Hour, now)
	created, err := r.Invitations.Create(context.Background(), inv)
	if err != nil {
		t.Fatalf("Invitations.Create(%q): %v", tokenHash, err)
	}
	return created
}

// redeemSweepLink drives one magic-link redemption. squat seeds an unverified
// claim on the address first, which is what turns a provisioning redemption into
// an ADOPTION.
func redeemSweepLink(t *testing.T, r auth.Repositories, address, digest, refreshHash string, now time.Time, squat bool) (res sweepRedemption) {
	t.Helper()
	ctx := context.Background()

	binding := provisioningBinding(address)
	if squat {
		u := user.NewUser(dbGenerated, "Squatter", now)
		ident, err := identifier.NewRegistrationEmail(dbGenerated, normalizer, "", address, now)
		if err != nil {
			t.Fatalf("NewRegistrationEmail: %v", err)
		}
		squatter, squatterIdent, err := r.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
		if err != nil {
			t.Fatalf("CreateWithPrimaryIdentifier (squatter): %v", err)
		}
		newSweepSession(t, r, squatter.ID, "sweep-refresh-squatter", now)
		binding.UserID, binding.IdentifierID = squatter.ID, squatterIdent.ID
		revision := revisionOf(t, r, squatter.ID)
		binding.AuthRevision = &revision
	}

	link := seedLink(t, r, digest, binding, now.Add(15*time.Minute))
	out, err := r.Passwordless.Redeem(ctx, redeemInput(link.digest, refreshHash, now))
	if err != nil {
		t.Fatalf("Passwordless.Redeem(%q): %v", digest, err)
	}
	return sweepRedemption{Outcome: string(out.Outcome)}
}

// sweepRedemption is the only part of a redemption result the sweep asserts.
type sweepRedemption struct{ Outcome string }

// readAll reads every document of a collection. The audit reads RAW: it must not
// share a query builder with the write path it is auditing.
func readAll(t *testing.T, db *firestoredb.DB, collection string) []*gcfs.DocumentSnapshot {
	t.Helper()
	ctx := context.Background()

	it := db.ReaderFrom(ctx).Documents(ctx, db.Collection(collection).Query)
	defer it.Stop()

	var out []*gcfs.DocumentSnapshot
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			slices.SortFunc(out, func(a, b *gcfs.DocumentSnapshot) int {
				switch {
				case a.Ref.ID < b.Ref.ID:
					return -1
				case a.Ref.ID > b.Ref.ID:
					return 1
				}
				return 0
			})
			return out
		}
		if err != nil {
			t.Fatalf("reading %s: %v", collection, err)
		}
		out = append(out, snap)
	}
}

// decodeAll decodes a collection's snapshots into their row shape.
func decodeAll[T any](t *testing.T, snaps []*gcfs.DocumentSnapshot) []T {
	t.Helper()
	out := make([]T, 0, len(snaps))
	for _, snap := range snaps {
		var row T
		if err := snap.DataTo(&row); err != nil {
			t.Fatalf("decoding %s: %v", snap.Ref.Path, err)
		}
		out = append(out, row)
	}
	return out
}
