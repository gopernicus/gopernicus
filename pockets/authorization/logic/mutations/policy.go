package mutations

import (
	"maps"
	"slices"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// IntegrityRule requires a minimum number of concrete subjects on a relationship
// after each ordinary canonical command. A userset (group#member) is not a
// concrete subject. An empty ResourceType applies to every resource type; a zero
// MinSubjects means one. Negative minima are invalid.
// Role and relationship facades enforce the same canonical integrity counts.
type IntegrityRule struct {
	ResourceType string
	Relation     string
	MinSubjects  int
}

// IntegrityPolicy is the repository's atomic post-state invariant configuration.
// Supply it once at store construction; it never rides a Command. Bundled stores
// default to an empty policy. mutations.NewService validates the repository's
// policy against its relationship model before exposing mutation capabilities.
//
// An ordinary canonical command must leave every configured minimum satisfied.
// A fresh protected scope therefore needs an grant establishing the minimum first.
// OpTeardown is the only operation exempt from these minimums.
type IntegrityPolicy struct {
	Rules []IntegrityRule
}

// DefaultIntegrityPolicy is an explicit convenience for hosts protecting one
// concrete owner on every resource type. Stores never install it implicitly.
// Its rule is unscoped, so it assumes every modelled type with relations
// declares owner; beside a model that has a type without it, authorization.New
// refuses — use DefaultIntegrityPolicyFor.
func DefaultIntegrityPolicy() IntegrityPolicy {
	return IntegrityPolicy{Rules: []IntegrityRule{{Relation: "owner", MinSubjects: 1}}}
}

// DefaultIntegrityPolicyFor builds the default owner policy from a model: one
// explicit {type, owner, 1} rule per modelled type that declares an owner
// relation, so a type without owner is left alone. Unlike the unscoped
// DefaultIntegrityPolicy it does not cover unmodelled (opaque) types or types
// whose owner is a permission only; add an explicit rule for each. Compose
// further rules with p.Rules = append(p.Rules, ...).
func DefaultIntegrityPolicyFor(model decisions.Model) IntegrityPolicy {
	var p IntegrityPolicy
	for _, rt := range slices.Sorted(maps.Keys(model.ResourceTypes)) {
		if _, ok := model.ResourceTypes[rt].Relations["owner"]; ok {
			p.Rules = append(p.Rules, IntegrityRule{ResourceType: rt, Relation: "owner", MinSubjects: 1})
		}
	}
	return p
}

// MinDirectSubjects returns the minimum number of distinct concrete subjects the policy
// requires for (resourceType, relation) — the largest applicable rule minimum, or
// 0 when the relation is unprotected on that resource type.
func (p IntegrityPolicy) MinDirectSubjects(resourceType, relation string) int {
	n := 0
	for _, r := range p.Rules {
		if r.Relation != relation {
			continue
		}
		if r.ResourceType != "" && r.ResourceType != resourceType {
			continue
		}
		m := r.MinSubjects
		if m < 1 {
			m = 1
		}
		if m > n {
			n = m
		}
	}
	return n
}

// Protects reports whether (resourceType, relation) carries an integrity minimum.
func (p IntegrityPolicy) Protects(resourceType, relation string) bool {
	return p.MinDirectSubjects(resourceType, relation) > 0
}

// Validate checks the policy configuration independently of any store or model.
func (p IntegrityPolicy) Validate() error { return validateIntegrityPolicy(p) }

// ValidateState checks the complete post-state of one addressed scope. Exact
// duplicate facts count once; usersets cannot satisfy a concrete-subject minimum.
func (p IntegrityPolicy) ValidateState(scope tuples.Scope, facts []tuples.Tuple) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	unique := make(map[tuples.Tuple]struct{}, len(facts))
	for _, fact := range facts {
		if err := fact.Validate(); err != nil {
			return err
		}
		if fact.Scope != scope {
			return ErrInvalidCommand
		}
		unique[fact] = struct{}{}
	}
	if scope.Kind == tuples.GlobalScope {
		return nil
	}
	for _, rule := range p.Rules {
		if rule.ResourceType != "" && rule.ResourceType != scope.Type {
			continue
		}
		n := 0
		for f := range unique {
			if f.Relation == rule.Relation && !f.Subject.IsUserset() {
				n++
			}
		}
		if n < p.MinDirectSubjects(scope.Type, rule.Relation) {
			return ErrInvariantBlocked
		}
	}
	return nil
}
