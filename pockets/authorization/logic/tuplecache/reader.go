package tuplecache

import (
	"context"
	"fmt"
	"slices"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type cachedReads struct {
	ctx            context.Context
	backend        Backend
	state          State
	sets           map[SetKey][]tuples.Tuple
	failed, closed bool
	failure        error
}

var _ tuples.Reader = (*cachedReads)(nil)

func (r *cachedReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &checkReader{raw: r, model: model}
}
func (r *cachedReads) check(ctx context.Context) error {
	if r.closed {
		return ErrSnapshotClosed
	}
	if err := r.ctx.Err(); err != nil {
		return r.fail(err)
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	return nil
}
func (r *cachedReads) fail(err error) error { r.failed = true; r.failure = err; return err }
func (r *cachedReads) read(ctx context.Context, keys []SetKey) ([][]tuples.Tuple, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if err := key.Validate(); err != nil {
			return nil, err
		}
	}
	var missing []SetKey
	seen := make(map[SetKey]bool)
	for _, key := range keys {
		if _, ok := r.sets[key]; !ok && !seen[key] {
			missing = append(missing, key)
			seen[key] = true
		}
	}
	if len(missing) > 0 {
		values, err := r.backend.Read(ctx, r.state, missing)
		if err != nil {
			return nil, r.fail(err)
		}
		if len(values) != len(missing) {
			return nil, r.fail(ErrUnavailable)
		}
		for i, facts := range values {
			for _, fact := range facts {
				key := missing[i]
				if fact.Validate() != nil || (key.Reverse && fact.Subject != key.Subject) || (!key.Reverse && (fact.Scope != key.Scope || fact.Relation != key.Relation)) {
					return nil, r.fail(ErrUnavailable)
				}
			}
			facts = slices.Clone(facts)
			slices.SortFunc(facts, tuples.Compare)
			r.sets[missing[i]] = slices.Compact(facts)
		}
	}
	out := make([][]tuples.Tuple, len(keys))
	for i, key := range keys {
		out[i] = slices.Clone(r.sets[key])
	}
	return out, nil
}
func (r *cachedReads) Contains(ctx context.Context, fact tuples.Tuple) (bool, error) {
	out, err := r.ContainsMany(ctx, []tuples.Tuple{fact})
	if err != nil {
		return false, err
	}
	return out[0], nil
}
func (r *cachedReads) ContainsMany(ctx context.Context, facts []tuples.Tuple) ([]bool, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	keys := make([]SetKey, len(facts))
	for i, fact := range facts {
		if err := fact.Validate(); err != nil {
			return nil, err
		}
		keys[i] = SetKey{Scope: fact.Scope, Relation: fact.Relation}
	}
	sets, err := r.read(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make([]bool, len(facts))
	for i, fact := range facts {
		out[i] = slices.Contains(sets[i], fact)
	}
	return out, nil
}
func (r *cachedReads) ReadSets(ctx context.Context, keys []SetKey, maxResults int) ([][]tuples.Tuple, error) {
	if maxResults < 0 {
		return nil, fmt.Errorf("negative tuple set limit: %w", sdk.ErrInvalidInput)
	}
	out, err := r.read(ctx, keys)
	if err != nil {
		return nil, err
	}
	if maxResults > 0 {
		remaining := maxResults
		for _, set := range out {
			if len(set) > remaining {
				return nil, tuples.ErrReadLimit
			}
			remaining -= len(set)
		}
	}
	return out, nil
}
func (r *cachedReads) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	var key SetKey
	switch {
	case q.Subject != nil:
		key = SetKey{Reverse: true, Subject: *q.Subject}
	case q.Scope != nil && q.Relation != "":
		key = SetKey{Scope: *q.Scope, Relation: q.Relation}
	default:
		return nil, r.fail(ErrUnavailable)
	}
	sets, err := r.read(ctx, []SetKey{key})
	if err != nil {
		return nil, err
	}
	out := make([]tuples.Tuple, 0)
	for _, fact := range sets[0] {
		if q.Matches(fact) && (q.After == nil || tuples.Compare(fact, *q.After) > 0) {
			out = append(out, fact)
			if q.Limit > 0 && len(out) == q.Limit {
				break
			}
		}
	}
	return out, nil
}

type checkReader struct {
	raw   *cachedReads
	model relationships.ReadModel
}

func (r *checkReader) GetRelationTargets(ctx context.Context, rt, id, relation string) ([]relationships.RelationTarget, error) {
	sets, err := r.RelationTargetsFor(ctx, rt, []string{id}, relation)
	return sets[id], err
}

func (r *checkReader) RelationTargetsFor(ctx context.Context, rt string, ids []string, relation string) (map[string][]relationships.RelationTarget, error) {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	keys := make([]SetKey, len(ids))
	for i, id := range ids {
		keys[i] = SetKey{Scope: tuples.On(rt, id), Relation: relation}
	}
	sets, err := r.raw.read(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]relationships.RelationTarget, len(ids))
	for i, refs := range sets {
		for _, fact := range refs {
			ref := fact.Subject
			if r.model.Allows(rt, relation, ref.Type, ref.Relation) {
				out[ids[i]] = append(out[ids[i]], ref)
			}
		}
	}
	return out, nil
}

