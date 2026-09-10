//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// THE CLAIM IS A PATH TO THE ANSWER; THE ROW IS THE ANSWER.
//
// Six read rails in this store resolve a secret — an address, a refresh hash, an
// API-key hash, an invitation token, a challenge digest — by reading a CLAIM
// document whose id is the KeyHash of that secret, then reading the row the
// claim names. In SQL the equivalent predicate is part of the query, so a row
// that stopped matching stops being returned no matter what else went wrong.
// Here the claim is an index entry the store maintains BY HAND, and a claim that
// ever out-lived or out-pointed its row would silently redirect a credential to
// somebody else's account: a login address resolving to the wrong subject, a
// refresh token resolving to the wrong session, a magic link consuming a
// password-reset challenge.
//
// So every claim-resolved read re-verifies the row against the claim's own
// predicate. These tests plant a DELIBERATELY MIS-POINTED claim document — the
// state a lifecycle bug would produce — and require every rail to answer
// sdk.ErrNotFound rather than the row the claim points at. Nothing here can be
// reached through a port: no port writes a claim, and no port returns one.

// plantClaim writes a claim document directly, bypassing every helper pair that
// owns one. That is the point: it manufactures the corrupt state a future
// lifecycle bug would leave behind, so the READ side can be held to its own
// standard independently of the write side.
func plantClaim(t *testing.T, db *firestoredb.DB, ref *gcfs.DocumentRef, data any) {
	t.Helper()
	ctx := context.Background()
	if err := db.WriterFrom(ctx).Set(ctx, ref, data); err != nil {
		t.Fatalf("planting the claim at %s: %v", ref.Path, err)
	}
}

// TestMisPointedAuthClaimResolvesNothing covers Identifiers.GetLogin and
// GetRecovery: a claim on an address the row does not carry must not hand that
// address's caller the row.
func TestMisPointedAuthClaimResolvesNothing(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	_, owned := seedUser(t, r, identifier.KindEmail, "owner@example.com", identifier.Uses{Login: true, Recovery: true}, true, testBase, testBase)

	// The corruption: "victim@example.com" is claimed, by the row that carries
	// "owner@example.com".
	const victim = "victim@example.com"
	plantClaim(t, db, authClaimRef(db, string(identifier.KindEmail), victim), identifierClaimDoc{
		DocID:        identifierDocID(owned.ID),
		IdentifierID: owned.ID,
		UserID:       owned.UserID,
	})

	if got, err := r.Identifiers.GetLogin(ctx, string(identifier.KindEmail), victim); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(%q) = %+v, %v — a mis-pointed claim logged the caller into %s", victim, got, err, got.UserID)
	}
	if got, err := r.Identifiers.GetRecovery(ctx, string(identifier.KindEmail), victim); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetRecovery(%q) = %+v, %v", victim, got, err)
	}

	// The legitimate lookup still works — the re-verification is a check, not a
	// second predicate.
	if _, err := r.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "owner@example.com"); err != nil {
		t.Errorf("GetLogin on the claimed address: %v", err)
	}
}

// TestRetiredRowBehindALiveAuthClaimResolvesNothing is the other half of the
// same predicate: the claim points at the RIGHT address, but the row has left
// the index's stored predicate (retired, or stripped of both authentication
// uses) and the release was lost.
func TestRetiredRowBehindALiveAuthClaimResolvesNothing(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const addr = "notify-only@example.com"
	_, owned := seedUser(t, r, identifier.KindEmail, addr, identifier.Uses{Notification: true}, true, testBase, testBase)

	// A notification-only row holds NO authentication claim (§5.1). Planting one
	// is exactly what a forgotten release looks like.
	plantClaim(t, db, authClaimRef(db, string(identifier.KindEmail), addr), identifierClaimDoc{
		DocID:        identifierDocID(owned.ID),
		IdentifierID: owned.ID,
		UserID:       owned.UserID,
	})

	if got, err := r.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(%q) = %+v, %v — a notification-only address must never authenticate", addr, got, err)
	}
}

