package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

func runIdentifierMutations(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Run("IdentifierMutations", func(t *testing.T) {
		repos := newRepos(t)
		if repos.CredentialMutations == nil || repos.Identifiers == nil {
			t.Skip("CredentialMutations or Identifiers not wired — identifier mutation conformance NOT verified")
		}
		t.Run("ConcurrentReplacementRetirement", func(t *testing.T) { testConcurrentIdentifierReplacement(t, newRepos(t)) })
		for _, scenario := range []string{"foreign_target", "missing_target", "retired_target", "foreign_replacement", "missing_replacement", "retired_replacement", "self_replacement", "wrong_kind", "secondary_target", "foreign_uses", "retired_uses", "missing_uses", "unverified_login_uses", "valid_replacement", "valid_contact_only_replacement"} {
			t.Run(scenario, func(t *testing.T) { testIdentifierMutation(t, newRepos(t), scenario) })
		}
	})
}

func testIdentifierMutation(t *testing.T, r auth.Repositories, scenario string) {
	t.Helper()
	ctx := context.Background()
	actor, primary := seedUserWithIdentifier(t, r, "actor", "actor@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	victim, foreign := seedUserWithIdentifier(t, r, "victim", "victim@example.com", identifier.KindEmail, identifier.Uses{Notification: true}, false, time.Time{})
	add := func(kind identifier.Kind, value string, replaces string, verifiedAt time.Time) identifier.Identifier {
		t.Helper()
		owner, err := r.Users.Get(ctx, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		it, err := r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{UserID: actor.ID, Kind: kind, NormalizedValue: value, NotificationEnabled: true, ReplacesIdentifierID: replaces}, owner.AuthRevision, verifiedAt)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	secondary := add(identifier.KindEmail, "secondary@example.com", "", suiteBase)
	phone := add(identifier.KindPhone, "+12025550101", "", suiteBase)
	retired := add(identifier.KindEmail, "retired@example.com", "", suiteBase)
	fresh := add(identifier.KindEmail, "fresh@example.com", retired.ID, suiteBase)
	// The port preserves a zero verification timestamp for contact-only identifiers.
	contactOnly := add(identifier.KindEmail, "unverified@example.com", "", time.Time{})
	mutation := credential.Mutation(credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: secondary.ID})
	switch scenario {
	case "foreign_target":
		mutation = credential.RetireIdentifier{IdentifierID: foreign.ID}
	case "missing_target":
		mutation = credential.RetireIdentifier{IdentifierID: "missing"}
	case "retired_target":
		mutation = credential.RetireIdentifier{IdentifierID: retired.ID}
	case "foreign_replacement":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: foreign.ID}
	case "missing_replacement":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: "missing"}
	case "retired_replacement":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: retired.ID}
	case "self_replacement":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: primary.ID}
	case "wrong_kind":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: phone.ID}
	case "secondary_target":
		mutation = credential.RetireIdentifier{IdentifierID: secondary.ID, ReplacementPrimaryID: foreign.ID}
	case "foreign_uses":
		mutation = credential.ChangeIdentifierUses{IdentifierID: foreign.ID, Uses: credential.IdentifierUses{Notification: true}, MakePrimary: true}
	case "retired_uses":
		mutation = credential.ChangeIdentifierUses{IdentifierID: retired.ID, MakePrimary: true}
	case "missing_uses":
		mutation = credential.ChangeIdentifierUses{IdentifierID: "missing", MakePrimary: true}
	case "unverified_login_uses":
		mutation = credential.ChangeIdentifierUses{IdentifierID: contactOnly.ID, Uses: credential.IdentifierUses{Login: true}}
	case "valid_contact_only_replacement":
		mutation = credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: contactOnly.ID}
	}
	before := make(map[string]identifier.Identifier)
	for _, it := range []identifier.Identifier{primary, secondary, phone, retired, fresh, contactOnly, foreign} {
		got, err := r.Identifiers.Get(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		before[it.ID] = got
	}
	actor, err := r.Users.Get(ctx, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	victim, err = r.Users.Get(ctx, victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sess := newSession(actor.ID, "identifier-mutation-session", time.Hour, now)
	sess, err = r.Sessions.Create(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	var proof challenge.Challenge
	if r.Challenges != nil {
		proof, err = r.Challenges.Replace(ctx, newChallenge(actor.ID, challenge.PurposePasswordReset, "", "identifier-mutation-proof", nil, 0, time.Hour, now))
		if err != nil {
			t.Fatal(err)
		}
	}
	grant := newGrant(sess.ID, actor.ID, authgrant.PurposeRemoveIdentifier, primary.ID, time.Hour, now)
	if r.AuthenticationGrants != nil {
		if _, err := r.AuthenticationGrants.Create(ctx, grant, actor.AuthRevision, now); err != nil {
			t.Fatal(err)
		}
	}
	err = r.CredentialMutations.Apply(ctx, actor.ID, actor.AuthRevision, mutation)
	valid := scenario == "valid_replacement" || scenario == "valid_contact_only_replacement"
	if valid {
		if err != nil {
			t.Fatalf("valid replacement: %v", err)
		}
		target, err := r.Identifiers.Get(ctx, primary.ID)
		if err != nil || target.Active() {
			t.Fatalf("retirement: %+v, %v", target, err)
		}
		replacementID := mutation.(credential.RetireIdentifier).ReplacementPrimaryID
		replacement, err := r.Identifiers.Get(ctx, replacementID)
		if err != nil || !replacement.Active() || !replacement.IsPrimary {
			t.Fatalf("promotion: %+v, %v", replacement, err)
		}
		if replacement.Verified() != before[replacementID].Verified() {
			t.Fatal("promotion changed verification")
		}
		owner, err := r.Users.Get(ctx, actor.ID)
		if err != nil || owner.AuthRevision != actor.AuthRevision+1 {
			t.Fatalf("revision: %+v, %v", owner, err)
		}
		if _, err := r.Sessions.Get(ctx, sess.ID); !errors.Is(err, sdk.ErrNotFound) {
			t.Fatalf("session survived: %v", err)
		}
	} else {
		if !errors.Is(err, sdk.ErrInvalidInput) && !(scenario == "unverified_login_uses" && errors.Is(err, identifier.ErrVerificationRequired)) {
			t.Errorf("invalid mutation returned %v", err)
		}
		for id, want := range before {
			got, err := r.Identifiers.Get(ctx, id)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("identifier changed on rejection: got=%+v want=%+v err=%v", got, want, err)
			}
		}
		owner, err := r.Users.Get(ctx, actor.ID)
		if err != nil || !reflect.DeepEqual(owner, actor) {
			t.Errorf("actor changed on rejection: %+v, %v", owner, err)
		}
		if _, err := r.Sessions.Get(ctx, sess.ID); err != nil {
			t.Errorf("session changed on rejection: %v", err)
		}
		if r.Challenges != nil {
			if _, err := r.Challenges.ConsumeToken(ctx, proof.Purpose, proof.SecretDigest, now); err != nil {
				t.Errorf("proof changed on rejection: %v", err)
			}
		}
		if r.AuthenticationGrants != nil {
			if _, err := (grantFixture{r}).Consume(ctx, sess.ID, grant.Purpose, grant.ContextDigest, now); err != nil {
				t.Errorf("grant changed on rejection: %v", err)
			}
		}
	}
	other, err := r.Users.Get(ctx, victim.ID)
	if err != nil || !reflect.DeepEqual(other, victim) {
		t.Errorf("other user changed: %+v, %v", other, err)
	}
}

func testConcurrentIdentifierReplacement(t *testing.T, r auth.Repositories) {
	ctx := context.Background()
	actor, primary := seedUserWithIdentifier(t, r, "actor", "actor@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	replacement, err := applyEmailChange(r, actor.ID, "replacement@example.com", identifier.Uses{Notification: true}, false, "", actor.AuthRevision, suiteBase)
	if err != nil {
		t.Fatal(err)
	}
	actor, err = r.Users.Get(ctx, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []credential.Mutation{
		credential.RetireIdentifier{IdentifierID: primary.ID, ReplacementPrimaryID: replacement.ID},
		credential.RetireIdentifier{IdentifierID: replacement.ID},
	}
	results := make([]error, len(mutations))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, mutation := range mutations {
		wg.Go(func() { <-start; results[i] = r.CredentialMutations.Apply(ctx, actor.ID, actor.AuthRevision, mutation) })
	}
	close(start)
	wg.Wait()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("unexpected competing retirement: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d results=%v", winners, results)
	}
	owner, err := r.Users.Get(ctx, actor.ID)
	if err != nil || owner.AuthRevision != actor.AuthRevision+1 {
		t.Fatalf("revision: %+v, %v", owner, err)
	}
	target, err := r.Identifiers.Get(ctx, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := r.Identifiers.Get(ctx, replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if results[0] == nil {
		if target.Active() || !promoted.Active() || !promoted.IsPrimary {
			t.Fatalf("replacement winner partially applied: %+v %+v", target, promoted)
		}
	} else if !target.Active() || !target.IsPrimary || promoted.Active() || promoted.IsPrimary {
		t.Fatalf("retirement winner partially applied: %+v %+v", target, promoted)
	}
}
