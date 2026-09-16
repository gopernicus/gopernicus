package decisions

import (
	"context"
	"errors"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// Check evaluates a named permission in one coherent canonical tuple view.
// The compiled expression defines exact membership, graph checks and composition.
func (s *Service) Check(ctx context.Context, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if !s.DeclaresPermission(req.Resource.Type, req.Permission) {
		return decision(false, "no rules defined"), nil
	}
	var result authmodel.CheckResult
	err := s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		var err error
		result, err = view.check(ctx, req, newBudget(view.limits, newMemoReader(view.reader)))
		return err
	})
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	return result, nil
}

// EvaluateWith evaluates inside an already coherent, caller-owned view.
func (s *Service) EvaluateWith(ctx context.Context, reader tuples.Reader, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	if isNilReader(reader) {
		return authmodel.CheckResult{}, fmt.Errorf("nil bound tuple reader: %w", sdk.ErrInvalidInput)
	}
	view := s.bound(reader)
	return view.check(ctx, req, newBudget(view.limits, newMemoReader(view.reader)))
}
func (s *Service) CheckWith(ctx context.Context, reader tuples.Reader, req authmodel.CheckRequest) (authmodel.CheckResult, error) {
	return s.EvaluateWith(ctx, reader, req)
}

// check is the single evaluation funnel shared by Check and CheckExplain. The
// only difference between the two entry points is whether the budget carries a
// trace collector — there is deliberately no second evaluator, so an explain
// cannot reach a different decision or spend more work than the plain Check.
func (s *Service) check(ctx context.Context, req authmodel.CheckRequest, b *budget) (authmodel.CheckResult, error) {
	if err := req.Validate(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if !s.DeclaresPermission(req.Resource.Type, req.Permission) {
		return authmodel.CheckResult{Allowed: false, ReasonCode: authmodel.ReasonDenied, Reason: "no rules defined"}, nil
	}
	result, err := s.checkPermission(ctx, req, 0, b, make(map[stateKey]bool))
	s.observeDenied(ctx, b, result, err)
	return result, err
}

// CheckBatch evaluates multiple checks with independent per-request budgets.
// Readers with RelationSetReader batch pending Through and direct reads across
// the ordinary checks. Other readers retain sequential, memoized evaluation.
// Canonical tuple snapshots keep all requests in one operation view.
func (s *Service) CheckBatch(ctx context.Context, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if len(reqs) > s.limits.MaxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}
	needsReads := false
	for _, req := range reqs {
		if err := req.Validate(); err != nil {
			return nil, err
		}
		needsReads = needsReads || s.DeclaresPermission(req.Resource.Type, req.Permission)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !needsReads {
		results := make([]authmodel.CheckResult, len(reqs))
		for i := range results {
			results[i] = decision(false, "no rules defined")
		}
		return results, nil
	}
	var results []authmodel.CheckResult
	err := s.withOperation(ctx, func(ctx context.Context, view *Service) error {
		var err error
		results, err = view.checkBatch(ctx, view.reader, reqs)
		return err
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// CheckBatchWith evaluates a batch over one operation-specific read source.
func (s *Service) CheckBatchWith(ctx context.Context, reader tuples.Reader, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if isNilReader(reader) {
		return nil, fmt.Errorf("nil bound tuple reader: %w", sdk.ErrInvalidInput)
	}
	view := s.bound(reader)
	return view.checkBatch(ctx, view.reader, reqs)
}

func (s *Service) checkBatch(ctx context.Context, reader CheckReader, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	// Reject an over-size batch BEFORE any store call: an oversized request is
	// indeterminate work, not a decision. This is the MaxBatchSize dimension.
	if len(reqs) > s.limits.MaxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}

	for i := range reqs {
		if err := reqs[i].Validate(); err != nil {
			return nil, err
		}
	}

	canBatch := true
	first := reqs[0]
	for i := 1; i < len(reqs); i++ {
		if reqs[i].Principal != first.Principal ||
			reqs[i].Permission != first.Permission ||
			reqs[i].Resource.Type != first.Resource.Type {
			canBatch = false
			break
		}
	}

	if !canBatch {
		return s.checkBatchTraversal(ctx, reader, reqs)
	}

	checks := s.compiled.permissionChecks(first.Resource.Type, first.Permission)
	if len(checks) == 0 {
		return s.checkBatchTraversal(ctx, reader, reqs)
	}
	for _, check := range checks {
		if check.Through != "" {
			return s.checkBatchTraversal(ctx, reader, reqs)
		}
	}

	return s.checkBatchOptimized(ctx, reader, reqs)
}

// FilterAuthorized preserves input order and duplicates. It uses CheckBatch's
// same root-relative depth, short-circuit rules and per-request work limits.
func (s *Service) FilterAuthorized(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string, resourceIDs []string) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	if len(resourceIDs) > s.limits.MaxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}
	requests := make([]authmodel.CheckRequest, len(resourceIDs))
	for i, id := range resourceIDs {
		requests[i] = authmodel.CheckRequest{Principal: principal, Permission: permission, Resource: authmodel.Resource{Type: resourceType, ID: id}}
	}
	results, err := s.CheckBatch(ctx, requests)
	if err != nil {
		return nil, err
	}
	allowed := make([]string, 0, len(resourceIDs))
	for i, result := range results {
		if result.Allowed {
			allowed = append(allowed, resourceIDs[i])
		}
	}
	return allowed, nil
}

// =============================================================================
// Internal check logic
// =============================================================================

// checkPermission evaluates one (resource, permission) state. depth counts the
// Through hops taken to reach it (0 at the top resource). The depth boundary is
// pinned to `>`: MaxThroughDepth is the MAXIMUM number of Through hops, so
// depth == MaxThroughDepth is the last permitted hop and depth > MaxThroughDepth
// is exhaustion. stack holds the (resource, permission) states on the ACTIVE
// recursion path for path-local cycle detection; it is distinct from the shared
// budget, which tallies distinct graph states for cost across the whole decision.
func (s *Service) checkPermission(ctx context.Context, req authmodel.CheckRequest, depth int, b *budget, stack map[stateKey]bool) (authmodel.CheckResult, error) {
	if err := ctx.Err(); err != nil {
		// Never begin the recursion (or any store call below it) after the caller
		// has canceled: fail closed on the context error, not a deny.
		return authmodel.CheckResult{}, err
	}
	if err := b.chargeStep(); err != nil {
		return authmodel.CheckResult{}, err
	}
	if depth > b.limits.MaxThroughDepth {
		// Budget exhaustion is INDETERMINATE, not a deny: the caller fails closed
		// on ErrEvaluationLimit (wrapping sdk.ErrUnavailable) rather than being
		// told "not allowed". A host that legitimately needs deeper traversal
		// sets EvaluationLimits.MaxThroughDepth deliberately through WithLimits.
		return authmodel.CheckResult{}, authmodel.ErrEvaluationLimit
	}

	key := stateKey{resourceType: req.Resource.Type, resourceID: req.Resource.ID, permission: req.Permission}
	if stack[key] {
		// PATH-LOCAL cycle: this state is already being evaluated up-stack. For a
		// permission rule's OR-semantics, the in-progress frame contributes no
		// additional grant, so this branch denies (it is NOT a budget error).
		return authmodel.CheckResult{Allowed: false, ReasonCode: authmodel.ReasonDenied, Reason: "cycle detected"}, nil
	}
	if err := b.chargeState(req.Resource.Type, req.Resource.ID, req.Permission); err != nil {
		return authmodel.CheckResult{}, err
	}
	stack[key] = true
	defer delete(stack, key)

	return s.evaluateExpression(ctx, req, s.compiled.expression(req.Resource.Type, req.Permission), depth, b, stack)

}

// mapExpansionBudget translates the store-layer group-expansion overflow signal
// (relationship.ErrExpansionBudgetExceeded) into the engine's own indeterminate
// budget outcome (ErrEvaluationLimit) at the engine boundary — the domain never
// imports the engine sentinel. Both wrap sdk.ErrUnavailable, so the host-facing
// posture (503, fail closed, retryable) is identical. Any other error passes
// through unchanged.
func mapExpansionBudget(err error) error {
	if errors.Is(err, ErrExpansionBudgetExceeded) {
		return authmodel.ErrEvaluationLimit
	}
	return err
}

func (s *Service) checkThrough(ctx context.Context, req authmodel.CheckRequest, check Expression, depth int, b *budget, stack map[stateKey]bool) (authmodel.CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return authmodel.CheckResult{}, err
	}
	targets, err := b.reader.GetRelationTargets(ctx, req.Resource.Type, req.Resource.ID, check.Through)
	if err != nil {
		return authmodel.CheckResult{}, err
	}
	// Bound the per-hop fan-out (MaxRelationTargets): a hop wider than the budget
	// is indeterminate, never a deny.
	if err := b.chargeFanout(len(targets)); err != nil {
		return authmodel.CheckResult{}, err
	}

	for _, target := range targets {
		if err := b.chargeStep(); err != nil {
			return authmodel.CheckResult{}, err
		}
		// A navigational Through edge points only at concrete resource references.
		// Compile is the boot gate: it rejects userset targets on any relation used
		// by a Through, so no such tuple can validly exist. A stored userset here is
		// therefore off-schema data — skip it, failing closed rather than traversing
		// into a userset the model forbids.
		if target.IsUserset() || !s.readModel.Allows(req.Resource.Type, check.Through, target.Type, target.Relation) {
			continue
		}
		result, err := s.checkPermission(ctx, authmodel.CheckRequest{
			Principal:  req.Principal,
			Permission: check.Permission,
			Resource:   authmodel.Resource{Type: target.Type, ID: target.ID},
		}, depth+1, b, stack)
		if err != nil {
			return authmodel.CheckResult{}, err
		}
		if result.Allowed {
			result.ReasonCode = authmodel.ReasonGranted
			result.Reason = fmt.Sprintf("through:%s->%s", check.Through, result.Reason)
			return result, nil
		}
	}

	return authmodel.CheckResult{Allowed: false, ReasonCode: authmodel.ReasonDenied}, nil
}