// TestMisPointedPrimaryClaimProjectsNothing covers the claim the DIRECTORY
// PROJECTION is recomputed from. It is white-box because no port returns the
// resolution: what a port would show is the projection AFTER a writer trusted
// it, which is one step too late to attribute.
func TestMisPointedPrimaryClaimProjectsNothing(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	_, mine := seedUser(t, r, identifier.KindEmail, "mine@example.com", identifier.Uses{Login: true}, true, testBase, testBase)
	stranger, _ := seedUser(t, r, identifier.KindEmail, "stranger@example.com", identifier.Uses{Login: true}, true, testBase, testBase)

	// The stranger's (user, email) primary claim now names MY identifier.
	plantClaim(t, db, primaryClaimRef(db, stranger.ID, string(identifier.KindEmail)), identifierPrimaryDoc{
		DocID:        identifierDocID(mine.ID),
		IdentifierID: mine.ID,
	})

	row, found, err := readPrimaryIdentifier(ctx, db, db.ReaderFrom(ctx), stranger.ID, string(identifier.KindEmail))
	if err != nil {
		t.Fatalf("readPrimaryIdentifier: %v", err)
	}
	if found {
		t.Errorf("the stranger's primary resolved to %s (%s), which belongs to %s — the directory would publish another subject's address",
			row.ID, row.NormalizedValue, row.UserID)
	}
}

// TestMisPointedRefreshClaimResolvesNoSession covers Sessions.GetByRefreshHash,
// the rail where a mis-pointed claim is a session takeover: a refresh token
// presented by one caller resolving to another caller's session.
func TestMisPointedRefreshClaimResolvesNoSession(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	sess, _ := session.NewSession("u-refresh-verify", time.Hour, testBase)
	sess.RefreshTokenHash = "refresh-owned"
	created, err := r.Sessions.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}

	const stolen = "refresh-never-issued"
	plantClaim(t, db, refreshClaimRef(db, stolen), refreshHashClaimDoc{
		DocID:     sessionDocID(created.ID),
		SessionID: created.ID,
	})

	got, match, err := r.Sessions.GetByRefreshHash(ctx, stolen)
	if !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByRefreshHash(%q) = %+v (match=%v), %v — a hash the session never carried resolved to it", stolen, got, match, err)
	}
	if _, _, err := r.Sessions.GetByRefreshHash(ctx, "refresh-owned"); err != nil {
		t.Errorf("GetByRefreshHash on the real hash: %v", err)
	}
}

// TestMisPointedAPIKeyClaimResolvesNoKey covers APIKeys.GetByHash — the rail a
// machine credential authenticates through.
func TestMisPointedAPIKeyClaimResolvesNoKey(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	key := createKey(t, r, "sa-verify", "real", "hash-real", testBase)

	const forged = "hash-forged"
	plantClaim(t, db, apiKeyHashClaimRef(db, forged), apiKeyHashClaimDoc{
		DocID:    apiKeyDocID(key.ID),
		APIKeyID: key.ID,
	})

	if got, err := r.APIKeys.GetByHash(ctx, forged); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByHash(%q) = %+v, %v — a hash the key does not carry authenticated as %s", forged, got, err, got.ServiceAccountID)
	}
	if _, err := r.APIKeys.GetByHash(ctx, "hash-real"); err != nil {
		t.Errorf("GetByHash on the real hash: %v", err)
	}
}

// TestMisPointedInvitationClaimResolvesNoInvitation covers
// Invitations.GetByTokenHash: a mailed link is a bearer credential, and a
// mis-pointed claim would accept its holder into the wrong resource.
func TestMisPointedInvitationClaimResolvesNoInvitation(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	// A LIVE invitation: GetByTokenHash reports expiry against the wall clock,
	// so the fixture's window has to contain it.
	inv := invitationFixture(t, "member", "invitee@example.com", string(identifier.KindEmail), "token-real", time.Hour, time.Now())
	created, err := r.Invitations.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Invitations.Create: %v", err)
	}

	const forged = "token-forged"
	plantClaim(t, db, invitationTokenClaimRef(db, forged), invitationTokenClaimDoc{
		DocID:        invitationDocID(created.ID),
		InvitationID: created.ID,
	})

	if got, err := r.Invitations.GetByTokenHash(ctx, forged); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByTokenHash(%q) = %+v, %v — a token the invitation does not carry resolved to %s/%s", forged, got, err, got.ResourceType, got.ResourceID)
	}
	if _, err := r.Invitations.GetByTokenHash(ctx, "token-real"); err != nil {
		t.Errorf("GetByTokenHash on the real token: %v", err)
	}
}

