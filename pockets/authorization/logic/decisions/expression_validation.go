package decisions

import (
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// MaxExpressionDepth and MaxExpressionNodes bound validation and configuration
// copying, independently of the runtime evaluation work budget.
const MaxExpressionDepth = 64
const MaxExpressionNodes = 4096
const maxExpressionDepth = MaxExpressionDepth
const maxExpressionNodes = MaxExpressionNodes

type expressionDeclarations struct {
	resource   func(string) bool
	subjects   func(string, string) ([]SubjectTypeRef, bool)
	permission func(string, string) bool
}

func validateExpression(expr Expression, model Model, resourceType string, adhoc bool) error {
	declarations := expressionDeclarations{
		resource: func(rt string) bool { _, ok := model.ResourceTypes[rt]; return ok },
		subjects: func(rt, relation string) ([]SubjectTypeRef, bool) {
			r, ok := model.ResourceTypes[rt].Relations[relation]
			return r.AllowedSubjects, ok
		},
		permission: func(rt, p string) bool { _, ok := model.ResourceTypes[rt].Permissions[p]; return ok },
	}
	_, err := validateExpressionTree(expr, declarations, resourceType, adhoc)
	return err
}

// ValidateExpression checks every branch and declaration without resolving
// runtime inputs or reading tuples. It accepts well-formed runtime slots.
func (s *Service) ValidateExpression(expr Expression) error {
	_, err := s.validateAdhocExpression(expr)
	return err
}

func (s *Service) validateAdhocExpression(expr Expression) (bool, error) {
	if s == nil || s.compiled == nil {
		return false, fmt.Errorf("nil decision service: %w", sdk.ErrInvalidInput)
	}
	declarations := expressionDeclarations{
		resource: func(rt string) bool { _, ok := s.compiled.resourceTypes[rt]; return ok },
		subjects: func(rt, relation string) ([]SubjectTypeRef, bool) {
			subjects, _, ok := s.compiled.relationSubjects(rt, relation)
			return subjects, ok
		},
		permission: s.compiled.declaresPermission,
	}
	return validateExpressionTree(expr, declarations, "", true)
}

func validateExpressionTree(expr Expression, declarations expressionDeclarations, resourceType string, adhoc bool) (bool, error) {
	nodes := 0
	slots := map[string]string{}
	invalid := func(msg string) error { return fmt.Errorf("expression %s: %w", msg, sdk.ErrInvalidInput) }
	var visit func(Expression, int) error
	visit = func(e Expression, depth int) error {
		nodes++
		if depth > MaxExpressionDepth || nodes > MaxExpressionNodes {
			return authmodel.ErrEvaluationLimit
		}
		kinds := 0
		for _, set := range []bool{e.AnyOf != nil, e.AllOf != nil, e.Relation != "", e.Through != "", e.RoleName != "", e.NamedPermission != ""} {
			if set {
				kinds++
			}
		}
		if kinds != 1 || e.remove {
			return invalid("must name exactly one operation")
		}
		if e.Through == "" && e.Permission != "" {
			return invalid("permission requires Through")
		}
		selectors := 0
		for _, set := range []bool{e.CurrentResource, e.Resource != nil, e.ResourceSlot != nil} {
			if set {
				selectors++
			}
		}
		if selectors > 1 || e.invalidSelector {
			return invalid("has two resource selectors")
		}
		if !adhoc && e.ResourceSlot != nil {
			return invalid("runtime resource slots are not allowed in named models")
		}
		if !adhoc && e.RoleName == "" && selectors > 0 {
			return invalid("exact resource selector requires RoleIn in named models")
		}
		if e.CurrentResource && e.RoleName == "" {
			return invalid("current resource selector requires RoleIn")
		}
		if e.AnyOf != nil || e.AllOf != nil {
			if selectors != 0 {
				return invalid("resource selectors require a leaf")
			}
			children := e.AnyOf
			if children == nil {
				children = e.AllOf
			}
			if len(children) == 0 {
				return invalid("cannot have empty All or Any")
			}
			for _, child := range children {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		targetType := resourceType
		if e.Resource != nil {
			if err := tuples.On(e.Resource.Type, e.Resource.ID).Validate(); err != nil {
				return err
			}
			targetType = e.Resource.Type
		}
		if e.ResourceSlot != nil {
			slot := *e.ResourceSlot
			if err := tuples.ValidateRefField("resource slot type", slot.Type); err != nil {
				return err
			}
			if err := tuples.ValidateRefField("resource slot key", slot.Key); err != nil {
				return err
			}
			if previous, ok := slots[slot.Key]; ok && previous != slot.Type {
				return invalid("resource slot has conflicting types")
			}
			slots[slot.Key] = slot.Type
			targetType = slot.Type
		}
		if e.RoleName != "" {
			if err := tuples.ValidateRefField("role", e.RoleName); err != nil {
				return err
			}
			if e.CurrentResource && resourceType == "" {
				return invalid("current resource is unavailable")
			}
			return nil
		}
		if targetType == "" {
			return invalid("graph or named permission needs a resource")
		}
		if !declarations.resource(targetType) {
			return invalid("resource type is undeclared")
		}
		if e.NamedPermission != "" {
			if err := tuples.ValidateRefField("permission", e.NamedPermission); err != nil {
				return err
			}
			if !declarations.permission(targetType, e.NamedPermission) {
				return invalid("named permission is undeclared")
			}
			return nil
		}
		relation := e.Relation
		if relation == "" {
			relation = e.Through
		}
		if err := tuples.ValidateRefField("relation", relation); err != nil {
			return err
		}
		subjects, ok := declarations.subjects(targetType, relation)
		if !ok {
			return invalid("relation is undeclared")
		}
		if e.Through != "" {
			if err := tuples.ValidateRefField("permission", e.Permission); err != nil {
				return err
			}
			targets := 0
			var missing []string
			for _, subject := range subjects {
				declared := declarations.resource(subject.Type)
				if adhoc && (subject.Relation != "" || !declared) {
					return invalid("Through must contain concrete resource subjects only")
				}
				if !declared {
					continue
				}
				targets++
				if !declarations.permission(subject.Type, e.Permission) {
					missing = append(missing, subject.Type)
				}
			}
			if len(missing) > 0 {
				return invalid(fmt.Sprintf("Through permission %q not found on target type(s) %v", e.Permission, dedupeSortStrings(missing)))
			}
			if targets == 0 {
				return invalid("Through has no resource targets")
			}
		}
		return nil
	}
	err := visit(expr, 0)
	return len(slots) > 0, err
}
