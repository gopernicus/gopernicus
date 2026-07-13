package credential

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// Stable credential-policy errors. Each wraps sdk.ErrConflict so the transport
// maps a rejected mutation to 409 with the pinned machine code (design §5.8);
// callers detect them with errors.Is. They are decisions about a proposed method
// set, never string-parsed.
var (
	// ErrNoLoginMethod rejects a mutation that would leave no direct login method
	// (the pinned cannot_remove_last_method 409, design §5.8).
	ErrNoLoginMethod = fmt.Errorf("credential: mutation would leave no direct login method: %w", sdk.ErrConflict)
	// ErrNoRecoveryMethod rejects a mutation that would leave no verified recovery
	// method (design §5.6).
	ErrNoRecoveryMethod = fmt.Errorf("credential: mutation would leave no verified recovery method: %w", sdk.ErrConflict)
	// ErrInsufficientRecovery rejects a mutation that would drop the verified
	// recovery method count below a host-configured minimum (design §5.6).
	ErrInsufficientRecovery = fmt.Errorf("credential: mutation would leave too few verified recovery methods: %w", sdk.ErrConflict)
	// ErrRecoveryRequiresNonPSTN rejects a mutation that would leave only PSTN
	// (SMS) recovery when the host requires a non-PSTN recovery method (design
	// §5.6). PSTN is restricted, not phishing-resistant.
	ErrRecoveryRequiresNonPSTN = fmt.Errorf("credential: mutation would leave only PSTN recovery: %w", sdk.ErrConflict)
	// ErrInsufficientAssurance rejects a mutation that would leave no login method
	// meeting a host-configured minimum assurance (design §5.6).
	ErrInsufficientAssurance = fmt.Errorf("credential: mutation would leave no login method at the required assurance: %w", sdk.ErrConflict)
)

// Policy evaluates a proposed credential/identifier mutation (design §5.6). The
// service computes proposed := current.With(mutation), calls EvaluateMutation
// immediately before the store's revision-CAS Apply, and reloads/re-evaluates on
// an sdk.ErrConflict. A nil return authorizes the mutation; a non-nil return is a
// stable credential-policy error.
type Policy interface {
	EvaluateMutation(ctx context.Context, current, proposed MethodSet) error
}

// PolicyConfig carries the bundled default's host-tunable rules (design §5.6).
// The zero value is the safe default: at least one direct login method and one
// verified recovery method (PSTN permitted). Hosts may require two independent
// recovery methods, a non-PSTN recovery method, or a minimum login assurance.
type PolicyConfig struct {
	// MinRecoveryMethods is the required verified-recovery-method floor; values
	// below 1 are treated as 1 (the safe default always requires one).
	MinRecoveryMethods int
	// RequireNonPSTNRecovery requires at least one verified non-PSTN recovery
	// method (an SMS-only recovery set is rejected).
	RequireNonPSTNRecovery bool
	// MinLoginAssurance, when set, requires at least one direct login method at or
	// above this assurance level. Empty imposes no assurance floor.
	MinLoginAssurance session.AssuranceLevel
}

// DefaultPolicy is the bundled safe credential policy (design §5.6). It evaluates
// the proposed (post-mutation) method set: at least one direct login method, at
// least MinRecoveryMethods verified recovery methods, and the optional non-PSTN /
// assurance floors. It never runs inside SQL — the store's revision-CAS Apply
// serializes concurrent removals; this evaluator only decides whether a single
// proposed state is safe.
type DefaultPolicy struct {
	minRecoveryMethods     int
	requireNonPSTNRecovery bool
	minLoginAssurance      session.AssuranceLevel
}

// NewDefaultPolicy builds the bundled safe policy from cfg, normalizing the
// recovery floor to at least one.
func NewDefaultPolicy(cfg PolicyConfig) DefaultPolicy {
	minRecovery := cfg.MinRecoveryMethods
	if minRecovery < 1 {
		minRecovery = 1
	}
	return DefaultPolicy{
		minRecoveryMethods:     minRecovery,
		requireNonPSTNRecovery: cfg.RequireNonPSTNRecovery,
		minLoginAssurance:      cfg.MinLoginAssurance,
	}
}

// EvaluateMutation applies the safe-default rules to the proposed set. current is
// accepted for host policies that reason over the transition; the default decides
// on the proposed state alone.
func (p DefaultPolicy) EvaluateMutation(_ context.Context, _ MethodSet, proposed MethodSet) error {
	if len(proposed.LoginMethods()) == 0 {
		return ErrNoLoginMethod
	}
	recovery := proposed.VerifiedRecoveryMethods()
	if len(recovery) == 0 {
		return ErrNoRecoveryMethod
	}
	if len(recovery) < p.minRecoveryMethods {
		return ErrInsufficientRecovery
	}
	if p.requireNonPSTNRecovery && !anyNonPSTN(recovery) {
		return ErrRecoveryRequiresNonPSTN
	}
	if p.minLoginAssurance != "" && !anyAtLeastAssurance(proposed.LoginMethods(), p.minLoginAssurance) {
		return ErrInsufficientAssurance
	}
	return nil
}

func anyNonPSTN(methods []session.AuthenticationMethod) bool {
	for _, m := range methods {
		if !m.PSTN {
			return true
		}
	}
	return false
}

func anyAtLeastAssurance(methods []session.AuthenticationMethod, min session.AssuranceLevel) bool {
	for _, m := range methods {
		if assuranceRank(m.Assurance) >= assuranceRank(min) {
			return true
		}
	}
	return false
}

// assuranceRank orders the honest assurance levels so a minimum can be compared
// (design §12.1): unknown < aal1 < aal2 < aal3.
func assuranceRank(a session.AssuranceLevel) int {
	switch a {
	case session.AssuranceAAL1:
		return 1
	case session.AssuranceAAL2:
		return 2
	case session.AssuranceAAL3:
		return 3
	default:
		return 0
	}
}