// TestMisPointedDigestClaimConsumesNothing covers the three rails that resolve a
// challenge through its (purpose, digest) claim: Challenges.ConsumeToken,
// PasswordResets.Redeem and Passwordless.Redeem.
//
// The corruption here is the most dangerous of the set, because the claim's key
// carries the PURPOSE: a claim planted under a different purpose would let a
// magic link consume a password-reset challenge, or a reset token consume a
// login link, and each of those redemptions performs a different set of writes
// on the user's credentials.
func TestMisPointedDigestClaimConsumesNothing(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const (
		userID = "u-digest-verify"
		digest = "digest-real"
	)
	live, err := r.Challenges.Replace(ctx, challengeFixture(userID, challenge.PurposePasswordReset, "", digest, nil, 0, time.Hour, testBase))
	if err != nil {
		t.Fatalf("Challenges.Replace: %v", err)
	}
	if err := r.Passwords.Set(ctx, userID, "hash:before"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}

	// Two corruptions, one document: the row's purpose is password_reset and its
	// digest is "digest-real", and the claims below say otherwise.
	const forgedDigest = "digest-forged"
	owner := challengeDigestClaimDoc{
		DocID:       challengeDocID(userID, challenge.PurposePasswordReset),
		ChallengeID: live.ID,
	}
	plantClaim(t, db, challengeDigestClaimRef(db, challenge.PurposePasswordReset, forgedDigest), owner)
	plantClaim(t, db, challengeDigestClaimRef(db, linkPurpose, digest), owner)

	t.Run("ConsumeToken with a forged digest", func(t *testing.T) {
		if got, err := r.Challenges.ConsumeToken(ctx, challenge.PurposePasswordReset, forgedDigest, testBase); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("ConsumeToken = %+v, %v — a digest the challenge does not carry redeemed it", got, err)
		}
	})

	t.Run("ConsumeToken across purposes", func(t *testing.T) {
		if got, err := r.Challenges.ConsumeToken(ctx, linkPurpose, digest, testBase); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("ConsumeToken(%s) = %+v, %v — a password-reset challenge was redeemed as a magic link", linkPurpose, got, err)
		}
	})

	t.Run("PasswordResets.Redeem with a forged digest", func(t *testing.T) {
		if _, err := r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
			Purpose:         challenge.PurposePasswordReset,
			TokenDigest:     forgedDigest,
			NewPasswordHash: "hash:after",
			Now:             testBase,
		}); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("Redeem = %v, want sdk.ErrNotFound", err)
		}
		hash, err := r.Passwords.Get(ctx, userID)
		if err != nil {
			t.Fatalf("Passwords.Get: %v", err)
		}
		if hash != "hash:before" {
			t.Errorf("the password is now %q — a forged digest reset it", hash)
		}
	})

	t.Run("Passwordless.Redeem across purposes", func(t *testing.T) {
		in := redeemInput(digest, "refresh-digest-verify", testBase)
		if _, err := r.Passwordless.Redeem(ctx, in); !errors.Is(err, passwordless.ErrRedemption) {
			t.Errorf("Redeem = %v, want passwordless.ErrRedemption — a password-reset challenge is not a magic link", err)
		}
	})

	// The row and its real claim are untouched by any of it.
	if row, found := storedChallenge(t, db, userID, challenge.PurposePasswordReset); !found || row.SecretDigest != digest {
		t.Errorf("the live challenge did not survive the mis-pointed reads: found=%v row=%+v", found, row)
	}
	if got, err := r.Challenges.ConsumeToken(ctx, challenge.PurposePasswordReset, digest, testBase); err != nil {
		t.Errorf("ConsumeToken on the real (purpose, digest): %+v, %v", got, err)
	}
}

