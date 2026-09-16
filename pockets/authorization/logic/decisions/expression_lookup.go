package decisions

import (
	"context"
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authorization/internal/decisioncursor"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Service) LookupResourcesIn(ctx context.Context, req authmodel.LookupRequest) (authmodel.LookupResult, error) {
	log := s.startDecisionLog(ctx, false)
	result, err := s.lookupResourcesIn(ctx, req)
	log.lookup(ctx, "LookupResourcesIn", req.Principal, req.Permission, req.ResourceType, req.Limit, result, err)
	return result, err
}

func (s *Service) lookupResourcesIn(ctx context.Context, req authmodel.LookupRequest) (authmodel.LookupResult, error) {
	if err := req.Validate(); err != nil {
		return authmodel.LookupResult{}, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = s.limits.MaxLookupResults
	}
	if limit > s.limits.MaxLookupResults {
		return authmodel.LookupResult{}, fmt.Errorf("lookup limit exceeds budget: %w", sdk.ErrInvalidInput)
	}
	fingerprint := decisioncursor.LookupFingerprint("tuple", s.ModelDigest(), req.Principal, req.Permission, req.ResourceType)
	after := ""
	if req.After != "" {
		var err error
		after, err = decisioncursor.DecodeLookupCursor(req.After, fingerprint)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
	}
	result, err := s.lookupResourcesPageOperation(ctx, req.Principal, req.Permission, req.ResourceType, after, limit)
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	if result.HasMore && len(result.IDs) > 0 {
		result.NextCursor = decisioncursor.EncodeLookupCursor(result.IDs[len(result.IDs)-1], fingerprint)
	}
	return result, nil
}
func (c *CompiledModel) pureGraph(rt, p string) bool {
	visited := map[nodeKey]bool{}
	var visit func(string, string) bool
	visit = func(rt, p string) bool {
		key := nodeKey{rt, p}
		if visited[key] {
			return true
		}
		visited[key] = true
		checks := c.permissionChecks(rt, p)
		if len(checks) == 0 {
			return !c.declaresPermission(rt, p)
		}
		for _, e := range checks {
			if e.Through != "" {
				for _, target := range c.relationResourceTargets(rt, e.Through) {
					if !visit(target, e.Permission) {
						return false
					}
				}
			}
		}
		return true
	}
	return visit(rt, p)
}
func (s *Service) lookupExpression(ctx context.Context, principal authmodel.PrincipalRef, rt, p string, e Expression, b *budget, stack map[nodeKey]bool) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if err := b.chargeStep(); err != nil {
		return authmodel.LookupResult{}, err
	}
	empty := authmodel.LookupResult{IDs: []string{}}
	if e.AnyOf != nil || e.AllOf != nil {
		all := e.AllOf != nil
		children := e.AnyOf
		if all {
			children = e.AllOf
		}
		result := empty
		if all {
			result.Unrestricted = true
		}
		for _, child := range children {
			next, err := s.lookupExpression(ctx, principal, rt, p, child, b, stack)
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			result = combineSets(result, next, all)
			if b.resultsOverflow(len(result.IDs)) {
				return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
			}
			if (!all && result.Unrestricted) || (all && !result.Unrestricted && len(result.IDs) == 0) {
				return result, nil
			}
		}
		return result, nil
	}
	if e.RoleName != "" {
		subject := tuples.SubjectRef{Type: principal.Type, ID: principal.ID}
		if !e.CurrentResource {
			scope := tuples.Global()
			if e.Resource != nil {
				scope = tuples.On(e.Resource.Type, e.Resource.ID)
			}
			allowed, err := s.facts.Contains(ctx, tuples.Tuple{Scope: scope, Relation: e.RoleName, Subject: subject})
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			empty.Unrestricted = allowed
			return empty, nil
		}
		rows, err := s.facts.Lookup(ctx, tuples.Query{ResourceType: rt, Relation: e.RoleName, Subject: &subject, Limit: b.resultFetchCap()})
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.Scope.ID)
		}
		ids = distinctSorted(ids)
		if b.resultsOverflow(len(ids)) {
			return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
		}
		return authmodel.LookupResult{IDs: ids}, nil
	}
	if e.NamedPermission != "" {
		return s.lookupNamedExpression(ctx, principal, rt, e.NamedPermission, b, stack)
	}
	if e.Relation != "" {
		ids, err := s.reader.LookupResourceIDs(ctx, rt, []string{e.Relation}, principal.Type, principal.ID, "", b.resultFetchCap())
		if err != nil {
			return authmodel.LookupResult{}, mapExpansionBudget(err)
		}
		if b.resultsOverflow(len(ids)) {
			return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
		}
		return authmodel.LookupResult{IDs: ids}, nil
	}
	result := empty
	for _, targetType := range s.compiled.relationResourceTargets(rt, e.Through) {
		if err := b.chargeStep(); err != nil {
			return authmodel.LookupResult{}, err
		}
		target, err := s.lookupNamedExpression(ctx, principal, targetType, e.Permission, b, stack)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		var ids []string
		if target.Unrestricted {
			rows, err := s.lookupDiscoveryFacts(ctx, tuples.Query{ResourceType: rt, Relation: e.Through}, b)
			if err != nil {
				return authmodel.LookupResult{}, err
			}
			for _, t := range rows {
				if t.Subject.Type == targetType && t.Subject.Relation == "" && s.readModel.Allows(rt, e.Through, targetType, "") {
					ids = append(ids, t.Scope.ID)
				}
			}
		} else if len(target.IDs) > 0 {
			ids, err = s.reader.LookupResourceIDsByRelationTarget(ctx, rt, e.Through, targetType, target.IDs, "", b.resultFetchCap())
			if err != nil {
				return authmodel.LookupResult{}, err
			}
		}
		result = combineSets(result, authmodel.LookupResult{IDs: distinctSorted(ids)}, false)
		if b.resultsOverflow(len(result.IDs)) {
			return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
		}
	}
	return result, nil
}
func combineSets(a, b authmodel.LookupResult, all bool) authmodel.LookupResult {
	if !all {
		if a.Unrestricted || b.Unrestricted {
			return authmodel.LookupResult{IDs: []string{}, Unrestricted: true}
		}
		return authmodel.LookupResult{IDs: distinctSorted(append(append([]string{}, a.IDs...), b.IDs...))}
	}
	if a.Unrestricted {
		return b
	}
	if b.Unrestricted {
		return a
	}
	present := map[string]bool{}
	for _, id := range b.IDs {
		present[id] = true
	}
	ids := []string{}
	for _, id := range a.IDs {
		if present[id] {
			ids = append(ids, id)
		}
	}
	return authmodel.LookupResult{IDs: ids}
}
func (s *Service) lookupNamedExpression(ctx context.Context, principal authmodel.PrincipalRef, rt, p string, b *budget, stack map[nodeKey]bool) (authmodel.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if !s.DeclaresPermission(rt, p) {
		return authmodel.LookupResult{IDs: []string{}}, nil
	}
	if err := b.chargeStep(); err != nil {
		return authmodel.LookupResult{}, err
	}
	if s.compiled.pureGraph(rt, p) {
		return s.lookupResources(ctx, principal, p, rt, b, map[nodeKey]bool{}, map[nodeKey]authmodel.LookupResult{})
	}
	key := nodeKey{rt, p}
	if stack[key] {
		return s.lookupCandidates(ctx, principal, rt, p, b)
	}
	stack[key] = true
	defer delete(stack, key)
	for _, e := range expressionLeaves(s.compiled.expression(rt, p)) {
		if e.Through != "" && e.Permission == p {
			for _, target := range s.compiled.relationResourceTargets(rt, e.Through) {
				if target == rt {
					return s.lookupCandidates(ctx, principal, rt, p, b)
				}
			}
		}
	}
	return s.lookupExpression(ctx, principal, rt, p, s.compiled.expression(rt, p), b, stack)
}

