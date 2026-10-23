package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The erasure conformance cases (SOC 2 S3). Repositories.UserErasure is
// OPTIONAL, so the group skips LOUDLY when it is unwired; a case that needs a
// further optional port skips loudly without it. UserAdmin/SummaryReportsErased
// pins the erased projection on the directory, and ServiceAccounts/
// DeleteCascadesKeys (registered in the ServiceAccounts group) is gated on the
// serviceaccount.CascadeDeleter capability, so a store that has not adopted
// them stays green until it does.

// eraseActor is the administrator every prepared user_erased row names.
var eraseActor = securityevent.Principal{Type: "user", ID: "erasure-admin"}

// runUserErasure registers the erasure and erased-projection groups.
func runUserErasure(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("UserErasure", func(t *testing.T) {
		if newRepos(t).UserErasure == nil {
			t.Skip("UserErasure not wired — erasure conformance NOT verified for this Repositories")
		}
		for _, tc := range []struct {
			name string
			test func(*testing.T, auth.Repositories)
		}{
			{"EraseIsOneTransactionAndIdempotent", testEraseIdempotent},
			{"EraseBlanksIdentifiersReadBack", testEraseBlanksIdentifiers},
			{"EraseDeletesCredentialsSessionsGrantsCodesChallenges", testEraseDeletesCredentials},
			{"EraseDeletesAddressKeyedMagicLinkChallenge", testEraseMagicLinkChallenge},
			{"EraseInvitationsPendingDeletedTerminalBlankedIdsKeptIssuedCancelled", testEraseInvitations},
			{"EraseMatchesPhoneInvitation", testErasePhoneInvitation},
			{"RetiredAddressNowOwnedByAnotherUserUntouched", testEraseRetiredAddressElsewhere},
			{"EraseRefusedWhileAcceptingClaimHeld", testEraseBlockedByClaim},
			{"EraseDeletesOwnedActAsServiceAccountsAndKeysKeepsOthers", testEraseServiceAccounts},
			{"EraseWritesUserErasedRowInTransaction", testEraseWritesRow},
			{"EraseLeavesSecurityEventsIntact", testEraseLeavesRail},
			{"EraseRefusesEmptyAndUnknown", testEraseBadInputs},
			{"SetStatusAfterErase", testEraseSetStatus},
			{"MintFencedAfterErase", testEraseMintFenced},
		} {
			t.Run(tc.name, func(t *testing.T) { tc.test(t, newRepos(t)) })
		}
	})

	t.Run("UserAdmin", func(t *testing.T) {
		if r := newRepos(t); r.UserAdmin == nil || r.UserErasure == nil {
			t.Skip("UserAdmin or UserErasure not wired — the erased directory projection NOT verified for this Repositories")
		}
		t.Run("SummaryReportsErased", func(t *testing.T) { testSummaryReportsErased(t, newRepos(t)) })
	})
}

// seedErasable creates an active user with a verified primary email.
func seedErasable(t *testing.T, repos auth.Repositories, email string) user.User {
	t.Helper()
	now := retentionNow()
	return seedDirectoryUser(t, repos, email, now, now.Add(-time.Hour))
}

// eraseInput is an erasure of id at now with the service's prepared row.
func eraseInput(id string, now time.Time, keys ...string) user.EraseInput {
	return user.EraseInput{ID: id, Now: now, SubjectKeys: keys, Event: securityevent.SecurityEvent{
		ID: ids.MustGenerate(), UserID: id, Actor: eraseActor, EventType: securityevent.TypeUserErased, EventStatus: securityevent.StatusSuccess,
		IPAddress: "198.51.100.7", UserAgent: "admin-console", RequestID: "req-erase", CreatedAt: now,
	}}
}

// mustErase erases id and fails the test on error.
func mustErase(t *testing.T, repos auth.Repositories, in user.EraseInput) user.Erasure {
	t.Helper()
	got, err := repos.UserErasure.Erase(context.Background(), in)
	if err != nil {
		t.Fatalf("Erase(%s): %v", in.ID, err)
	}
	return got
}