// =============================================================================
// Batch internals
// =============================================================================

// checkBatchSequential evaluates each request through the ordinary evaluation
// funnel with its OWN fresh budget, over ONE batch-local memoReader (B4). The
// memo shares successful store reads across the batch — a container's items no
// longer re-read the shared container's targets and re-run its direct check —
// while the per-request budget keeps depth/state/fan-out charging, and therefore
// every result and every ErrEvaluationLimit, identical to a standalone Check.
func (s *Service) checkBatchSequential(ctx context.Context, source CheckReader, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	results := make([]authmodel.CheckResult, len(reqs))
	reader := newMemoReader(source)
	for i, req := range reqs {
		result, err := s.check(ctx, req, newBudget(s.limits, reader))
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

func (s *Service) checkBatchOptimized(ctx context.Context, reader CheckReader, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results := make([]authmodel.CheckResult, len(reqs))
	first := reqs[0]

	checks := s.compiled.permissionChecks(first.Resource.Type, first.Permission)
	if len(checks) == 0 {
		for i := range results {
			results[i] = authmodel.CheckResult{Allowed: false, ReasonCode: authmodel.ReasonDenied, Reason: "no rules defined"}
		}
		return results, nil
	}

	resourceIDs := make([]string, 0, len(reqs))
	for _, req := range reqs {
		resourceIDs = append(resourceIDs, req.Resource.ID)
	}

	resourceIDs = distinctSorted(resourceIDs)
	steps := 1 // the permission frame
	if s.compiled.expression(first.Resource.Type, first.Permission).AnyOf != nil {
		steps++
	}

	// grantedBy records the relation that ACTUALLY granted each resource — the
	// FIRST direct check (in the compiled, sorted check order) whose batch query
	// returned true. The earlier implementation reported checks[0]'s relation for
	// every grant, naming a relation that may not have granted (the audit's "batch
	// reasons can name a relation that did not grant"); this pins the debug Reason
	// to the real granting relation while ReasonCode stays the stable coarse code.
	grantedBy := make(map[string]string)
	for _, check := range checks {
		if len(resourceIDs) == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		steps++
		if steps > s.limits.MaxEvaluationSteps {
			return nil, authmodel.ErrEvaluationLimit
		}
		if check.Relation == "" {
			continue
		}
		batchResults, err := reader.CheckBatchDirect(
			ctx, first.Resource.Type, resourceIDs, check.Relation, first.Principal.Type, first.Principal.ID, s.limits.MaxGraphStates,
		)
		if err != nil {
			return nil, mapExpansionBudget(err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := resourceIDs[:0]
		for _, id := range resourceIDs {
			if !batchResults[id] {
				remaining = append(remaining, id)
			}
		}
		resourceIDs = remaining
		for resourceID, allowed := range batchResults {
			if allowed {
				if _, seen := grantedBy[resourceID]; !seen {
					grantedBy[resourceID] = check.Relation
				}
			}
		}
	}

	for i, req := range reqs {
		if relation, ok := grantedBy[req.Resource.ID]; ok {
			results[i] = authmodel.CheckResult{Allowed: true, ReasonCode: authmodel.ReasonGranted, Reason: fmt.Sprintf("direct:%s", relation)}
		} else {
			results[i] = authmodel.CheckResult{Allowed: false, ReasonCode: authmodel.ReasonDenied, Reason: "no matching rule"}
		}
	}

	return results, nil
}
