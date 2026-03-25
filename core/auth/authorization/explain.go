package authorization

import (
	"context"
	"fmt"
)

// =============================================================================
// Explain Types
// =============================================================================

// ExplainResult contains the detailed explanation of a permission check.
type ExplainResult struct {
	Request CheckRequest
	Allowed bool
	Reason  string
	Steps   []TraversalStep
}

// TraversalStep represents a single step in the permission check traversal.
type TraversalStep struct {
	Type       StepType
	Resource   Resource
	Relation   string
	Permission string
	Subject    Subject
	Result     StepResult
	Message    string
}

// StepType describes the type of check performed.
type StepType string

const (
	StepPlatformAdmin   StepType = "platform_admin"
	StepSelfAccess      StepType = "self_access"
	StepDirectRelation  StepType = "direct_relation"
	StepThroughRelation StepType = "through_relation"
	StepGroupExpansion  StepType = "group_expansion"
)

// StepResult indicates the outcome of a traversal step.
type StepResult string

const (
	StepGranted   StepResult = "granted"
	StepDenied    StepResult = "denied"
	StepSkipped   StepResult = "skipped"
	StepContinued StepResult = "continued"
)

// =============================================================================
// CheckExplain
// =============================================================================

// CheckExplain performs a permission check and returns detailed traversal
// information. Use this for debugging permission issues.
func (a *Authorizer) CheckExplain(ctx context.Context, req CheckRequest) (ExplainResult, error) {
	result := ExplainResult{
		Request: req,
		Steps:   make([]TraversalStep, 0),
	}

	// Platform admin check.
	isAdmin, err := a.checkPlatformAdmin(ctx, req.Subject)
	if err != nil {
		return result, err
	}
	result.Steps = append(result.Steps, TraversalStep{
		Type:    StepPlatformAdmin,
		Subject: req.Subject,
		Result:  boolToResult(isAdmin),
		Message: fmt.Sprintf("checking platform:main#admin@%s:%s", req.Subject.Type, req.Subject.ID),
	})
	if isAdmin {
		result.Allowed = true
		result.Reason = "platform:admin"
		return result, nil
	}

	// Self-access check.
	selfAllowed, selfReason := a.checkSelf(req)
	if req.Resource.Type == req.Subject.Type && req.Resource.ID == req.Subject.ID {
		result.Steps = append(result.Steps, TraversalStep{
			Type:     StepSelfAccess,
			Resource: req.Resource,
			Subject:  req.Subject,
			Result:   boolToResult(selfAllowed),
			Message:  fmt.Sprintf("checking self-access for %s on %s:%s", req.Permission, req.Resource.Type, req.Resource.ID),
		})
	}
	if selfAllowed {
		result.Allowed = true
		result.Reason = selfReason
		return result, nil
	}

	// Schema rules.
	rules := a.getPermissionRules(req.Resource.Type, req.Permission)
	if rules.AnyOf == nil {
		result.Steps = append(result.Steps, TraversalStep{
			Type:       StepDirectRelation,
			Resource:   req.Resource,
			Permission: req.Permission,
			Result:     StepDenied,
			Message:    fmt.Sprintf("no rules defined for %s#%s", req.Resource.Type, req.Permission),
		})
		result.Allowed = false
		result.Reason = "no rules defined"
		return result, nil
	}

	visited := make(map[string]bool)
	allowed, reason, steps, err := a.checkPermissionExplain(ctx, req, rules, 0, visited)
	if err != nil {
		return result, err
	}

	result.Steps = append(result.Steps, steps...)
	result.Allowed = allowed
	result.Reason = reason
	return result, nil
}