// erasedRows returns the user_erased rows on the rail.
func erasedRows(t *testing.T, repos auth.Repositories) []securityevent.SecurityEvent {
	t.Helper()
	if repos.SecurityEvents == nil {
		return nil
	}
	page, err := repos.SecurityEvents.List(context.Background(), securityevent.ListFilter{EventType: securityevent.TypeUserErased}, listing.Request{Limit: 100})
	if err != nil {
		t.Fatalf("List user_erased: %v", err)
	}
	return page.Items
}

// createSession inserts an unexpired browser session for userID.
func createSession(t *testing.T, repos auth.Repositories, userID, label string) session.Session {
	t.Helper()
	s := newSession(userID, "erase-"+label+"-"+ids.MustGenerate(), time.Hour, retentionNow())
	created, err := repos.Sessions.Create(context.Background(), s)
	if err != nil {
		t.Fatalf("Sessions.Create(%s): %v", label, err)
	}
	return created
}

func testEraseIdempotent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedErasable(t, repos, "idempotent@example.com")
	if err := repos.Passwords.Set(ctx, u.ID, "hash:erase"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	createSession(t, repos, u.ID, "one")
	now := retentionNow()
	first := mustErase(t, repos, eraseInput(u.ID, now))
	if !first.Changed || !first.ErasedAt.Equal(now) {
		t.Fatalf("first erase = %+v, want Changed at %v", first, now)
	}
	if c := first.Counts; c.Sessions != 1 || c.Passwords != 1 || c.IdentifiersBlanked != 1 {
		t.Errorf("first counts = %+v", c)
	}
	second := mustErase(t, repos, eraseInput(u.ID, now.Add(time.Minute)))
	if second.Changed || !second.ErasedAt.Equal(now) || second.Counts != (user.EraseCounts{}) {
		t.Errorf("repeat erase = %+v, want unchanged with the first ErasedAt and zero counts", second)
	}
	if repos.SecurityEvents != nil {
		if rows := erasedRows(t, repos); len(rows) != 1 {
			t.Errorf("user_erased rows = %d, want 1 — a repeat writes no row", len(rows))
		}
	}
}

func testEraseBlanksIdentifiers(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedErasable(t, repos, "blank-old@example.com")
	before, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	primary, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "blank-old@example.com")
	if err != nil {
		t.Fatalf("GetLogin: %v", err)
	}
	changed, err := applyEmailChange(repos, u.ID, "blank-new@example.com", loginRecoveryUses, true, primary.ID, before.AuthRevision, retentionNow())
	if err != nil {
		t.Fatalf("applyEmailChange: %v", err)
	}
	now := retentionNow()
	got := mustErase(t, repos, eraseInput(u.ID, now))
	if got.Counts.IdentifiersBlanked != 2 {
		t.Errorf("IdentifiersBlanked = %d, want 2 (active and retired)", got.Counts.IdentifiersBlanked)
	}
	if active, err := repos.Identifiers.ListByUser(ctx, u.ID); err != nil || len(active) != 0 {
		t.Errorf("ListByUser after erase = %v, %v; want none active", active, err)
	}
	for _, id := range []string{primary.ID, changed.ID} {
		it, err := repos.Identifiers.Get(ctx, id)
		if err != nil {
			t.Fatalf("Identifiers.Get(%s): %v", id, err)
		}
		if it.NormalizedValue != "" || it.ReplacedAt.IsZero() || !it.VerifiedAt.IsZero() || it.LoginEnabled || it.RecoveryEnabled || it.NotificationEnabled || it.IsPrimary {
			t.Errorf("identifier %s not blanked: %+v", id, it)
		}
	}
	for _, addr := range []string{"blank-old@example.com", "blank-new@example.com"} {
		if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), addr); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("GetLogin(%s) after erase: err = %v, want ErrNotFound", addr, err)
		}
	}
	after, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != user.StatusErased || after.DisplayName != "" || after.AuthRevision <= before.AuthRevision || after.Active() {
		t.Errorf("user after erase = %+v (revision before %d)", after, before.AuthRevision)
	}
	// The blanked rows release the address: a new account may claim it.
	seedErasable(t, repos, "blank-new@example.com")
}

