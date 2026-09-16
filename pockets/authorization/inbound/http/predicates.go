package authorizationhttp

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type predicateKind uint8

const (
	predicateAll predicateKind = iota + 1
	predicateAny
	predicateRole
	predicateRelationship
	predicatePermission
)

// Target is an immutable global scope or resource input for a route policy.
// Reuse a resource Target to resolve that input once per request. Its zero value
// is invalid. Custom resolvers must be safe for concurrent requests.
type Target struct{ value *guardTarget }

type guardTarget struct {
	global       bool
	resourceType string
	fixed        *authmodel.Resource
	resolve      ResourceResolver
	err          error
}

// Predicate describes a deferred authorization check. All and Any compose
// predicates; Require validates the complete policy when the route is mounted.
type Predicate struct {
	kind     predicateKind
	label    string
	target   Target
	children []Predicate
}

// Global selects exact global membership. It is valid only with HasRole.
func Global() Target { return Target{value: &guardTarget{global: true}} }

// Resource declares a type and resolves its concrete resource lazily. The result
// must have the declared type. Resolvers should be context-aware, read-only input
// readers: they run inside the authorization operation, but their own datastore
// reads do not automatically join its tuple snapshot. A resolver using the same
// connection pool must leave capacity for this input read while the snapshot is
// open. Returning ErrAlternativeNotApplicable makes the reached predicate false.
func Resource(resourceType string, resolve ResourceResolver) Target {
	return Target{value: &guardTarget{resourceType: resourceType, resolve: resolve}}
}

// Fixed selects a known resource. Its coordinates are validated at mount.
func Fixed(resourceType, resourceID string) Target {
	return Target{value: &guardTarget{resourceType: resourceType, fixed: &authmodel.Resource{Type: resourceType, ID: resourceID}}}
}

// Path selects the resource named by a route parameter. A missing parameter
// fails the request closed; an empty parameter name is rejected at mount.
func Path(resourceType, parameter string) Target {
	target := Resource(resourceType, func(r *http.Request) (authmodel.Resource, error) {
		id := web.Param(r, parameter)
		if id == "" {
			return authmodel.Resource{}, fmt.Errorf("authorization: path parameter %q is empty (does the route pattern name it?)", parameter)
		}
		return authmodel.Resource{Type: resourceType, ID: id}, nil
	})
	if err := tuples.ValidateRefField("path parameter", parameter); err != nil {
		target.value.err = err
	}
	return target
}

// HasRole requires an exact concrete-subject grant at the supplied target.
// It requires no role catalog or graph model and performs no userset expansion.
func HasRole(label string, target Target) Predicate {
	return Predicate{kind: predicateRole, label: label, target: target}
}

// HasRelationship requires membership in a declared relation, including
// model-permitted userset expansion (decisions.Direct semantics).
func HasRelationship(label string, resource Target) Predicate {
	return Predicate{kind: predicateRelationship, label: label, target: resource}
}

// Can requires a declared permission, including its named traversal rules.
func Can(permission string, resource Target) Predicate {
	return Predicate{kind: predicatePermission, label: permission, target: resource}
}

// All requires every predicate, stopping at the first false result or error.
// An empty group is rejected at mount.
func All(predicates ...Predicate) Predicate {
	return Predicate{kind: predicateAll, children: append([]Predicate{}, predicates...)}
}

// Any requires at least one predicate, stopping at the first true result or
// error. An empty group is rejected at mount.
func Any(predicates ...Predicate) Predicate {
	return Predicate{kind: predicateAny, children: append([]Predicate{}, predicates...)}
}

func lowerPredicate(predicate Predicate) (decisions.Expression, map[string]ResourceResolver, error) {
	resolvers := make(map[string]ResourceResolver)
	slots := make(map[*guardTarget]string)
	nodes := 0
	var lower func(Predicate, int) (decisions.Expression, error)
	lower = func(p Predicate, depth int) (decisions.Expression, error) {
		nodes++
		if depth > decisions.MaxExpressionDepth || nodes > decisions.MaxExpressionNodes {
			return decisions.Expression{}, authmodel.ErrEvaluationLimit
		}
		invalid := func(message string) (decisions.Expression, error) {
			return decisions.Expression{}, fmt.Errorf("authorization predicate: %s: %w", message, sdk.ErrInvalidInput)
		}
		if p.kind == predicateAll || p.kind == predicateAny {
			if len(p.children) == 0 {
				return invalid("All and Any require at least one predicate")
			}
			if len(p.children) > decisions.MaxExpressionNodes-nodes {
				return decisions.Expression{}, authmodel.ErrEvaluationLimit
			}
			children := make([]decisions.Expression, len(p.children))
			for i, child := range p.children {
				var err error
				children[i], err = lower(child, depth+1)
				if err != nil {
					return decisions.Expression{}, err
				}
			}
			if p.kind == predicateAll {
				return decisions.All(children...), nil
			}
			return decisions.Any(children...), nil
		}
		var expression decisions.Expression
		switch p.kind {
		case predicateRole:
			expression = decisions.Role(p.label)
		case predicateRelationship:
			expression = decisions.Direct(p.label)
		case predicatePermission:
			expression = decisions.Permission(p.label)
		default:
			return invalid("uninitialized predicate")
		}
		target := p.target.value
		if target == nil {
			return invalid("uninitialized target")
		}
		if target.err != nil {
			return decisions.Expression{}, target.err
		}
		if target.global {
			if p.kind != predicateRole {
				return invalid("Global is valid only for HasRole")
			}
			return expression, nil
		}
		if target.fixed != nil {
			return decisions.On(*target.fixed, expression), nil
		}
		if target.resolve == nil {
			return invalid("resource target requires a resolver")
		}
		key, exists := slots[target]
		if !exists {
			key = "resource_" + strconv.Itoa(len(slots))
			slots[target] = key
			resolvers[key] = target.resolve
		}
		return decisions.BindResource(target.resourceType, key, expression), nil
	}
	expression, err := lower(predicate, 0)
	return expression, resolvers, err
}
