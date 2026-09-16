package decisions

import (
	"context"
	"sort"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// tupleGraphReader is the portable graph read implementation. Store adapters
// may provide the same model-scoped ports with optimized recursive queries.
type tupleGraphReader struct {
	facts tuples.Reader
	model ReadModel
}

func (r *tupleGraphReader) GetRelationTargets(ctx context.Context, rt, id, rel string) ([]RelationTarget, error) {
	sets, err := r.facts.ReadSets(ctx, []tuples.SetKey{{Scope: tuples.On(rt, id), Relation: rel}}, 0)
	if err != nil {
		return nil, err
	}
	if len(sets) != 1 {
		return nil, authmodel.ErrEvaluationLimit
	}
	out := make([]RelationTarget, 0, len(sets[0]))
	for _, t := range sets[0] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.model.Allows(rt, rel, t.Subject.Type, t.Subject.Relation) {
			out = append(out, RelationTarget{Type: t.Subject.Type, ID: t.Subject.ID, Relation: t.Subject.Relation})
		}
	}
	return out, nil
}

// reachable mirrors the adapters' subject-first closure. The concrete seed and
// every model-allowed resource#relation count as distinct expansion states;
// reaching a grant early never skips the rest of that bounded closure.
func (r *tupleGraphReader) reachable(ctx context.Context, st, sid string, max int) (map[tuples.SubjectRef]bool, error) {
	seed := tuples.SubjectRef{Type: st, ID: sid}
	seen := map[tuples.SubjectRef]bool{seed: true}
	queue := []tuples.SubjectRef{seed}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		sets, err := r.facts.ReadSets(ctx, []tuples.SetKey{{Reverse: true, Subject: current}}, 0)
		if err != nil {
			return nil, err
		}
		if len(sets) != 1 {
			return nil, authmodel.ErrEvaluationLimit
		}
		for _, fact := range sets[0] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if fact.Scope.Kind != tuples.ResourceScope || !r.model.Allows(fact.Scope.Type, fact.Relation, current.Type, current.Relation) {
				continue
			}
			next := tuples.SubjectRef{Type: fact.Scope.Type, ID: fact.Scope.ID, Relation: fact.Relation}
			if seen[next] {
				continue
			}
			if max > 0 && len(seen) >= max {
				return nil, ErrExpansionBudgetExceeded
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return seen, ctx.Err()
}
func (r *tupleGraphReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, rel, st, sid string, max int) (bool, error) {
	reached, err := r.reachable(ctx, st, sid, max)
	if err != nil {
		return false, err
	}
	return reached[tuples.SubjectRef{Type: rt, ID: id, Relation: rel}], nil
}
func (r *tupleGraphReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) (map[string]bool, error) {
	reached, err := r.reachable(ctx, st, sid, max)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = reached[tuples.SubjectRef{Type: rt, ID: id, Relation: rel}]
	}
	return out, nil
}
func pageIDs(ids map[string]bool, after string, limit int) []string {
	out := make([]string, 0, len(ids))
	for id := range ids {
		if id > after {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
func (r *tupleGraphReader) LookupResourceIDs(ctx context.Context, rt string, rels []string, st, sid, after string, limit int) ([]string, error) {
	// Enumeration ports do not impose a membership-expansion bound. The engine
	// bounds the result and verifies each candidate with its root-relative budget.
	reached, err := r.reachable(ctx, st, sid, 0)
	if err != nil {
		return nil, err
	}
	relations := map[string]bool{}
	for _, rel := range rels {
		relations[rel] = true
	}
	ids := map[string]bool{}
	for ref := range reached {
		if ref.Type == rt && relations[ref.Relation] {
			ids[ref.ID] = true
		}
	}
	return pageIDs(ids, after, limit), nil
}
func (r *tupleGraphReader) LookupResourceIDsByRelationTarget(ctx context.Context, rt, rel, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if !r.model.Allows(rt, rel, targetType, "") {
		return []string{}, ctx.Err()
	}
	ids := map[string]bool{}
	for _, target := range distinctSorted(targetIDs) {
		subject := tuples.SubjectRef{Type: targetType, ID: target}
		rows, err := r.facts.Lookup(ctx, tuples.Query{ResourceType: rt, Relation: rel, Subject: &subject})
		if err != nil {
			return nil, err
		}
		for _, fact := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ids[fact.Scope.ID] = true
		}
	}
	return pageIDs(ids, after, limit), nil
}
func (r *tupleGraphReader) LookupDescendantResourceIDs(ctx context.Context, rt string, relations []string, subjectType string, roots []string, after string, limit int) ([]string, error) {
	allowed := map[string]bool{}
	for _, rel := range relations {
		allowed[rel] = r.model.Allows(rt, rel, subjectType, "")
	}
	queue := distinctSorted(roots)
	expanded := map[string]bool{}
	descendants := map[string]bool{}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		if expanded[current] {
			continue
		}
		expanded[current] = true
		subject := tuples.SubjectRef{Type: subjectType, ID: current}
		rows, err := r.facts.Lookup(ctx, tuples.Query{ResourceType: rt, Subject: &subject})
		if err != nil {
			return nil, err
		}
		for _, fact := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if allowed[fact.Relation] {
				descendants[fact.Scope.ID] = true
				if !expanded[fact.Scope.ID] {
					queue = append(queue, fact.Scope.ID)
				}
			}
		}
	}
	return pageIDs(descendants, after, limit), nil
}

type checkGraphReader struct {
	*tupleGraphReader
	checks CheckReader
}

func (r *checkGraphReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, rel, st, sid string, max int) (bool, error) {
	return r.checks.CheckRelationWithGroupExpansion(ctx, rt, id, rel, st, sid, max)
}
func (r *checkGraphReader) GetRelationTargets(ctx context.Context, rt, id, rel string) ([]RelationTarget, error) {
	return r.checks.GetRelationTargets(ctx, rt, id, rel)
}
func (r *checkGraphReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) (map[string]bool, error) {
	return r.checks.CheckBatchDirect(ctx, rt, ids, rel, st, sid, max)
}
func (r *checkGraphReader) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) ([]string, error) {
	if set, ok := r.checks.(RelationSetReader); ok {
		return set.FilterRelation(ctx, rt, ids, rel, st, sid, max)
	}
	result, err := r.checks.CheckBatchDirect(ctx, rt, ids, rel, st, sid, max)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range distinctSorted(ids) {
		if result[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
func (r *checkGraphReader) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]RelationTarget, error) {
	if set, ok := r.checks.(RelationSetReader); ok {
		return set.RelationTargetsFor(ctx, rt, ids, rel)
	}
	out := map[string][]RelationTarget{}
	for _, id := range distinctSorted(ids) {
		targets, err := r.checks.GetRelationTargets(ctx, rt, id, rel)
		if err != nil {
			return nil, err
		}
		out[id] = targets
	}
	return out, nil
}
