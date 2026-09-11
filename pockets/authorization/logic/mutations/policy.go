package mutations

// GuardianRule requires a minimum number of concrete subjects on a relationship
// after each ordinary relationship command. A userset (group#member) is not a
// concrete anchor. An empty ResourceType applies to every resource type; a zero
// MinAnchors means one. Negative minima are invalid.
// Role commands do not use relationship guardian rules.
type GuardianRule struct {
	ResourceType string
	Relation     string
	MinAnchors   int
}

// GuardianPolicy is the repository's atomic post-state invariant configuration.
// Supply it once at store construction; it never rides a Command. Bundled stores
// default to an empty policy. authorization.NewService validates the repository's
// policy against its relationship model before exposing mutation capabilities.
//
// An ordinary relationship command must leave every configured minimum satisfied.
// A fresh protected scope therefore needs an establishing guardian grant first.
// OpTeardown is the only operation exempt from these minimums.
type GuardianPolicy struct {
	Rules []GuardianRule
}

// DefaultGuardianPolicy is an explicit convenience for hosts protecting one
// concrete owner on every resource type. Stores never install it implicitly.
func DefaultGuardianPolicy() GuardianPolicy {
	return GuardianPolicy{Rules: []GuardianRule{{Relation: "owner", MinAnchors: 1}}}
}

// MinDirectAnchors returns the minimum number of DIRECT anchors the policy
// requires for (resourceType, relation) — the largest applicable rule minimum, or
// 0 when the relation is unprotected on that resource type.
func (p GuardianPolicy) MinDirectAnchors(resourceType, relation string) int {
	n := 0
	for _, r := range p.Rules {
		if r.Relation != relation {
			continue
		}
		if r.ResourceType != "" && r.ResourceType != resourceType {
			continue
		}
		m := r.MinAnchors
		if m < 1 {
			m = 1
		}
		if m > n {
			n = m
		}
	}
	return n
}

// Protects reports whether (resourceType, relation) carries a guardian minimum.
func (p GuardianPolicy) Protects(resourceType, relation string) bool {
	return p.MinDirectAnchors(resourceType, relation) > 0
}