func testEraseDeletesCredentials(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedErasable(t, repos, "credentials@example.com")
	other := seedErasable(t, repos, "bystander@example.com")
	var want user.EraseCounts
	want.IdentifiersBlanked = 1
	if err := repos.Passwords.Set(ctx, u.ID, "hash:erase"); err != nil {
		t.Fatal(err)
	}
	want.Passwords = 1
	if err := repos.Passwords.Set(ctx, other.ID, "hash:other"); err != nil {
		t.Fatal(err)
	}
	web := createSession(t, repos, u.ID, "web")
	createSession(t, repos, u.ID, "second")
	otherSession := createSession(t, repos, other.ID, "other")
	want.Sessions = 2
	if repos.AuthenticationGrants != nil {
		if _, err := (grantFixture{repos}).Create(ctx, newGrant(web.ID, u.ID, "set_password", "ctx-erase", time.Hour, retentionNow())); err != nil {
			t.Fatalf("grant: %v", err)
		}
		want.Grants = 1
	}
	if repos.OAuthAccounts != nil {
		acct, err := oauthaccount.New(u.ID, "github", "gh-erase", retentionNow())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repos.OAuthAccounts.Create(ctx, acct); err != nil {
			t.Fatalf("OAuthAccounts.Create: %v", err)
		}
		want.OAuthAccounts = 1
	}
	if repos.ExternalSubjects != nil {
		s, err := externalsubject.New("idp", "subject-erase", u.ID, "https://idp.example", "credentials@example.com", retentionNow())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repos.ExternalSubjects.Link(ctx, s); err != nil {
			t.Fatalf("ExternalSubjects.Link: %v", err)
		}
		want.ExternalSubjects = 1
	}
	if repos.ContactChanges != nil {
		p := contactchange.New(u.ID, identifier.KindEmail, "pending@example.com", loginRecoveryUses, true, "", time.Hour, retentionNow())
		if _, err := repos.ContactChanges.Create(ctx, p); err != nil {
			t.Fatalf("ContactChanges.Create: %v", err)
		}
		want.ContactChanges = 1
	}
	if repos.Challenges != nil {
		if _, err := repos.Challenges.Replace(ctx, newChallenge(u.ID, challenge.PurposeVerifyRegistration, "k1", "digest-erase", nil, 0, time.Hour, retentionNow())); err != nil {
			t.Fatalf("Challenges.Replace: %v", err)
		}
		if _, err := repos.Challenges.Replace(ctx, newChallenge(other.ID, challenge.PurposeVerifyRegistration, "k1", "digest-other", nil, 0, time.Hour, retentionNow())); err != nil {
			t.Fatalf("Challenges.Replace: %v", err)
		}
		want.Challenges = 1
	}
	var code oauth2.Code
	if repos.OAuth2 != nil {
		current, err := repos.Users.Get(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		code = oauthFixture{r: repos, u: current, w: web, now: retentionNow()}.code(t)
		want.Codes = 1
	}

	got := mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	if got.Counts != want {
		t.Errorf("counts = %+v, want %+v", got.Counts, want)
	}
	if _, err := repos.Passwords.Get(ctx, u.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("password survived: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, web.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("session survived: %v", err)
	}
	if repos.OAuthAccounts != nil {
		if accounts, err := repos.OAuthAccounts.ListByUser(ctx, u.ID); err != nil || len(accounts) != 0 {
			t.Errorf("OAuth accounts survived: %v %v", accounts, err)
		}
	}
	if repos.ExternalSubjects != nil {
		if _, err := repos.ExternalSubjects.Get(ctx, "idp", "subject-erase"); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("external subject survived: %v", err)
		}
	}
	if repos.ContactChanges != nil {
		if _, err := repos.ContactChanges.Get(ctx, u.ID, identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("contact change survived: %v", err)
		}
	}
	if repos.OAuth2 != nil {
		if _, err := repos.OAuth2.GetCode(ctx, code.Hash); err == nil {
			t.Error("authorization code survived")
		}
	}
	if _, err := repos.Passwords.Get(ctx, other.ID); err != nil {
		t.Errorf("a bystander's password went with it: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, otherSession.ID); err != nil {
		t.Errorf("a bystander's session went with it: %v", err)
	}
}

func testEraseMagicLinkChallenge(t *testing.T, repos auth.Repositories) {
	if repos.Challenges == nil {
		t.Skip("Challenges not wired — address-keyed challenge erasure NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "magic@example.com")
	mine := newChallenge("", challenge.PurposeLoginMagicLink, "", "digest-magic-mine", nil, 0, time.Hour, retentionNow())
	mine.SubjectKey = "challenge:digest-of-magic:login_magic_link"
	theirs := newChallenge("", challenge.PurposeLoginMagicLink, "", "digest-magic-theirs", nil, 0, time.Hour, retentionNow())
	theirs.SubjectKey = "challenge:digest-of-someone-else:login_magic_link"
	for _, c := range []challenge.Challenge{mine, theirs} {
		if _, err := repos.Challenges.Replace(ctx, c); err != nil {
			t.Fatalf("Replace: %v", err)
		}
	}
	got := mustErase(t, repos, eraseInput(u.ID, retentionNow(), mine.SubjectKey))
	if got.Counts.Challenges != 1 {
		t.Errorf("Challenges = %d, want the one address-keyed link", got.Counts.Challenges)
	}
	if _, err := repos.Challenges.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "digest-magic-mine", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the erased address's link survived: %v", err)
	}
	if _, err := repos.Challenges.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "digest-magic-theirs", time.Now()); err != nil {
		t.Errorf("another address's link went with it: %v", err)
	}
}

func testEraseInvitations(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — invitation erasure NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "invitee@example.com")
	now := retentionNow()
	pending := seedRetentionInvitation(t, repos, "to-pending", "email", "invitee@example.com", "inviter-x", now, time.Hour)
	declined := seedRetentionInvitation(t, repos, "to-declined", "email", "invitee@example.com", "inviter-x", now, time.Hour)
	declineInvitation(t, repos, declined, now.Add(time.Second))
	accepted := seedRetentionInvitation(t, repos, "to-accepted", "email", "invitee@example.com", "inviter-x", now, time.Hour)
	claimRetentionInvitation(t, repos, accepted, u.ID, now.Add(time.Second), true)
	issuedPending := seedRetentionInvitation(t, repos, "issued-pending", "email", "someone@example.com", u.ID, now, time.Hour)
	issuedAccepted := seedRetentionInvitation(t, repos, "issued-accepted", "email", "else@example.com", u.ID, now, time.Hour)
	claimRetentionInvitation(t, repos, issuedAccepted, "someone-else", now.Add(time.Second), true)

	got := mustErase(t, repos, eraseInput(u.ID, now.Add(time.Minute)))
	if c := got.Counts; c.InvitationsDeleted != 1 || c.InvitationsBlanked != 2 || c.InvitationsCancelled != 1 {
		t.Errorf("invitation counts = %+v, want deleted 1, blanked 2, cancelled 1", c)
	}
	if _, err := repos.Invitations.Get(ctx, pending.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the pending invitation to the user survived: %v", err)
	}
	d, err := repos.Invitations.Get(ctx, declined.ID)
	if err != nil || d.Identifier != "" || len(d.Metadata) != 0 || d.InvitedBy != "inviter-x" || d.Status != invitations.StatusDeclined {
		t.Errorf("declined invitation = %+v, %v; want identifier and metadata blanked, issuer kept", d, err)
	}
	a, err := repos.Invitations.Get(ctx, accepted.ID)
	if err != nil || a.Identifier != "" || len(a.Metadata) != 0 || a.ResolvedSubjectID != u.ID || a.ResolvedSubjectType != "user" || a.Status != invitations.StatusAccepted {
		t.Errorf("accepted invitation = %+v, %v; want blanked with the resolved subject kept", a, err)
	}
	ip, err := repos.Invitations.Get(ctx, issuedPending.ID)
	if err != nil || ip.Status != invitations.StatusCancelled || ip.Identifier != "someone@example.com" || ip.InvitedBy != u.ID {
		t.Errorf("issued pending invitation = %+v, %v; want cancelled and otherwise unchanged", ip, err)
	}
	ia, err := repos.Invitations.Get(ctx, issuedAccepted.ID)
	if err != nil || ia.Status != invitations.StatusAccepted || ia.Identifier != "else@example.com" {
		t.Errorf("issued accepted invitation = %+v, %v; want unchanged", ia, err)
	}
}

func testErasePhoneInvitation(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — phone invitation erasure NOT verified")
	}
	ctx := context.Background()
	u := seedEmaillessUser(t, repos, "+15551230099", retentionNow().Add(-time.Hour))
	normalized, err := idNorm.Normalize(string(identifier.KindPhone), "+15551230099")
	if err != nil {
		t.Fatal(err)
	}
	inv := seedRetentionInvitation(t, repos, "phone", "phone", normalized, "inviter-x", retentionNow(), time.Hour)
	got := mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	if got.Counts.InvitationsDeleted != 1 {
		t.Errorf("InvitationsDeleted = %d, want the phone invitation", got.Counts.InvitationsDeleted)
	}
	if _, err := repos.Invitations.Get(ctx, inv.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the phone invitation survived: %v", err)
	}
}

