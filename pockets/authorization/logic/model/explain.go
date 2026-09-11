package model

// Explain step kinds — the coarse shape of the rule/path decision a step records.
const (
	// ExplainKindDirect is a direct-relation check on the current resource.
	ExplainKindDirect = "direct"
	// ExplainKindThrough is a Through traversal of a relation to another resource.
	ExplainKindThrough = "through"
	// ExplainKindRole is a roles-kind grantor probe at the request's scope.
	ExplainKindRole = "role"
)

// Explain step scopes — WHERE a role step's grant was found. The values are the
// provenance vocabulary the roles kind already ships (role.EffectiveGrant's
// direct/global labels), not a second vocabulary.
const (
	// ExplainScopeDirect — the role was found at the exact resource scope.
	ExplainScopeDirect = "direct"
	// ExplainScopeGlobal — the role was found as the ("", "") global assignment.
	ExplainScopeGlobal = "global"
)

// ExplainStep is one coarse rule/path decision recorded during a traced Check. It
// carries SCHEMA-level identifiers and a stable Outcome code ONLY — never a raw
// infrastructure error string, a stack trace, or any secret. A step is emitted
// as each schema check on the evaluated path resolves; nested Through steps are
// recorded before the parent step that traversed into them.
type ExplainStep struct {
	// ResourceType, ResourceID, and Permission name the (resource, permission)
	// state whose rule this step evaluated.
	ResourceType string
	ResourceID   string
	Permission   string
	// Relation is the direct relation examined (Kind == ExplainKindDirect) or the
	// relation traversed (Kind == ExplainKindThrough).
	Relation string
	Kind     string
	// Depth is the number of Through hops taken to reach this state (0 at the top
	// resource).
	Depth int
	// Outcome is the stable coarse result of this check: ReasonGranted or
	// ReasonDenied.
	Outcome Reason
	// Role and Scope are set for ExplainKindRole steps:
	// the role probed and where it was found (ExplainScopeDirect/Global; "" when
	// not held). Empty for relationship steps.
	Role  string
	Scope string
}

// Explanation is the opt-in, bounded trace returned beside a CheckExplain
// decision. Decision equals the CheckResult.ReasonCode (ReasonGranted or
// ReasonDenied); Steps are the coarse rule/path decisions the engine took. It
// records NO raw infrastructure error: on an evaluation-limit or store failure

type Explanation struct {
	Decision Reason
	Steps    []ExplainStep
}
