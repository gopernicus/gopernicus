package decisions

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type Storer = relationships.Storer
type SubjectRef = relationships.SubjectRef
type SubjectRelationshipFilter = relationships.SubjectRelationshipFilter
type ResourceRelationshipFilter = relationships.ResourceRelationshipFilter
type SubjectRelationship = relationships.SubjectRelationship
type ResourceRelationship = relationships.ResourceRelationship
type serviceConfig struct{ limits authmodel.EvaluationLimits }

func newService(store Storer, model Model, cfg serviceConfig) (*Service, error) {
	if isNilReader(store) {
		return NewService(nil, WithModel(model), WithLimits(cfg.limits))
	}
	f := &graphFixture{store: store}
	s, err := NewService(f, WithModel(model), WithLimits(cfg.limits))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSchema, err)
	}
	if isNilReader(store.ForModel(s.readModel)) {
		return nil, fmt.Errorf("nil graph fixture: %w", sdk.ErrInvalidInput)
	}
	return s, err
}

type graphFixture struct {
	store    Storer
	reader   Reader
	snapshot func(context.Context, func(context.Context, tuples.Reader) error) error
}

func (f *graphFixture) ForModel(m ReadModel) Reader {
	if f.reader != nil {
		return f.reader
	}
	return f.store.ForModel(m)
}
func (f *graphFixture) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	if f.snapshot != nil {
		return f.snapshot(ctx, fn)
	}
	return fn(ctx, f)
}

// graphSnapshotFacts supplies optimized graph reads within a canonical snapshot.
type graphSnapshotFacts struct {
	graphOnlyFacts
	Reader
}

func (f graphSnapshotFacts) ForModel(ReadModel) Reader { return f.Reader }

func (f *graphFixture) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	return f.store.CheckRelationExists(ctx, t.Scope.Type, t.Scope.ID, t.Relation, t.Subject.Type, t.Subject.ID)
}
func (f *graphFixture) ContainsMany(ctx context.Context, ts []tuples.Tuple) ([]bool, error) {
	out := make([]bool, len(ts))
	for i, t := range ts {
		v, err := f.Contains(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
func (f *graphFixture) ReadSets(ctx context.Context, keys []tuples.SetKey, max int) ([][]tuples.Tuple, error) {
	return nil, fmt.Errorf("fixture raw sets unused")
}
func (f *graphFixture) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	return nil, fmt.Errorf("fixture raw lookup unused")
}

// These fixtures exercise graph reads only, keeping their original counters and
// injected errors while satisfying the canonical bound-view contract.
type graphOnlyFacts struct{}

func (graphOnlyFacts) Contains(context.Context, tuples.Tuple) (bool, error) {
	return false, fmt.Errorf("unexpected exact read")
}
func (graphOnlyFacts) ContainsMany(context.Context, []tuples.Tuple) ([]bool, error) {
	return nil, fmt.Errorf("unexpected exact batch")
}
func (graphOnlyFacts) ReadSets(context.Context, []tuples.SetKey, int) ([][]tuples.Tuple, error) {
	return nil, fmt.Errorf("unexpected raw set read")
}
func (graphOnlyFacts) Lookup(context.Context, tuples.Query) ([]tuples.Tuple, error) {
	return nil, fmt.Errorf("unexpected raw tuple lookup")
}

type permissionChecks struct{ PermissionReader }

func (p permissionChecks) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		v, err := p.CheckRelationWithGroupExpansion(ctx, rt, id, rel, st, sid, max)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}
func (r *recordingReader) ForChecks(m ReadModel) CheckReader { return permissionChecks{r.ForModel(m)} }
func (v modelStoreView) ForChecks(m ReadModel) CheckReader   { return permissionChecks{v.ForModel(m)} }

func (v modelStoreView) Contains(c context.Context, t tuples.Tuple) (bool, error) {
	return graphOnlyFacts{}.Contains(c, t)
}
func (v modelStoreView) ContainsMany(c context.Context, t []tuples.Tuple) ([]bool, error) {
	return graphOnlyFacts{}.ContainsMany(c, t)
}
func (v modelStoreView) ReadSets(c context.Context, k []tuples.SetKey, n int) ([][]tuples.Tuple, error) {
	return graphOnlyFacts{}.ReadSets(c, k, n)
}
func (v modelStoreView) Lookup(c context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	return graphOnlyFacts{}.Lookup(c, q)
}