func testEraseRetiredAddressElsewhere(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — the retired-address exclusion NOT verified")
	}
	ctx := context.Background()
	a := seedErasable(t, repos, "shared@example.com")
	ua, err := repos.Users.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "shared@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyEmailChange(repos, a.ID, "a-moved@example.com", loginRecoveryUses, true, shared.ID, ua.AuthRevision, retentionNow()); err != nil {
		t.Fatalf("retire the shared address: %v", err)
	}
	b := seedErasable(t, repos, "shared@example.com")
	inv := seedRetentionInvitation(t, repos, "shared", "email", "shared@example.com", "inviter-x", retentionNow(), time.Hour)

	got := mustErase(t, repos, eraseInput(a.ID, retentionNow()))
	if got.Counts.InvitationsDeleted != 0 || got.Counts.InvitationsBlanked != 0 {
		t.Errorf("counts = %+v; the address now belongs to another user", got.Counts)
	}
	if kept, err := repos.Invitations.Get(ctx, inv.ID); err != nil || kept.Identifier != "shared@example.com" || kept.Status != invitations.StatusPending {
		t.Errorf("the other owner's invitation = %+v, %v; want untouched", kept, err)
	}
	if owner, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "shared@example.com"); err != nil || owner.UserID != b.ID {
		t.Errorf("the other owner's identifier = %+v, %v", owner, err)
	}
}