func (a *Authorizer) checkPermissionExplain(
	ctx context.Context,
	req CheckRequest,
	rules PermissionRule,
	depth int,
	visited map[string]bool,
) (bool, string, []TraversalStep, error) {
	var steps []TraversalStep

	if depth > a.config.MaxTraversalDepth {
		steps = append(steps, TraversalStep{
			Type:     StepDirectRelation,
			Resource: req.Resource,
			Result:   StepDenied,
			Message:  "max depth exceeded (possible cycle)",
		})
		return false, "max depth exceeded", steps, nil
	}

	visitKey := fmt.Sprintf("%s:%s#%s", req.Resource.Type, req.Resource.ID, req.Permission)
	if visited[visitKey] {
		steps = append(steps, TraversalStep{
			Type:     StepDirectRelation,
			Resource: req.Resource,
			Result:   StepDenied,
			Message:  fmt.Sprintf("cycle detected at %s", visitKey),
		})
		return false, "cycle detected", steps, nil
	}
	visited[visitKey] = true

	for _, check := range rules.AnyOf {
		if check.Through != "" {
			allowed, reason, throughSteps, err := a.checkThroughExplain(ctx, req, check, depth, visited)
			steps = append(steps, throughSteps...)
			if err != nil {
				return false, "", steps, err
			}
			if allowed {
				return true, reason, steps, nil
			}
		} else {
			allowed, err := a.store.CheckRelationWithGroupExpansion(
				ctx, req.Resource.Type, req.Resource.ID, check.Relation, req.Subject.Type, req.Subject.ID,
			)
			if err != nil {
				return false, "", steps, err
			}

			steps = append(steps, TraversalStep{
				Type:     StepDirectRelation,
				Resource: req.Resource,
				Relation: check.Relation,
				Subject:  req.Subject,
				Result:   boolToResult(allowed),
				Message:  fmt.Sprintf("checking %s:%s#%s@%s:%s", req.Resource.Type, req.Resource.ID, check.Relation, req.Subject.Type, req.Subject.ID),
			})

			if allowed {
				return true, fmt.Sprintf("direct:%s", check.Relation), steps, nil
			}
		}
	}

	return false, "no matching rule", steps, nil
}

func (a *Authorizer) checkThroughExplain(
	ctx context.Context,
	req CheckRequest,
	check PermissionCheck,
	depth int,
	visited map[string]bool,
) (bool, string, []TraversalStep, error) {
	var steps []TraversalStep

	targets, err := a.store.GetRelationTargets(ctx, req.Resource.Type, req.Resource.ID, check.Through)
	if err != nil {
		return false, "", steps, err
	}

	steps = append(steps, TraversalStep{
		Type:       StepThroughRelation,
		Resource:   req.Resource,
		Relation:   check.Through,
		Permission: check.Permission,
		Result:     StepContinued,
		Message:    fmt.Sprintf("traversing %s:%s#%s -> found %d targets", req.Resource.Type, req.Resource.ID, check.Through, len(targets)),
	})

	if len(targets) == 0 {
		return false, "", steps, nil
	}

	for _, target := range targets {
		targetRules := a.getPermissionRules(target.SubjectType, check.Permission)
		targetReq := CheckRequest{
			Subject:    req.Subject,
			Permission: check.Permission,
			Resource:   Resource{Type: target.SubjectType, ID: target.SubjectID},
		}

		steps = append(steps, TraversalStep{
			Type:       StepThroughRelation,
			Resource:   Resource{Type: target.SubjectType, ID: target.SubjectID},
			Permission: check.Permission,
			Subject:    req.Subject,
			Result:     StepContinued,
			Message:    fmt.Sprintf("checking %s#%s on target %s:%s", check.Permission, target.SubjectType, target.SubjectType, target.SubjectID),
		})

		allowed, reason, targetSteps, err := a.checkPermissionExplain(ctx, targetReq, targetRules, depth+1, visited)
		steps = append(steps, targetSteps...)
		if err != nil {
			return false, "", steps, err
		}

		if allowed {
			return true, fmt.Sprintf("through:%s->%s", check.Through, reason), steps, nil
		}
	}

	return false, "", steps, nil
}

func boolToResult(allowed bool) StepResult {
	if allowed {
		return StepGranted
	}
	return StepDenied
}

// =============================================================================
// Formatting
// =============================================================================

// FormatExplain returns a human-readable string representation of the result.
func (e ExplainResult) FormatExplain() string {
	s := fmt.Sprintf("Permission Check: %s:%s#%s@%s:%s\n",
		e.Request.Resource.Type, e.Request.Resource.ID,
		e.Request.Permission,
		e.Request.Subject.Type, e.Request.Subject.ID)
	s += fmt.Sprintf("Result: %v (reason: %s)\n", e.Allowed, e.Reason)
	s += "\nTraversal Path:\n"

	for i, step := range e.Steps {
		var prefix string
		switch step.Result {
		case StepGranted:
			prefix = "V "
		case StepDenied:
			prefix = "X "
		default:
			prefix = "> "
		}
		s += fmt.Sprintf("%d. %s[%s] %s\n", i+1, prefix, step.Type, step.Message)
	}

	return s
}
