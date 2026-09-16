package model

import (
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Default work-budget ceilings. Each is a SAFE NONZERO default: a zero field in
// a configured EvaluationLimits resolves to its default here — never to
// unlimited. The sizes bound worst-case fan-out/recursion for one decision or
// enumeration well above any hand-modeled hierarchy while still capping an
// adversarial or misconfigured schema.
const (
	DefaultMaxEvaluationSteps = 100000
	// DefaultMaxThroughDepth bounds navigational Through recursion (the former
	// MaxTraversalDepth).
	DefaultMaxThroughDepth = 10
	// DefaultMaxGraphStates bounds the distinct (resource, permission) states an
	// expanded decision graph may visit before it is declared indeterminate.
	DefaultMaxGraphStates = 10000
	// DefaultMaxRelationTargets bounds navigational Through fan-out. Membership
	// expansion uses MaxGraphStates instead.
	DefaultMaxRelationTargets = 1000
	// DefaultMaxBatchSize bounds the checks accepted in one CheckBatch /
	// FilterAuthorized call.
	DefaultMaxBatchSize = 1000
	// DefaultMaxLookupResults bounds the resource IDs one LookupResources returns.
	DefaultMaxLookupResults = 1000
	// DefaultMaxFilterScan = 20 × DefaultMaxBatchSize candidates one FilterPage
	// call may scan.
	DefaultMaxFilterScan = 20000
)

var (
	// ErrInvalidLimits reports an invalid EvaluationLimits field at construction.
	// It is a CONFIG error wrapping sdk.ErrInvalidInput (HTTP 400 / invalid
	// input) — distinct from ErrEvaluationLimit below. Zero is not invalid: it
	// selects the default. There is no implicit unlimited mode.
	ErrInvalidLimits = fmt.Errorf("authorization evaluation limits: %w", sdk.ErrInvalidInput)

	// ErrEvaluationLimit reports that a resolved work budget was exhausted during
	// evaluation. It is the RUNTIME indeterminate outcome (default #9): a caller
	// fails closed and may retry unchanged; it is NEVER a deny and NEVER a
	// complete-but-truncated result. It wraps sdk.ErrUnavailable (HTTP 503 /
	// unavailable) — never a new kind and never sdk.ErrConflict.
	ErrEvaluationLimit = fmt.Errorf("authorization: evaluation budget exhausted: %w", sdk.ErrUnavailable)
)

// EvaluationLimits is the resolved SEMANTIC work budget for one decision
// (Check/CheckBatch/FilterAuthorized) or enumeration (LookupResources). Every
// field is a hard ceiling on a distinct dimension of evaluation cost, sized at
// construction and identical across store dialects — a semantic contract, not a
// per-adapter tuning knob.
//
// Resolution rules (see Resolve):
//   - a ZERO field resolves to its Default*; zero NEVER means unlimited.
//   - a NEGATIVE field is a construction error (ErrInvalidLimits).
//   - step, depth, graph-state and lookup-result bounds must leave room for +1.
//   - an explicit unlimited mode is deliberately absent from v3; adding one
//     would require a separately named opt-in, never a magic zero/negative.
//
// Query count is intentionally NOT a field here: it is observer telemetry with
// an optional adapter-local emergency ceiling, not a cross-store semantic
// budget — optimized SQL and the reference memory graph reach the same decision
// with naturally different query counts.
//
// ENFORCEMENT (AZ3-1.3, complete). Every field is charged per decision by the
// shared per-decision budget (see budget.go); exhaustion of any dimension
// returns ErrEvaluationLimit (indeterminate), never a deny or a truncated list:
//   - MaxThroughDepth: charged per Through hop; the boundary is `>` (depth ==
//     MaxThroughDepth is the last permitted hop).
//   - MaxEvaluationSteps: frames, rules and targets visited by one root decision,
//     including repeats. Expressions charge visited nodes in evaluation order;
//     enumeration also charges discovery and candidate evaluation work.
//   - MaxGraphStates: distinct expanded (resource, permission) states across
//     nested checks; diamonds charge each state once.
//   - MaxRelationTargets: per-hop navigational relation fan-out.
//   - MaxBatchSize: an over-size CheckBatch/FilterAuthorized is rejected before
//     any store call.
//   - MaxLookupResults: each enumerated result set is bounded. Result reads use
//     MaxLookupResults+1 to distinguish overflow from completeness; raw candidate
//     discovery uses the remaining step budget. An overflowing unpaged Lookup
//     returns ErrEvaluationLimit, never a truncated slice presented as complete.
//   - MaxFilterScan: clamps every source request of one FilterPage and ends the
//     call at the bound with a partial page and a continuation — it is the one
//     dimension whose exhaustion is a partial PAGE, not ErrEvaluationLimit,
//     because the continuation makes it resumable.
//
// Cancellation contract: no store call begins after ctx cancellation or budget
// exhaustion is observed.
type EvaluationLimits struct {
	// MaxEvaluationSteps bounds frames, rules and targets visited by one root
	// decision, including repeats (0 selects the default). Expressions preserve
	// ordered short-circuiting; enumeration also charges its discovery work.
	MaxEvaluationSteps int
	// MaxThroughDepth bounds navigational Through recursion (0 -> default). Past
	// the bound (depth > MaxThroughDepth), Check returns ErrEvaluationLimit.
	MaxThroughDepth int
	// MaxGraphStates bounds distinct expanded graph states/edges (0 -> default).
	MaxGraphStates int
	// MaxRelationTargets bounds per-hop relation fan-out (0 -> default).
	MaxRelationTargets int
	// MaxBatchSize bounds one CheckBatch/FilterAuthorized (0 -> default) and
	// the actor-facing purge blast radius.
	MaxBatchSize int
	// MaxLookupResults bounds one LookupResources; enumeration fetches at most
	// MaxLookupResults+1 to distinguish overflow from completeness (0 -> default).
	MaxLookupResults int
	// MaxFilterScan bounds the candidates ONE FilterPage call may pull from its
	// source (0 -> default). Every source request is clamped to the remaining
	// budget, so a call never overshoots it; reaching the bound with the page
	// unfilled returns a PARTIAL page plus a continuation, not ErrEvaluationLimit.
	MaxFilterScan int
}

// Resolve validates the configured limits and returns the effective budget:
// each zero field takes its Default*. Negative fields and bounds that cannot
// represent their +1 sentinel are collected into ErrInvalidLimits in a
// deterministic order. Resolve is pure and performs no I/O.
func (l EvaluationLimits) Resolve() (EvaluationLimits, error) {
	var errs []error
	resolve := func(name string, v, def int) int {
		switch {
		case v < 0:
			errs = append(errs, fmt.Errorf("%s must not be negative, got %d", name, v))
			return def
		case v == 0:
			return def
		default:
			return v
		}
	}
	out := EvaluationLimits{
		MaxEvaluationSteps: resolve("MaxEvaluationSteps", l.MaxEvaluationSteps, DefaultMaxEvaluationSteps),
		MaxThroughDepth:    resolve("MaxThroughDepth", l.MaxThroughDepth, DefaultMaxThroughDepth),
		MaxGraphStates:     resolve("MaxGraphStates", l.MaxGraphStates, DefaultMaxGraphStates),
		MaxRelationTargets: resolve("MaxRelationTargets", l.MaxRelationTargets, DefaultMaxRelationTargets),
		MaxBatchSize:       resolve("MaxBatchSize", l.MaxBatchSize, DefaultMaxBatchSize),
		MaxLookupResults:   resolve("MaxLookupResults", l.MaxLookupResults, DefaultMaxLookupResults),
		MaxFilterScan:      resolve("MaxFilterScan", l.MaxFilterScan, DefaultMaxFilterScan),
	}
	// These dimensions use one extra depth/state/result to detect exhaustion.
	// Reject the unrepresentable sentinel before a store can see an overflowed
	// (negative, potentially unlimited) fetch cap.
	maxInt := int(^uint(0) >> 1)
	for _, field := range []struct {
		name  string
		value int
	}{
		{"MaxEvaluationSteps", out.MaxEvaluationSteps},
		{"MaxThroughDepth", out.MaxThroughDepth},
		{"MaxGraphStates", out.MaxGraphStates},
		{"MaxLookupResults", out.MaxLookupResults},
	} {
		if field.value == maxInt {
			errs = append(errs, fmt.Errorf("%s must be less than %d to allow its +1 sentinel", field.name, maxInt))
		}
	}
	if len(errs) > 0 {
		return EvaluationLimits{}, fmt.Errorf("%w: %w", ErrInvalidLimits, errors.Join(errs...))
	}
	return out, nil
}