func testEraseBlockedByClaim(t *testing.T, repos auth.Repositories) {
	if repos.Invitations == nil {
		t.Skip("Invitations not wired — the accepting-claim refusal NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "claimant@example.com")
	if err := repos.Passwords.Set(ctx, u.ID, "hash:claimant"); err != nil {
		t.Fatal(err)
	}
	sess := createSession(t, repos, u.ID, "claimant")
	now := retentionNow()
	inv := seedRetentionInvitation(t, repos, "claim", "email", "claimant@example.com", "inviter-x", now, time.Hour)
	claimRetentionInvitation(t, repos, inv, u.ID, now.Add(time.Second), false)

	_, err := repos.UserErasure.Erase(ctx, eraseInput(u.ID, now.Add(time.Minute)))
	if !errors.Is(err, user.ErrEraseBlockedByClaim) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("erase with an accepting claim: err = %v, want ErrEraseBlockedByClaim", err)
	}
	after, err := repos.Users.Get(ctx, u.ID)
	if err != nil || after.Status != user.StatusActive || after.DisplayName == "" {
		t.Errorf("the refused erase changed the user: %+v %v", after, err)
	}
	if _, err := repos.Passwords.Get(ctx, u.ID); err != nil {
		t.Errorf("the refused erase removed the password: %v", err)
	}
	if _, err := repos.Sessions.Get(ctx, sess.ID); err != nil {
		t.Errorf("the refused erase removed the session: %v", err)
	}
	if got, err := repos.Invitations.Get(ctx, inv.ID); err != nil || got.Status != invitations.StatusAccepting || got.Identifier != "claimant@example.com" {
		t.Errorf("the claim = %+v, %v; want untouched", got, err)
	}
	if rows := erasedRows(t, repos); len(rows) != 0 {
		t.Errorf("a refused erase wrote %d user_erased rows", len(rows))
	}
}

func testEraseServiceAccounts(t *testing.T, repos auth.Repositories) {
	if repos.ServiceAccounts == nil || repos.APIKeys == nil {
		t.Skip("ServiceAccounts/APIKeys not wired — the act-as cascade NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "owner@example.com")
	other := seedErasable(t, repos, "other-owner@example.com")
	now := retentionNow()
	mkAccount := func(name string, actAs bool, owner string) serviceaccount.ServiceAccount {
		sa, err := serviceaccount.New(ids, name, "", "admin", actAs, owner, now)
		if err != nil {
			t.Fatal(err)
		}
		created, err := repos.ServiceAccounts.Create(ctx, sa)
		if err != nil {
			t.Fatalf("ServiceAccounts.Create(%s): %v", name, err)
		}
		return created
	}
	personal := mkAccount("personal", true, u.ID)
	integration := mkAccount("integration", false, u.ID)
	theirs := mkAccount("theirs", true, other.ID)
	k1 := mustCreateAPIKey(t, repos.APIKeys, personal.ID, "k1", "hash-personal-1", time.Time{}, now)
	k2 := mustCreateAPIKey(t, repos.APIKeys, personal.ID, "k2", "hash-personal-2", time.Time{}, now)
	if err := repos.APIKeys.Revoke(ctx, k2.ID, now); err != nil {
		t.Fatal(err)
	}
	mustCreateAPIKey(t, repos.APIKeys, integration.ID, "k3", "hash-integration", time.Time{}, now)
	mustCreateAPIKey(t, repos.APIKeys, theirs.ID, "k4", "hash-theirs", time.Time{}, now)

	got := mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	if got.Counts.ServiceAccounts != 1 || got.Counts.APIKeys != 2 {
		t.Errorf("counts = %+v, want one act-as account and its two keys", got.Counts)
	}
	if _, err := repos.ServiceAccounts.Get(ctx, personal.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the act-as account survived: %v", err)
	}
	for _, k := range []apikey.APIKey{k1, k2} {
		if _, err := repos.APIKeys.GetByHash(ctx, k.KeyHash); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("key %s survived: %v", k.ID, err)
		}
	}
	for _, sa := range []serviceaccount.ServiceAccount{integration, theirs} {
		if _, err := repos.ServiceAccounts.Get(ctx, sa.ID); err != nil {
			t.Errorf("account %s went with it: %v", sa.Name, err)
		}
	}
	for _, hash := range []string{"hash-integration", "hash-theirs"} {
		if _, err := repos.APIKeys.GetByHash(ctx, hash); err != nil {
			t.Errorf("key %s went with it: %v", hash, err)
		}
	}
}

func testEraseWritesRow(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — the in-transaction user_erased row NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "row@example.com")
	createSession(t, repos, u.ID, "row")
	now := retentionNow()
	in := eraseInput(u.ID, now)
	got := mustErase(t, repos, in)
	rows := erasedRows(t, repos)
	if len(rows) != 1 {
		t.Fatalf("user_erased rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != in.Event.ID || row.UserID != u.ID || row.Actor != eraseActor || row.EventStatus != securityevent.StatusSuccess ||
		row.IPAddress != "198.51.100.7" || row.UserAgent != "admin-console" || !row.CreatedAt.Equal(now) {
		t.Errorf("user_erased row = %+v", row)
	}
	if n, ok := detailInt(row.Details["sessions"]); !ok || n != got.Counts.Sessions || n != 1 {
		t.Errorf("row Details sessions = %v, want the erasure's count %d", row.Details["sessions"], got.Counts.Sessions)
	}
	for _, key := range []string{"email", "identifier", "display_name"} {
		if _, ok := row.Details[key]; ok {
			t.Errorf("row Details carry %q", key)
		}
	}

	quiet := seedErasable(t, repos, "quiet@example.com")
	in = eraseInput(quiet.ID, retentionNow())
	in.Event = securityevent.SecurityEvent{}
	mustErase(t, repos, in)
	if rows := erasedRows(t, repos); len(rows) != 1 {
		t.Errorf("a zero Event appended a row: %d rows", len(rows))
	}
	if _, err := repos.Users.Get(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
}

func testEraseLeavesRail(t *testing.T, repos auth.Repositories) {
	if repos.SecurityEvents == nil {
		t.Skip("SecurityEvents not wired — rail immutability under erasure NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "rail-user@example.com")
	now := retentionNow()
	first := seedRailEvent(t, repos, "rail-erase-1", u.ID, now.Add(-2*time.Minute))
	second := seedRailEvent(t, repos, "rail-erase-2", u.ID, now.Add(-time.Minute))
	mustErase(t, repos, eraseInput(u.ID, now))
	page, err := repos.SecurityEvents.List(ctx, securityevent.ListFilter{UserID: u.ID}, listing.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]securityevent.SecurityEvent{}
	for _, evt := range page.Items {
		seen[evt.ID] = evt
	}
	for _, want := range []securityevent.SecurityEvent{first, second} {
		got, ok := seen[want.ID]
		if !ok || got.IPAddress != want.IPAddress || got.UserAgent != want.UserAgent || got.Details["reason"] != "fixture" || got.UserID != u.ID {
			t.Errorf("rail row %s after erase = %+v (present %v), want unchanged", want.ID, got, ok)
		}
	}
	if len(page.Items) != 3 {
		t.Errorf("the user's rail rows = %d, want the two earlier rows plus user_erased", len(page.Items))
	}
}

func testEraseBadInputs(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.UserErasure.Erase(ctx, eraseInput("", retentionNow())); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty id: err = %v, want ErrInvalidInput", err)
	}
	if _, err := repos.UserErasure.Erase(ctx, eraseInput("no-such-user", retentionNow())); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unknown id: err = %v, want ErrNotFound", err)
	}
	if rows := erasedRows(t, repos); len(rows) != 0 {
		t.Errorf("a refused erase wrote %d rows", len(rows))
	}
}

func testEraseSetStatus(t *testing.T, repos auth.Repositories) {
	if repos.UserAdmin == nil {
		t.Skip("UserAdmin not wired — status transitions after erasure NOT verified")
	}
	ctx := context.Background()
	bystander := seedErasable(t, repos, "bystander-status@example.com")
	if _, err := repos.UserAdmin.SetStatus(ctx, bystander.ID, user.StatusErased, retentionNow()); !errors.Is(err, user.ErrErased) || !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("SetStatus(erased): err = %v, want ErrErased as a refused transition", err)
	}
	if got, _ := repos.Users.Get(ctx, bystander.ID); got.Status != user.StatusActive {
		t.Errorf("a refused SetStatus(erased) changed the user to %s", got.Status)
	}
	u := seedErasable(t, repos, "status@example.com")
	erasure := mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	change, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusDeactivated, retentionNow())
	if err != nil || change.Changed || change.Status != user.StatusErased {
		t.Errorf("deactivate an erased user = %+v, %v; want {erased, unchanged}", change, err)
	}
	if _, err := repos.UserAdmin.SetStatus(ctx, u.ID, user.StatusActive, retentionNow()); !errors.Is(err, user.ErrErased) {
		t.Errorf("reactivate an erased user: err = %v, want ErrErased", err)
	}
	got, err := repos.Users.Get(ctx, u.ID)
	if err != nil || got.Status != user.StatusErased || got.Active() {
		t.Errorf("user after the refused transitions = %+v, %v", got, err)
	}
	if !erasure.Changed {
		t.Error("the erase did not apply")
	}
}

func testEraseMintFenced(t *testing.T, repos auth.Repositories) {
	if repos.ActiveSessions == nil {
		t.Skip("ActiveSessions not wired — the fenced mint after erasure NOT verified")
	}
	ctx := context.Background()
	u := seedErasable(t, repos, "mint@example.com")
	mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	current, err := repos.Users.Get(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(u.ID, "erase-mint-"+ids.MustGenerate(), time.Hour, retentionNow())
	if _, err := repos.ActiveSessions.CreateForActiveUser(ctx, s, current.AuthRevision); !errors.Is(err, session.ErrUserNotActive) {
		t.Errorf("mint for an erased user: err = %v, want ErrUserNotActive", err)
	}
	if _, err := repos.Sessions.Get(ctx, s.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a session was minted for an erased user: %v", err)
	}
}

func testSummaryReportsErased(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := seedErasable(t, repos, "summary@example.com")
	v := seedErasable(t, repos, "summary-other@example.com")
	mustErase(t, repos, eraseInput(u.ID, retentionNow()))
	got, err := repos.UserAdmin.GetSummary(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != user.StatusErased || got.DisplayName != "" || got.PrimaryEmail != "" || got.EmailVerified {
		t.Errorf("summary of an erased user = %+v", got)
	}
	page, err := repos.UserAdmin.List(ctx, listing.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if row, ok := findSummary(page, u.ID); !ok || row.Status != user.StatusErased {
		t.Errorf("directory row of the erased user = %+v (present %v)", row, ok)
	}
	if row, ok := findSummary(page, v.ID); !ok || row.Status != user.StatusActive {
		t.Errorf("directory row of a bystander = %+v (present %v)", row, ok)
	}
	if reread, err := repos.Users.Get(ctx, u.ID); err != nil || reread.Status != user.StatusErased {
		t.Errorf("Users.Get of an erased user = %+v, %v", reread, err)
	}
}

// testServiceAccountsDeleteCascades proves the cascade: DeleteWithKeys removes
// the account and every key and reports the counts, and the plain Delete of a
// store with the capability cascades too.
func testServiceAccountsDeleteCascades(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	cascader := repos.ServiceAccounts.(serviceaccount.CascadeDeleter)
	now := retentionNow()
	mk := func(name string) serviceaccount.ServiceAccount {
		sa, err := serviceaccount.New(ids, name, "", "admin", false, "", now)
		if err != nil {
			t.Fatal(err)
		}
		created, err := repos.ServiceAccounts.Create(ctx, sa)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := mk("cascade-1")
	k1 := mustCreateAPIKey(t, repos.APIKeys, first.ID, "a", "hash-cascade-a", time.Time{}, now)
	k2 := mustCreateAPIKey(t, repos.APIKeys, first.ID, "b", "hash-cascade-b", time.Time{}, now)
	if err := repos.APIKeys.Revoke(ctx, k2.ID, now); err != nil {
		t.Fatal(err)
	}
	deletion, err := cascader.DeleteWithKeys(ctx, first.ID)
	if err != nil || deletion != (serviceaccount.Deletion{KeyCount: 2, UnrevokedKeyCount: 1}) {
		t.Fatalf("DeleteWithKeys = %+v, %v; want 2 keys, 1 unrevoked", deletion, err)
	}
	for _, k := range []apikey.APIKey{k1, k2} {
		if _, err := repos.APIKeys.GetByHash(ctx, k.KeyHash); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("key %s survived the cascade: %v", k.ID, err)
		}
	}
	if _, err := repos.ServiceAccounts.Get(ctx, first.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("the account survived: %v", err)
	}
	if _, err := cascader.DeleteWithKeys(ctx, first.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("a repeat DeleteWithKeys: err = %v, want ErrNotFound", err)
	}

	second := mk("cascade-2")
	k3 := mustCreateAPIKey(t, repos.APIKeys, second.ID, "c", "hash-cascade-c", time.Time{}, now)
	if err := repos.ServiceAccounts.Delete(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.APIKeys.GetByHash(ctx, k3.KeyHash); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Delete left an orphan key: %v", err)
	}
}
