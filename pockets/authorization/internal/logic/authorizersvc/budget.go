package authorizersvc

// stateKey identifies one expanded evaluation state — a (resource, permission)
// pair. Distinct states are the MaxGraphStates dimension of the work budget and
// the path-local cycle key for recursive Check.
type stateKey struct {
	resourceType string
	resourceID   string
	permission   string
}

// budget is the ONE per-decision semantic work ledger, shared across every
// nested Check/Lookup call of a single top-level decision. It charges the
// resolved EvaluationLimits' cross-store semantic dimensions:
//
//   - MaxGraphStates    — distinct expanded (resource, permission) states.
//   - MaxRelationTargets — per-hop relation fan-out (targets / expanded members).
//   - MaxLookupResults   — enumerated distinct result IDs.
//
// MaxThroughDepth is charged by the recursion depth counter at the call sites
// (it is a stack property, not a ledger tally). Exhaustion of ANY dimension is
// ErrEvaluationLimit (indeterminate) — never a deny, never a truncated result.
//
// A budget is NOT safe for concurrent use: one decision evaluates on one
// goroutine. Query count is deliberately absent — it is adapter-local telemetry
// with an optional store-side emergency ceiling, not a cross-store semantic
// budget (see EvaluationLimits).
type budget struct {
	limits EvaluationLimits
	states map[stateKey]struct{}

	// trace is the OPTIONAL explain collector. It is nil for an ordinary decision
	// and non-nil only for a CheckExplain call. It rides the SAME evaluation path
	// (there is no second evaluator): the engine appends coarse rule/path steps to
	// it, and because it hangs off the one per-decision budget it inherently shares
	// that budget — an explain cannot spend more work or reach a different
	// decision than the plain Check it mirrors.
	trace *explainTrace
}

func newBudget(limits EvaluationLimits) *budget {
	return &budget{limits: limits, states: make(map[stateKey]struct{})}
}

// record appends one coarse rule/path step to the explain trace when tracing is
// enabled; it is a no-op for an ordinary decision. It is nil-safe on both the
// budget and the trace so every call site can record unconditionally.
func (b *budget) record(step ExplainStep) {
	if b == nil || b.trace == nil {
		return
	}
	b.trace.steps = append(b.trace.steps, step)
}

// chargeState records a distinct (resource, permission) expansion state. A state
// already charged on ANY path is free — the diamond property: reconvergent paths
// never re-spend the graph-state budget. The first sighting of a new state once
// MaxGraphStates are already charged is ErrEvaluationLimit.
func (b *budget) chargeState(resourceType, resourceID, permission string) error {
	k := stateKey{resourceType, resourceID, permission}
	if _, seen := b.states[k]; seen {
		return nil
	}
	if len(b.states) >= b.limits.MaxGraphStates {
		return ErrEvaluationLimit
	}
	b.states[k] = struct{}{}
	return nil
}

// chargeFanout bounds one relation hop's fan-out (the count of relation targets
// or expanded members observed at that hop). A hop wider than MaxRelationTargets
// is ErrEvaluationLimit.
func (b *budget) chargeFanout(n int) error {
	if n > b.limits.MaxRelationTargets {
		return ErrEvaluationLimit
	}
	return nil
}

// resultFetchCap is the per-store-call row cap the engine passes to the bounded
// Lookup* reader methods: MaxLookupResults+1, so a store returning a full cap is
// a distinguishable overflow the engine reports rather than truncating silently.
func (b *budget) resultFetchCap() int {
	return b.limits.MaxLookupResults + 1
}

// resultsOverflow reports whether a single enumeration node's distinct-ID count
// has exceeded MaxLookupResults. The result cap is applied PER enumeration node
// (each lookupResources/lookupThrough set), not as a shared running total: an
// intermediate Through enumeration and the final result are each bounded, and
// because MaxLookupResults bounds every node, it bounds the top-level result too
// — without a nested lookup double-charging the parent's budget.
func (b *budget) resultsOverflow(count int) bool {
	return count > b.limits.MaxLookupResults
}
