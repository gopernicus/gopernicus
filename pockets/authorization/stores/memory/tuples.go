package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gopernicus/gopernicus/pockets/authorization/internal/tuplekey"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// Tuples owns the single canonical fact authority shared by every facade.
type Tuples struct{ st *state }

func NewTuples() *Tuples { return &Tuples{st: newState()} }

var _ tuples.Storer = (*Tuples)(nil)

func (t *Tuples) Contains(ctx context.Context, f tuples.Tuple) (bool, error) {
	if err := f.Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, held := t.st.facts[f]
	return held, nil
}
func (t *Tuples) ContainsMany(ctx context.Context, facts []tuples.Tuple) ([]bool, error) {
	for _, f := range facts {
		if err := f.Validate(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	out := make([]bool, len(facts))
	for i, f := range facts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, out[i] = t.st.facts[f]
	}
	return out, nil
}
func (t *Tuples) ReadSets(ctx context.Context, keys []tuples.SetKey, maxResults int) ([][]tuples.Tuple, error) {
	if maxResults < 0 {
		return nil, sdk.ErrInvalidInput
	}
	for _, k := range keys {
		if err := k.Validate(); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	out := make([][]tuples.Tuple, len(keys))
	count := 0
	for i, k := range keys {
		for f := range t.st.facts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if (k.Reverse && f.Subject == k.Subject) || (!k.Reverse && f.Scope == k.Scope && f.Relation == k.Relation) {
				count++
				if maxResults > 0 && count > maxResults {
					return nil, tuples.ErrReadLimit
				}
				out[i] = append(out[i], f)
			}
		}
		sortTuples(out[i])
	}
	return out, nil
}
func sortTuples(facts []tuples.Tuple) {
	slices.SortFunc(facts, tuples.Compare)
}
func (t *Tuples) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.st.mu.Lock()
	defer t.st.mu.Unlock()
	var out []tuples.Tuple
	for f := range t.st.facts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if q.Matches(f) && (q.After == nil || tuples.Compare(f, *q.After) > 0) {
			out = append(out, f)
		}
	}
	sortTuples(out)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
func (t *Tuples) ListTuples(ctx context.Context, q tuples.Query, req list.Request) (list.Page[tuples.Tuple], error) {
	if q.After != nil || q.Limit != 0 {
		return list.Page[tuples.Tuple]{}, fmt.Errorf("list query cannot carry a second cursor/limit: %w", sdk.ErrInvalidInput)
	}
	if strings.TrimSpace(req.Search) != "" {
		return list.Page[tuples.Tuple]{}, fmt.Errorf("tuple search is not supported: %w", sdk.ErrInvalidInput)
	}
	all, err := t.Lookup(ctx, q)
	if err != nil {
		return list.Page[tuples.Tuple]{}, err
	}
	return pageMemByKey(all, req, map[string]list.OrderField{"tuple_key": {Column: "tuple_key"}}, "tuple_key", tuplekey.Encode)
}
func (t *Tuples) ApplyTuples(ctx context.Context, c tuples.Changes) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if len(c.Add)+len(c.Remove) > 4096 {
		return fmt.Errorf("tuple batch too large: %w", sdk.ErrInvalidInput)
	}
	scopes := make([]tuples.Scope, 0, len(c.Add)+len(c.Remove))
	for _, fact := range c.Add {
		scopes = append(scopes, fact.Scope)
	}
	for _, fact := range c.Remove {
		scopes = append(scopes, fact.Scope)
	}
	return t.st.writeScopes(ctx, scopes, func(next *state) error { next.applyLocked(c); return ctx.Err() })
}
func (t *Tuples) ReconcileTuples(ctx context.Context, scope tuples.Scope, relation string, subjects []tuples.SubjectRef) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("relation", relation); err != nil {
		return err
	}
	if len(subjects) > 4096 {
		return sdk.ErrInvalidInput
	}
	desired := make(map[tuples.Tuple]struct{}, len(subjects))
	for _, s := range subjects {
		if err := s.Validate(); err != nil {
			return err
		}
		desired[tuples.Tuple{Scope: scope, Relation: relation, Subject: s}] = struct{}{}
	}
	return t.st.writeScopes(ctx, []tuples.Scope{scope}, func(next *state) error {
		for f := range next.facts {
			if f.Scope == scope && f.Relation == relation {
				delete(next.facts, f)
			}
		}
		for f := range desired {
			next.facts[f] = struct{}{}
		}
		return ctx.Err()
	})
}
func (t *Tuples) DeleteScope(ctx context.Context, scope tuples.Scope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	return t.st.writeScopes(ctx, []tuples.Scope{scope}, func(next *state) error {
		for f := range next.facts {
			if f.Scope == scope {
				delete(next.facts, f)
			}
		}
		return ctx.Err()
	})
}
