package credential

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// emailID / phoneID build verified identifier methods for the policy tables.
func emailID(id string, login, recovery bool) IdentifierMethod {
	return IdentifierMethod{ID: id, Kind: identity.KindEmail, Verified: true, Uses: IdentifierUses{Login: login, Recovery: recovery}}
}

func phoneID(id string, login, recovery bool) IdentifierMethod {
	return IdentifierMethod{ID: id, Kind: identity.KindPhone, Verified: true, Uses: IdentifierUses{Login: login, Recovery: recovery}}
}

func TestDefaultPolicyLoginFloor(t *testing.T) {
	p := NewDefaultPolicy(PolicyConfig{})
	ctx := context.Background()

	// A password + one verified login+recovery email: removing the password still
	// leaves the email as a login and recovery method → allowed.
	current := MethodSet{HasPassword: true, Identifiers: []IdentifierMethod{emailID("e1", true, true)}}
	proposed := current.With(RemovePassword{})
	if err := p.EvaluateMutation(ctx, current, proposed); err != nil {
		t.Fatalf("remove password with a standing login+recovery email: err=%v, want nil", err)
	}

	// Removing the last login method is rejected with the cannot_remove_last_method
	// conflict. A password-only set (no verified recovery) already violates the
	// recovery floor, so build a set whose only login is an email that is also the
	// only recovery, then retire it.
	soleLoginRecovery := MethodSet{Identifiers: []IdentifierMethod{emailID("e1", true, true)}}
	afterRetire := soleLoginRecovery.With(RetireIdentifier{IdentifierID: "e1"})
	err := p.EvaluateMutation(ctx, soleLoginRecovery, afterRetire)
	if !errors.Is(err, ErrNoLoginMethod) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("retire the sole login method: err=%v, want ErrNoLoginMethod wrapping ErrConflict", err)
	}
}

func TestDefaultPolicyRecoveryFloor(t *testing.T) {
	p := NewDefaultPolicy(PolicyConfig{})
	ctx := context.Background()

	// A password (login) plus a single recovery email: retiring the email leaves a
	// login (password) but no verified recovery → ErrNoRecoveryMethod.
	current := MethodSet{HasPassword: true, Identifiers: []IdentifierMethod{emailID("e1", false, true)}}
	proposed := current.With(RetireIdentifier{IdentifierID: "e1"})
	err := p.EvaluateMutation(ctx, current, proposed)
	if !errors.Is(err, ErrNoRecoveryMethod) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("retire the sole recovery method: err=%v, want ErrNoRecoveryMethod wrapping ErrConflict", err)
	}

	// The "recovery set is only the identifier being removed" case: the proposed
	// (post-removal) set is what is evaluated, so it is rejected structurally.
	if len(proposed.VerifiedRecoveryMethods()) != 0 {
		t.Fatalf("proposed recovery methods = %d, want 0", len(proposed.VerifiedRecoveryMethods()))
	}
}

func TestDefaultPolicyMinRecoveryMethods(t *testing.T) {
	p := NewDefaultPolicy(PolicyConfig{MinRecoveryMethods: 2})
	ctx := context.Background()

	// Two recovery emails: retiring one drops to one recovery method, below the
	// host's floor of two → ErrInsufficientRecovery (distinct from the zero case).
	current := MethodSet{HasPassword: true, Identifiers: []IdentifierMethod{emailID("e1", false, true), emailID("e2", false, true)}}
	proposed := current.With(RetireIdentifier{IdentifierID: "e1"})
	err := p.EvaluateMutation(ctx, current, proposed)
	if !errors.Is(err, ErrInsufficientRecovery) {
		t.Fatalf("drop below the recovery floor: err=%v, want ErrInsufficientRecovery", err)
	}
	// Both present satisfies the floor.
	if err := p.EvaluateMutation(ctx, current, current); err != nil {
		t.Fatalf("two recovery emails satisfy MinRecoveryMethods=2: err=%v, want nil", err)
	}
}

func TestDefaultPolicyPSTNRestriction(t *testing.T) {
	ctx := context.Background()

	// A phone-only recovery set: the default (PSTN permitted) accepts it, the
	// stronger host rule (RequireNonPSTNRecovery) rejects it.
	set := MethodSet{HasPassword: true, Identifiers: []IdentifierMethod{phoneID("p1", false, true)}}

	if err := NewDefaultPolicy(PolicyConfig{}).EvaluateMutation(ctx, set, set); err != nil {
		t.Fatalf("PSTN recovery under the default: err=%v, want nil (PSTN restricted, not forbidden)", err)
	}

	strict := NewDefaultPolicy(PolicyConfig{RequireNonPSTNRecovery: true})
	if err := strict.EvaluateMutation(ctx, set, set); !errors.Is(err, ErrRecoveryRequiresNonPSTN) {
		t.Fatalf("PSTN-only recovery under RequireNonPSTNRecovery: err=%v, want ErrRecoveryRequiresNonPSTN", err)
	}

	// Adding a verified non-PSTN (email) recovery method satisfies the strict rule.
	mixed := MethodSet{HasPassword: true, Identifiers: []IdentifierMethod{phoneID("p1", false, true), emailID("e1", false, true)}}
	if err := strict.EvaluateMutation(ctx, mixed, mixed); err != nil {
		t.Fatalf("mixed PSTN+email recovery under the strict rule: err=%v, want nil", err)
	}
}

