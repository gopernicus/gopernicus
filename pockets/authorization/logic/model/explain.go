package model

import "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

// Explain step kinds — the coarse shape of the rule/path decision a step records.
const (
	// ExplainKindDirect is a direct-relation check on the current resource.
	ExplainKindDirect = "direct"
	// ExplainKindThrough is a Through traversal of a relation to another resource.
	ExplainKindThrough = "through"
	// ExplainKindExact is an exact concrete tuple membership check.
	ExplainKindExact = "exact"
)

// ExplainStep is one coarse rule/path decision recorded during a traced Check.
// It includes identifiers and a stable Outcome, never a raw infrastructure
// error, stack trace or secret. A step is emitted
// as each schema check on the evaluated path resolves; nested Through steps are
// recorded before the parent step that traversed into them.
type ExplainStep struct {
	// ResourceType, ResourceID, and Permission name the (resource, permission)
	// state whose rule this step evaluated.
	ResourceType string
	ResourceID   string
	Permission   string
	// Relation is the direct relation examined (Kind == ExplainKindDirect) or the
	// relation traversed (Kind == ExplainKindThrough), or the exact label probed
	// (Kind == ExplainKindExact).
	Relation string
	Kind     string
	// Depth is the number of Through hops taken to reach this state (0 at the top
	// resource).
	Depth int
	// Outcome is the stable coarse result of this check: ReasonGranted or
	// ReasonDenied.
	Outcome Reason
	// Scope is the explicit scope examined, whether granted or denied. For graph
	// steps it is the resource whose relation is checked or traversed.
	Scope tuples.Scope
}

// Explanation is the opt-in, bounded trace returned beside a CheckExplain
// decision. Decision equals the CheckResult.ReasonCode (ReasonGranted or
// ReasonDenied); Steps are the coarse rule/path decisions the engine took. It
// may retain partial steps on an evaluation-limit or store failure. Cancellation
// and snapshot completion failures discard the provisional decision and trace.
type Explanation struct {
	Decision Reason
	Steps    []ExplainStep
}
