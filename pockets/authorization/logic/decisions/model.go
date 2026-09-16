package decisions

import (
	"slices"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// Model declares optional graph shapes and named permission expressions.
// Exact role predicates do not require a model or a role catalog.
type Model struct {
	ResourceTypes  map[string]ResourceTypeDef
	assemblyErrors []string
}
type ResourceTypeDef struct {
	Relations   map[string]RelationDef
	Permissions map[string]Expression
}
type RelationDef struct{ AllowedSubjects []SubjectTypeRef }
type SubjectTypeRef struct{ Type, Relation string }

// Expression is one operation. Exactly one of AnyOf, AllOf, Relation, Through,
// RoleName or NamedPermission must be set. Constructors avoid ambiguous values.
// Child order is evaluation order; compiling snapshots every child and resource.
type Expression struct {
	AnyOf           []Expression
	AllOf           []Expression
	Relation        string
	Through         string
	Permission      string
	RoleName        string
	CurrentResource bool
	Resource        *authmodel.Resource
	ResourceSlot    *ResourceSlot `json:",omitempty"`
	NamedPermission string
	remove          bool
	invalidSelector bool
}

func Direct(relation string) Expression { return Expression{Relation: relation} }
func Through(relation, permission string) Expression {
	return Expression{Through: relation, Permission: permission}
}
func Any(expressions ...Expression) Expression {
	return Expression{AnyOf: append([]Expression{}, expressions...)}
}
func All(expressions ...Expression) Expression {
	return Expression{AllOf: append([]Expression{}, expressions...)}
}
func Role(label string) Expression { return Expression{RoleName: label} }

// RoleIn names the current check resource, or the one explicitly supplied.
// More than one resource creates an invalid expression rejected before any I/O.
func RoleIn(label string, resources ...authmodel.Resource) Expression {
	e := Expression{RoleName: label, CurrentResource: true}
	if len(resources) == 1 {
		r := resources[0]
		e.CurrentResource = false
		e.Resource = &r
	}
	if len(resources) > 1 {
		e.AnyOf = []Expression{}
	}
	return e
}
func Permission(name string) Expression          { return Expression{NamedPermission: name} }
func AnyOf(expressions ...Expression) Expression { return Any(expressions...) }
func Remove() Expression                         { return Expression{remove: true} }
func (e Expression) IsRemove() bool              { return e.remove }

type ResourceSchema struct {
	Name string
	Def  ResourceTypeDef
}

func copyExpression(e Expression) Expression {
	nodes := 0
	var copyNode func(Expression, int) Expression
	copyNode = func(e Expression, depth int) Expression {
		nodes++
		if depth > maxExpressionDepth || nodes > maxExpressionNodes {
			return Expression{AnyOf: []Expression{}}
		}
		e.AnyOf = slices.Clone(e.AnyOf)
		e.AllOf = slices.Clone(e.AllOf)
		for i := range e.AnyOf {
			e.AnyOf[i] = copyNode(e.AnyOf[i], depth+1)
		}
		for i := range e.AllOf {
			e.AllOf[i] = copyNode(e.AllOf[i], depth+1)
		}
		if e.Resource != nil {
			r := *e.Resource
			e.Resource = &r
		}
		if e.ResourceSlot != nil {
			slot := *e.ResourceSlot
			e.ResourceSlot = &slot
		}
		return e
	}
	return copyNode(e, 0)
}
func expressionLeaves(e Expression) []Expression {
	out := []Expression{}
	nodes := 0
	var visit func(Expression, int)
	visit = func(e Expression, depth int) {
		nodes++
		if depth > maxExpressionDepth || nodes > maxExpressionNodes {
			return
		}
		children := e.AnyOf
		if children == nil {
			children = e.AllOf
		}
		if children == nil {
			out = append(out, e)
			return
		}
		for _, child := range children {
			visit(child, depth+1)
		}
	}
	visit(e, 0)
	return out
}

// graphChecks is a semantics-preserving optimization only for a flat ordered
// disjunction of graph leaves. Every other expression uses the same general walk.
func graphChecks(e Expression) []Expression {
	checks := e.AnyOf
	if checks == nil {
		checks = []Expression{e}
	}
	for _, c := range checks {
		if c.Relation == "" && c.Through == "" {
			return nil
		}
		if c.AnyOf != nil || c.AllOf != nil {
			return nil
		}
	}
	return checks
}