func TestDefaultPolicyIgnoresUnverifiedAndUnknownKinds(t *testing.T) {
	p := NewDefaultPolicy(PolicyConfig{})
	ctx := context.Background()

	// An unverified identifier is neither a login nor a recovery method, and an
	// unknown kind never silently counts. Only the password stands as a login and
	// there is no recovery → rejected on the recovery floor.
	set := MethodSet{
		HasPassword: true,
		Identifiers: []IdentifierMethod{
			{ID: "e1", Kind: identity.KindEmail, Verified: false, Uses: IdentifierUses{Login: true, Recovery: true}},
			{ID: "x1", Kind: "unknown_kind", Verified: true, Uses: IdentifierUses{Login: true, Recovery: true}},
		},
	}
	if got := len(set.LoginMethods()); got != 1 {
		t.Fatalf("login methods = %d, want 1 (password only)", got)
	}
	if got := len(set.VerifiedRecoveryMethods()); got != 0 {
		t.Fatalf("recovery methods = %d, want 0", got)
	}
	if err := p.EvaluateMutation(ctx, set, set); !errors.Is(err, ErrNoRecoveryMethod) {
		t.Fatalf("unverified/unknown identifiers do not count: err=%v, want ErrNoRecoveryMethod", err)
	}
}

func TestWithMutations(t *testing.T) {
	base := MethodSet{
		AuthRevision: 7,
		HasPassword:  true,
		OAuth:        []OAuthMethod{{Provider: "google", Assurance: session.AssuranceAAL1}, {Provider: "github", Assurance: session.AssuranceAAL1}},
		Identifiers: []IdentifierMethod{
			{ID: "e1", Kind: identity.KindEmail, Verified: true, Primary: true, Uses: IdentifierUses{Login: true, Recovery: true}},
			{ID: "e2", Kind: identity.KindEmail, Verified: true, Uses: IdentifierUses{Recovery: true}},
		},
	}

	t.Run("RemovePasswordDoesNotMutateReceiver", func(t *testing.T) {
		got := base.With(RemovePassword{})
		if got.HasPassword {
			t.Errorf("proposed still has a password")
		}
		if !base.HasPassword {
			t.Errorf("With mutated the receiver's password")
		}
		if got.AuthRevision != base.AuthRevision {
			t.Errorf("With changed AuthRevision: %d, want %d", got.AuthRevision, base.AuthRevision)
		}
	})

	t.Run("UnlinkOAuth", func(t *testing.T) {
		got := base.With(UnlinkOAuth{Provider: "google"})
		if len(got.OAuth) != 1 || got.OAuth[0].Provider != "github" {
			t.Errorf("oauth after unlink = %+v, want github only", got.OAuth)
		}
		if len(base.OAuth) != 2 {
			t.Errorf("With mutated the receiver's oauth slice: %+v", base.OAuth)
		}
	})

	t.Run("RetireIdentifierPromotesReplacement", func(t *testing.T) {
		got := base.With(RetireIdentifier{IdentifierID: "e1", ReplacementPrimaryID: "e2"})
		if len(got.Identifiers) != 1 || got.Identifiers[0].ID != "e2" || !got.Identifiers[0].Primary {
			t.Errorf("identifiers after retire = %+v, want e2 promoted to primary", got.Identifiers)
		}
		if !base.Identifiers[0].Primary {
			t.Errorf("With mutated the receiver's identifier primary flag")
		}
	})

	t.Run("ChangeIdentifierUsesReassignsPrimary", func(t *testing.T) {
		got := base.With(ChangeIdentifierUses{IdentifierID: "e2", Uses: IdentifierUses{Login: true, Recovery: true}, MakePrimary: true})
		var e1, e2 IdentifierMethod
		for _, m := range got.Identifiers {
			switch m.ID {
			case "e1":
				e1 = m
			case "e2":
				e2 = m
			}
		}
		if !e2.Primary || !e2.Uses.Login || !e2.Uses.Recovery {
			t.Errorf("e2 after change = %+v, want primary login+recovery", e2)
		}
		if e1.Primary {
			t.Errorf("e1 stayed primary after e2 was promoted: %+v", e1)
		}
	})
}
