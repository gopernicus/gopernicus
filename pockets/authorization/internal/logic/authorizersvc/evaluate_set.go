package authorizersvc

import (
	"context"
	"slices"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
)

// nodeKey is one NODE of a set walk: a (resource type, permission) pair whose
// candidate ids are decided together. It is the set dual of the per-decision
// stateKey — a stateKey is one node plus one id.
type nodeKey struct {
	resourceType string
	permission   string
}

// setEvaluation is the working state of ONE evaluateSet call: the states the
// walk has already expanded, the states it has proven granted, and the reverse
// Through edges it propagates that proof along. It is not safe for concurrent
// use — one evaluation runs on one goroutine, exactly like the budget it rides
// beside.
type setEvaluation struct {
	principal PrincipalRef
	budget    *budget

	// allowed holds the states proven granted, first by a Direct branch and then
	// by propagation along dependents.
	allowed map[stateKey]bool
	// dependents maps a TARGET state to the states a Through hop grants when the
	// target is granted (the reverse of the hop edge). It is what turns the set
	// walk into a least fixpoint: a state whose only grant arrives through a
	// target decided elsewhere in the walk — a sibling candidate, an ancestor
	// already on the frontier — is still granted, without the walk needing to
	// know the order the two were discovered in.
	dependents map[stateKey][]stateKey
	// expanded marks the states whose own branches have been (or are about to be)
	// walked, so each state costs its reads ONCE however many candidates reach it.
	expanded map[stateKey]bool
}

// evaluateSet decides ONE permission for MANY resources of ONE type as a SET:
// it walks the compiled permission tree level by level, spending one store read
// per (branch, hop) over the whole remaining candidate set instead of one per
// (candidate, branch, hop). ids must be distinct; the returned map answers
// exactly those ids (an absent or false entry is a deny).
//
// # Answers
//
// The result is the LEAST FIXPOINT of the schema's rules over the tuples: a
// state is granted when a Direct branch admits it, or when any concrete target
// of one of its Through hops is granted. That is precisely what a per-resource
// Check computes — its path-local cycle rule ("the in-progress frame
// contributes no additional grant") IS the least-fixpoint rule — so for the
// same store and limits, an id is in this result exactly when Check allows it.
// Reaching the answer as a fixpoint rather than a depth-first walk is also what
// keeps a candidate set containing BOTH a resource and its ancestor honest: the
// ancestor is not "in progress" to its sibling, it is simply another state
// whose grant propagates.
//
// # Budgets
//
// One set evaluation is ONE decision and carries ONE budget, charged with the
// same resolved limits as a Check: MaxThroughDepth on every hop (a target
// discovered beyond the bound is ErrEvaluationLimit, never a deny),
// MaxRelationTargets per resource per hop, MaxGraphStates on distinct states,
// and the group-expansion bound forwarded to every FilterRelation. Overflow of
// any dimension ends the WHOLE call with ErrEvaluationLimit, exactly as
// CheckBatch reports one request's overflow for the whole batch today.
//
// Two dimensions are therefore deliberately more CONSERVATIVE than N
// per-decision budgets, in the indeterminate direction only (never a wrong
// allow, never a wrong deny):
//
//   - MaxGraphStates is charged ONCE for the set, so a wide candidate set can
//     exhaust it where each candidate alone would not. The set is bounded by
//     MaxBatchSize, so the ceiling is a deliberate ratio between the two limits,
//     not an accident.
//   - A Check stops at its first granting branch and target and so may never
//     reach a too-deep or too-wide part of the graph that another candidate's
//     branch reaches. Under CheckBatch that already ended the whole batch; here
//     it ends the whole set the same way.
func (s *Service) evaluateSet(ctx context.Context, principal PrincipalRef, permission, resourceType string, ids []string, b *budget) (map[string]bool, error) {
	e := &setEvaluation{
		principal:  principal,
		budget:     b,
		allowed:    make(map[stateKey]bool),
		dependents: make(map[stateKey][]stateKey),
		expanded:   make(map[stateKey]bool),
	}

	frontier := map[nodeKey][]string{}
	seed := nodeKey{resourceType, permission}
	for _, id := range ids {
		state := stateKey{resourceType, id, permission}
		if e.expanded[state] {
			continue
		}
		e.expanded[state] = true
		if err := b.chargeState(resourceType, id, permission); err != nil {
			return nil, err
		}
		frontier[seed] = append(frontier[seed], id)
	}

	for depth := 0; len(frontier) > 0; depth++ {
		next := map[nodeKey][]string{}
		for _, node := range sortedNodes(frontier) {
			if err := e.expand(ctx, s, node, frontier[node], depth, next); err != nil {
				return nil, err
			}
		}
		frontier = next
	}
	e.propagate()

	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if e.allowed[stateKey{resourceType, id, permission}] {
			out[id] = true
		}
	}
	return out, nil
}