// A recursive conjunction cannot use the graph union closure. Every resource
// carrying a fact is a complete candidate universe for a positive scoped graph
// expression; exact global/fixed branches are checked separately for universality.
func (s *Service) lookupCandidates(ctx context.Context, principal authmodel.PrincipalRef, rt, p string, b *budget) (authmodel.LookupResult, error) {
	universal, err := s.universalExpression(ctx, principal, rt, s.compiled.expression(rt, p), b, map[string]bool{})
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	if universal {
		return authmodel.LookupResult{IDs: []string{}, Unrestricted: true}, nil
	}
	rows, err := s.lookupDiscoveryFacts(ctx, tuples.Query{ResourceType: rt}, b)
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	candidates := map[string]bool{}
	for _, row := range rows {
		candidates[row.Scope.ID] = true
	}
	ids := pageIDs(candidates, "", 0)
	result := []string{}
	for _, id := range ids {
		if err := b.chargeStep(); err != nil {
			return authmodel.LookupResult{}, err
		}
		checked, err := s.check(ctx, authmodel.CheckRequest{Principal: principal, Permission: p, Resource: authmodel.Resource{Type: rt, ID: id}}, b)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		if checked.Allowed {
			result = append(result, id)
			if b.resultsOverflow(len(result)) {
				return authmodel.LookupResult{}, authmodel.ErrEvaluationLimit
			}
		}
	}
	return authmodel.LookupResult{IDs: result}, nil
}

