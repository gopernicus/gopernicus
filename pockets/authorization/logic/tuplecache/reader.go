package tuplecache

import (
	"context"
	"slices"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

type cachedReads struct {
	ctx            context.Context
	backend        Backend
	state          State
	sets           map[SetKey][]relationships.SubjectRef
	failed, closed bool
}

func (r *cachedReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &checkReader{raw: r, model: model}
}

func (r *cachedReads) HasExactRole(ctx context.Context, _ string, _ string, _ string, _ string, _ string) (bool, error) {
	if err := r.check(ctx); err != nil {
		return false, err
	}
	r.failed = true
	return false, ErrUnavailable
}

func (r *cachedReads) check(ctx context.Context) error {
	if r.closed {
		return ErrSnapshotClosed
	}
	if err := r.ctx.Err(); err != nil {
		r.failed = true
		return err
	}
	if err := ctx.Err(); err != nil {
		r.failed = true
		return err
	}
	return nil
}

func (r *cachedReads) read(ctx context.Context, keys []SetKey) ([][]relationships.SubjectRef, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
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
			r.failed = true
			return nil, err
		}
		if len(values) != len(missing) {
			r.failed = true
			return nil, ErrUnavailable
		}
		for i, refs := range values {
			for _, ref := range refs {
				if ref.Validate() != nil || (missing[i].Reverse && ref.Relation == "") {
					r.failed = true
					return nil, ErrUnavailable
				}
			}
			refs = slices.Clone(refs)
			sort.Slice(refs, func(i, j int) bool {
				if refs[i].Type != refs[j].Type {
					return refs[i].Type < refs[j].Type
				}
				if refs[i].ID != refs[j].ID {
					return refs[i].ID < refs[j].ID
				}
				return refs[i].Relation < refs[j].Relation
			})
			r.sets[missing[i]] = slices.Compact(refs)
		}
	}
	out := make([][]relationships.SubjectRef, len(keys))
	for i, key := range keys {
		out[i] = slices.Clone(r.sets[key])
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
		keys[i] = SetKey{Ref: relationships.SubjectRef{Type: rt, ID: id, Relation: relation}}
	}
	sets, err := r.raw.read(ctx, keys)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]relationships.RelationTarget, len(ids))
	for i, refs := range sets {
		for _, ref := range refs {
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
			keys[i] = SetKey{Reverse: true, Ref: ref}
		}
		sets, err := r.raw.read(ctx, keys)
		if err != nil {
			return nil, err
		}
		var next []relationships.SubjectRef
		for i, refs := range sets {
			for _, ref := range refs {
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