// expand walks ONE node's compiled checks over its undecided candidate ids:
// every Direct branch is one FilterRelation over the ids still undecided (an
// admitted id leaves the set immediately — the per-id AnyOf short-circuit), and
// every Through branch is one RelationTargetsFor over them, whose DISTINCT
// concrete targets are recorded as reverse edges and enqueued onto the next
// depth's frontier. A target already expanded contributes its edge without
// being walked again.
func (e *setEvaluation) expand(ctx context.Context, s *Service, node nodeKey, ids []string, depth int, next map[nodeKey][]string) error {
	checks := s.compiled.permissionChecks(node.resourceType, node.permission)
	if len(checks) == 0 {
		// No rules defined for the pair: the states are charged (they were
		// enqueued) and deny, exactly as checkPermission denies over no checks.
		return nil
	}

	remaining := slices.Clone(ids)
	for _, check := range checks {
		if len(remaining) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if check.Through == "" {
			matched, err := e.budget.reader.FilterRelation(
				ctx, node.resourceType, remaining, check.Relation, e.principal.Type, e.principal.ID, s.limits.MaxGraphStates,
			)
			if err != nil {
				return mapExpansionBudget(err)
			}
			if len(matched) == 0 {
				continue
			}
			granted := make(map[string]bool, len(matched))
			for _, id := range matched {
				granted[id] = true
				e.allowed[stateKey{node.resourceType, id, node.permission}] = true
			}
			remaining = slices.DeleteFunc(remaining, func(id string) bool { return granted[id] })
			continue
		}
		if err := e.hop(ctx, s, node, remaining, check, depth, next); err != nil {
			return err
		}
	}
	return nil
}

// hop reads ONE Through relation for the whole remaining candidate set and
// records where each candidate's grant could come from. Fan-out is charged per
// resource, exactly as checkThrough charges one hop; a userset target is
// skipped (Compile forbids one on a navigational relation, so a stored one is
// off-schema data the walk fails closed on); and a target beyond the depth
// bound is ErrEvaluationLimit for the whole call, never a deny.
func (e *setEvaluation) hop(ctx context.Context, s *Service, node nodeKey, ids []string, check PermissionCheck, depth int, next map[nodeKey][]string) error {
	targets, err := e.budget.reader.RelationTargetsFor(ctx, node.resourceType, ids, check.Through)
	if err != nil {
		return err
	}
	for _, id := range ids {
		hop := targets[id]
		if err := e.budget.chargeFanout(len(hop)); err != nil {
			return err
		}
		from := stateKey{node.resourceType, id, node.permission}
		for _, target := range hop {
			if target.IsUserset() {
				continue
			}
			if depth+1 > e.budget.limits.MaxThroughDepth {
				return ErrEvaluationLimit
			}
			state := stateKey{target.Type, target.ID, check.Permission}
			e.dependents[state] = append(e.dependents[state], from)
			if e.expanded[state] {
				continue
			}
			e.expanded[state] = true
			if err := e.budget.chargeState(target.Type, target.ID, check.Permission); err != nil {
				return err
			}
			key := nodeKey{target.Type, check.Permission}
			next[key] = append(next[key], target.ID)
		}
	}
	return nil
}

// propagate closes the least fixpoint: every state that a Through hop grants
// from an already-granted target becomes granted itself, transitively. It is a
// worklist over the reverse edges, so it costs one visit per edge and needs no
// ordering assumption about the depth a grant was discovered at.
func (e *setEvaluation) propagate() {
	queue := make([]stateKey, 0, len(e.allowed))
	for state := range e.allowed {
		queue = append(queue, state)
	}
	for len(queue) > 0 {
		state := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, from := range e.dependents[state] {
			if e.allowed[from] {
				continue
			}
			e.allowed[from] = true
			queue = append(queue, from)
		}
	}
}

// sortedNodes returns a frontier's nodes in a deterministic order, so a set
// evaluation spends its reads (and reports its budget overflow) in the same
// order on every run regardless of map iteration.
func sortedNodes(frontier map[nodeKey][]string) []nodeKey {
	out := make([]nodeKey, 0, len(frontier))
	for node := range frontier {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].resourceType != out[j].resourceType {
			return out[i].resourceType < out[j].resourceType
		}
		return out[i].permission < out[j].permission
	})
	return out
}

// distinctSorted folds a caller's id list to the distinct ids in byte order —
// the shape a set read takes and the shape the walk dedups on.
func distinctSorted(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	sort.Strings(out)
	return slices.Compact(out)
}

// PerResourceReader is the PER-RESOURCE half of PermissionReader: the two reads
// every relationship walk has always made, one state at a time. It is the seam
// a reader that CANNOT batch — the guarded mutation's transaction-bound,
// dependency-recording view — fills its set methods over.
type PerResourceReader interface {
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string, maxExpansionStates int) (bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]relationship.RelationTarget, error)
}

// FilterRelationOver answers FilterRelation by looping the per-resource check —
// the honest fallback for a reader with no set read of its own. It preserves
// the port's output contract: distinct, byte order, a subset of the input, and
// no read at all for an empty input.
func FilterRelationOver(ctx context.Context, r PerResourceReader, resourceType string, resourceIDs []string, relation, subjectType, subjectID string, maxExpansionStates int) ([]string, error) {
	var out []string
	for _, id := range distinctSorted(resourceIDs) {
		ok, err := r.CheckRelationWithGroupExpansion(ctx, resourceType, id, relation, subjectType, subjectID, maxExpansionStates)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// RelationTargetsForOver answers RelationTargetsFor by looping the per-resource
// read, the sibling fallback of FilterRelationOver. An id with no targets stays
// absent from the map.
func RelationTargetsForOver(ctx context.Context, r PerResourceReader, resourceType string, resourceIDs []string, relation string) (map[string][]relationship.RelationTarget, error) {
	out := make(map[string][]relationship.RelationTarget, len(resourceIDs))
	for _, id := range distinctSorted(resourceIDs) {
		targets, err := r.GetRelationTargets(ctx, resourceType, id, relation)
		if err != nil {
			return nil, err
		}
		if len(targets) > 0 {
			out[id] = targets
		}
	}
	return out, nil
}