// Raw discovery is bounded by the remaining operation work, not a fresh copy
// of its ceiling. Every returned fact consumes one discovery step.
func (s *Service) lookupDiscoveryFacts(ctx context.Context, q tuples.Query, b *budget) ([]tuples.Tuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	remaining := b.limits.MaxEvaluationSteps - b.steps
	if remaining <= 0 {
		return nil, authmodel.ErrEvaluationLimit
	}
	q.Limit = remaining + 1
	rows, err := s.facts.Lookup(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(rows) > remaining {
		return nil, authmodel.ErrEvaluationLimit
	}
	for range rows {
		if err := b.chargeStep(); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (s *Service) universalExpression(ctx context.Context, principal authmodel.PrincipalRef, rt string, e Expression, b *budget, stack map[string]bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := b.chargeStep(); err != nil {
		return false, err
	}
	if e.AnyOf != nil || e.AllOf != nil {
		all := e.AllOf != nil
		children := e.AnyOf
		if all {
			children = e.AllOf
		}
		for _, child := range children {
			v, err := s.universalExpression(ctx, principal, rt, child, b, stack)
			if err != nil {
				return false, err
			}
			if v != all {
				return v, nil
			}
		}
		return all, nil
	}
	if e.RoleName != "" && !e.CurrentResource {
		scope := tuples.Global()
		if e.Resource != nil {
			scope = tuples.On(e.Resource.Type, e.Resource.ID)
		}
		return s.facts.Contains(ctx, tuples.Tuple{Scope: scope, Relation: e.RoleName, Subject: tuples.SubjectRef{Type: principal.Type, ID: principal.ID}})
	}
	if e.NamedPermission != "" && !stack[e.NamedPermission] {
		if err := b.chargeStep(); err != nil {
			return false, err
		}
		stack[e.NamedPermission] = true
		defer delete(stack, e.NamedPermission)
		return s.universalExpression(ctx, principal, rt, s.compiled.expression(rt, e.NamedPermission), b, stack)
	}
	return false, nil
}
func (s *Service) lookupGeneral(ctx context.Context, principal authmodel.PrincipalRef, p, rt, after string, limit int) (authmodel.LookupResult, error) {
	return s.runLookup(ctx, func(ctx context.Context, view *Service) (authmodel.LookupResult, error) {
		b := newBudget(view.limits, newMemoReader(view.reader))
		if limit > 0 && view.exactDisjunction(rt, view.compiled.expression(rt, p)) {
			return view.lookupExactPage(ctx, principal, rt, p, after, limit, b)
		}
		result, err := view.lookupNamedExpression(ctx, principal, rt, p, b, map[nodeKey]bool{})
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		if result.Unrestricted {
			return result, nil
		}
		result, err = view.verifyLookup(ctx, principal, p, rt, result)
		if err != nil {
			return authmodel.LookupResult{}, err
		}
		sort.Strings(result.IDs)
		start := sort.SearchStrings(result.IDs, after)
		for start < len(result.IDs) && result.IDs[start] <= after {
			start++
		}
		result.IDs = result.IDs[start:]
		if limit > 0 && len(result.IDs) > limit {
			result.HasMore = true
			result.IDs = result.IDs[:limit]
		}
		return result, nil
	})
}

// A disjunction of exact facts has no intermediate graph set to materialize.
// Each leaf supplies at most the next page plus one row; merging these bounded
// prefixes produces the next complete page even when the total set is larger
// than MaxLookupResults.
func (s *Service) exactDisjunction(rt string, e Expression) bool {
	// Named expressions form a validated DAG. Memoize classification instead of
	// flattening shared subtrees, which could grow exponentially before evaluation.
	named := map[string]bool{}
	var visit func(Expression) bool
	visit = func(e Expression) bool {
		if e.RoleName != "" {
			return true
		}
		if e.NamedPermission != "" {
			if exact, ok := named[e.NamedPermission]; ok {
				return exact
			}
			exact := visit(s.compiled.expression(rt, e.NamedPermission))
			named[e.NamedPermission] = exact
			return exact
		}
		if e.AnyOf == nil {
			return false
		}
		for _, child := range e.AnyOf {
			if !visit(child) {
				return false
			}
		}
		return true
	}
	return visit(e)
}

func (s *Service) lookupExactPage(ctx context.Context, principal authmodel.PrincipalRef, rt, p, after string, limit int, b *budget) (authmodel.LookupResult, error) {
	subject := tuples.SubjectRef{Type: principal.Type, ID: principal.ID}
	ids := map[string]bool{}
	var expression func(Expression) (bool, error)
	var permission func(string) (bool, error)
	permission = func(name string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := b.chargeStep(); err != nil {
			return false, err
		}
		return expression(s.compiled.expression(rt, name))
	}
	expression = func(e Expression) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := b.chargeStep(); err != nil {
			return false, err
		}
		if e.NamedPermission != "" {
			return permission(e.NamedPermission)
		}
		if e.AnyOf != nil {
			for _, child := range e.AnyOf {
				unrestricted, err := expression(child)
				if err != nil || unrestricted {
					return unrestricted, err
				}
			}
			return false, nil
		}
		if !e.CurrentResource {
			scope := tuples.Global()
			if e.Resource != nil {
				scope = tuples.On(e.Resource.Type, e.Resource.ID)
			}
			return s.facts.Contains(ctx, tuples.Tuple{Scope: scope, Relation: e.RoleName, Subject: subject})
		}
		query := tuples.Query{ResourceType: rt, Relation: e.RoleName, Subject: &subject, Limit: limit + 1}
		if after != "" {
			query.After = &tuples.Tuple{Scope: tuples.On(rt, after), Relation: e.RoleName, Subject: subject}
		}
		rows, err := s.facts.Lookup(ctx, query)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			ids[row.Scope.ID] = true
		}
		return false, nil
	}
	unrestricted, err := permission(p)
	if err != nil {
		return authmodel.LookupResult{}, err
	}
	if unrestricted {
		return authmodel.LookupResult{IDs: []string{}, Unrestricted: true}, nil
	}
	result := authmodel.LookupResult{IDs: pageIDs(ids, "", limit+1)}
	if len(result.IDs) > limit {
		result.HasMore = true
		result.IDs = result.IDs[:limit]
	}
	return s.verifyLookup(ctx, principal, p, rt, result)
}
