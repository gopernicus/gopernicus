package mutation

// GuardianRule declares a protected relation on a resource type: after any
// ordinary command on a resource of ResourceType, at least MinAnchors DIRECT
// anchors of Relation must remain (default #10 / AZ3-3.2). A DIRECT anchor is an
// exact concrete [github.com/gopernicus/gopernicus/features/authorization/domain/relationship.SubjectRef]
// with an EMPTY userset relation — a `group#member` owner is NOT a direct anchor,
// so a group-expanded count can never mask the loss of the final direct guardian.
// An empty ResourceType protects Relation on EVERY resource type. A zero
// MinAnchors resolves to one.
//
// The vocabulary is RELATIONSHIP-ONLY: a direct anchor is a relationship subject,
// so a rule names a relationship Relation, and the invariant is consulted only by
// the relationship command families (grant, revoke, replace, ordinary
// purge/batch). Role operations are NOT guardian-bearing in v3 and there is no way
// to declare a role as one: a role is an opaque string with no direct-anchor
// notion (default #5 keeps roles opaque, with no catalog or implication), so a
// "minimum N holders of role R" invariant would require role semantics v3
// deliberately excludes. The ratified default #10 protected set is the
// relationship `owner`; no host wires a guardian-bearing role, so role families
// pass the invariant vacuously. Should a role-minimum ever be required it is an
// honest new packet (a RoleGuardianRule with its own anchor definition), not an
// overload of this relationship rule.
type GuardianRule struct {
	ResourceType string
	Relation     string
	MinAnchors   int
}

// GuardianPolicy is the set of protected-relation invariants a
// [MutationRepository] enforces UNDER ITS SCOPE LOCK, as a post-state rule, for
// every ordinary command on a configured protected resource type. It is the
// invariant input supplied at repository CONSTRUCTION: it is POLICY, not payload,
// and never rides a [Command]. An empty policy declares no invariant. All three
// stores (reference memstore, pgx, turso) take the same shape so the guardian
// contract is mirrored operationally rather than re-derived per dialect.
//
// The post-state rule uniformly enforces the "establish first" contract: because
// it requires the protected relation's minimum in the state AFTER the command, a
// member/role-first command on a fresh protected resource (which would leave zero
// direct anchors) is blocked, while the establishing owner grant (which brings the
// count up to the minimum) is allowed. A legacy orphan scope — rows present, no
// direct guardian — is likewise blocked for ordinary mutation until an
// owner-establishing grant repairs it. Only [OpTeardown] is exempt (it is allowed
// to zero a protected scope); its capability gating lives in the service layer.
type GuardianPolicy struct {
	Rules []GuardianRule
}

// DefaultGuardianPolicy protects the `owner` relation on every resource type with
// a minimum of one direct anchor — the ratified default #10 protected set. A host
// narrows it (e.g. to specific resource types) or clears it by supplying its own
// policy at repository construction.
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