// reachable preserves the durable reader's subject-first, full-closure budget.
// Only raw sets are memoized for this operation; no closure survives a request.
func (r *checkReader) reachable(ctx context.Context, st, sid string, limit int) (map[relationships.SubjectRef]bool, error) {
	seed := relationships.SubjectRef{Type: st, ID: sid}
	seen := map[relationships.SubjectRef]bool{seed: true}
	frontier := []relationships.SubjectRef{seed}
	for len(frontier) > 0 {
		keys := make([]SetKey, len(frontier))
		for i, ref := range frontier {
			keys[i] = SetKey{Reverse: true, Subject: ref}
		}
		sets, err := r.raw.read(ctx, keys)
		if err != nil {
			return nil, err
		}
		var next []relationships.SubjectRef
		for i, refs := range sets {
			for _, fact := range refs {
				if fact.Scope.Kind != tuples.ResourceScope {
					continue
				}
				ref := tuples.SubjectRef{Type: fact.Scope.Type, ID: fact.Scope.ID, Relation: fact.Relation}
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !r.model.Allows(ref.Type, ref.Relation, frontier[i].Type, frontier[i].Relation) || seen[ref] {
					continue
				}
				if limit > 0 && len(seen) >= limit {
					return nil, relationships.ErrExpansionBudgetExceeded
				}
				seen[ref] = true
				next = append(next, ref)
			}
		}
		frontier = next
	}
	return seen, nil
}

func (r *checkReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, relation, st, sid string, limit int) (bool, error) {
	results, err := r.CheckBatchDirect(ctx, rt, []string{id}, relation, st, sid, limit)
	return results[id], err
}

func (r *checkReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, relation, st, sid string, limit int) (map[string]bool, error) {
	if err := r.raw.check(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	reached, err := r.reachable(ctx, st, sid, limit)
	if err != nil {
		return nil, err
	}
	// Every reachable exact resource#relation is precisely a directly/indirectly
	// held relation. The reverse closure already contains the final grant edge.
	for _, id := range ids {
		out[id] = reached[relationships.SubjectRef{Type: rt, ID: id, Relation: relation}]
	}
	return out, nil
}

func (r *checkReader) FilterRelation(ctx context.Context, rt string, ids []string, relation, st, sid string, limit int) ([]string, error) {
	matched, err := r.CheckBatchDirect(ctx, rt, ids, relation, st, sid, limit)
	if err != nil {
		return nil, err
	}
	var out []string
	for id, ok := range matched {
		if ok {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}