// TestAdoptionRefusesToRevokeTheSessionItIsMinting is the redemption's other
// one-document-two-writes hazard, and the only one a caller can trigger: a
// proposed session id that is already one of the sessions the adoption revokes.
//
// Delete-then-Create for one document in one transaction has no defined outcome
// — Firestore refuses the second write, and the ordering that did commit would
// decide whether the squatter's session survived the adoption that exists to
// revoke it. The port's answer for every stable bad outcome is one sentinel with
// nothing written, so that is what it gets.
func TestAdoptionRefusesToRevokeTheSessionItIsMinting(t *testing.T) {
	ctx := context.Background()
	r, db := openRepos(t)

	const addr = "collide@example.com"

	u := user.NewUser(dbGenerated, "Squatter", testBase)
	ident, err := identifier.NewRegistrationEmail(dbGenerated, normalizer, "", addr, testBase)
	if err != nil {
		t.Fatalf("NewRegistrationEmail: %v", err)
	}
	squatter, squatterIdent, err := r.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	sess, _ := session.NewSession(squatter.ID, time.Hour, testBase)
	sess.RefreshTokenHash = "squatter-collide-refresh"
	if _, err := r.Sessions.Create(ctx, sess); err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}

	binding := provisioningBinding(addr)
	binding.UserID, binding.IdentifierID = squatter.ID, squatterIdent.ID
	link := seedLink(t, r, "digest-collide", binding, testBase.Add(15*time.Minute))

	in := redeemInput(link.digest, "adopting-collide-refresh", testBase)
	in.Session.ID = sess.ID // the id the adoption is about to revoke

	if _, err := r.Passwordless.Redeem(ctx, in); !errors.Is(err, passwordless.ErrRedemption) {
		t.Fatalf("Redeem = %v, want passwordless.ErrRedemption", err)
	}

	// NOTHING written: the token survives with its claim, and the squatter's
	// session and password survive too — a partially-applied adoption is the one
	// outcome the port forbids outright.
	if row, claim := tokenIsStored(t, db, link); !row || !claim {
		t.Errorf("the refused redemption consumed the token: row=%v claim=%v", row, claim)
	}
	live, err := readSessionsForUser(ctx, db, db.ReaderFrom(ctx), squatter.ID)
	if err != nil {
		t.Fatalf("readSessionsForUser: %v", err)
	}
	if len(live) != 1 || live[0].ID != sess.ID {
		t.Errorf("sessions = %+v, want only the pre-existing %q", live, sess.ID)
	}

	// And the redemption still succeeds with a session id of its own, so the
	// refusal is about the collision rather than about the fixture.
	clean := redeemInput(link.digest, "adopting-clean-refresh", testBase)
	res, err := r.Passwordless.Redeem(ctx, clean)
	if err != nil {
		t.Fatalf("Redeem with a fresh session id: %v", err)
	}
	if res.Outcome != passwordless.OutcomeVerifyAndAdoptExistingUnverified {
		t.Errorf("outcome = %q, want verify_and_adopt", res.Outcome)
	}
}

// TestBagKeysRoundTripThroughTheEmulator is the storage half of the JSON-text
// decision (documents.go): the keys that a NATIVE Firestore map would reject or
// silently restructure — "" is not a legal field path, "a.b" is a nested path,
// "__x__" is reserved — survive a real write and a real read, through the ports.
func TestBagKeysRoundTripThroughTheEmulator(t *testing.T) {
	ctx := context.Background()
	r, _ := openRepos(t)

	details := map[string]any{"count": float64(3), "nested": map[string]any{"a.b": "deep"}}
	metadata := map[string]string{}
	for i, key := range awkwardKeys {
		details[key] = "detail"
		metadata[key] = map[bool]string{true: "meta", false: ""}[i%2 == 0]
	}

	evt := securityevent.New(dbGenerated, "login", "success", testBase)
	evt.UserID = "u-bags"
	evt.Details = details
	created, err := r.SecurityEvents.Create(ctx, evt)
	if err != nil {
		t.Fatalf("SecurityEvents.Create: %v", err)
	}
	if !sameBag(t, created.Details, details) {
		t.Errorf("Create returned details %#v, want %#v", created.Details, details)
	}
	page, err := r.SecurityEvents.List(ctx, securityevent.ListFilter{}, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("SecurityEvents.List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("List returned %d events, want 1", len(page.Items))
	}
	if !sameBag(t, page.Items[0].Details, details) {
		t.Errorf("List returned details %#v, want %#v", page.Items[0].Details, details)
	}

	inv := invitationFixture(t, "member", "bags@example.com", string(identifier.KindEmail), "token-bags", time.Hour, testBase)
	inv.Metadata = metadata
	createdInv, err := r.Invitations.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Invitations.Create: %v", err)
	}
	got, err := r.Invitations.Get(ctx, createdInv.ID)
	if err != nil {
		t.Fatalf("Invitations.Get: %v", err)
	}
	if len(got.Metadata) != len(metadata) {
		t.Fatalf("stored metadata = %#v, want %#v", got.Metadata, metadata)
	}
	for key, want := range metadata {
		if got.Metadata[key] != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got.Metadata[key], want)
		}
	}
}

// sameBag compares two open bags through the store's own JSON encoding, which is
// what "round-trips as itself" means for a value the domain never types.
func sameBag(t *testing.T, got, want map[string]any) bool {
	t.Helper()
	left, err := encodeDetails(got)
	if err != nil {
		t.Fatalf("encodeDetails(got): %v", err)
	}
	right, err := encodeDetails(want)
	if err != nil {
		t.Fatalf("encodeDetails(want): %v", err)
	}
	return left == right
}
