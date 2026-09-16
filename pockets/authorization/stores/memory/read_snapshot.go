package memory

import (
	"context"
	"fmt"
	"maps"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Store) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	return s.tup.ReadTupleSnapshot(ctx, fn)
}
func (t *Tuples) ReadTupleSnapshot(ctx context.Context, fn func(context.Context, tuples.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil snapshot callback: %w", sdk.ErrInvalidInput)
	}
	t.st.mu.Lock()
	snapshot := &state{facts: maps.Clone(t.st.facts)}
	t.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	view := &snapshotReads{st: snapshot, ctx: ctx}
	defer view.closed.Store(true)
	if err := fn(ctx, view); err != nil {
		return err
	}
	return ctx.Err()
}

type snapshotReads struct {
	ctx    context.Context
	st     *state
	closed atomic.Bool
}

func (s *snapshotReads) check(ctx context.Context) error {
	if s.closed.Load() {
		return tuples.ErrSnapshotClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
func (s *snapshotReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &snapshotCheckReader{view: s, reader: (&Relationships{st: s.st}).ForModel(model)}
}
func (s *snapshotReads) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	return (&Tuples{st: s.st}).Contains(ctx, t)
}
func (s *snapshotReads) ContainsMany(ctx context.Context, t []tuples.Tuple) ([]bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return (&Tuples{st: s.st}).ContainsMany(ctx, t)
}
func (s *snapshotReads) ReadSets(ctx context.Context, k []tuples.SetKey, n int) ([][]tuples.Tuple, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return (&Tuples{st: s.st}).ReadSets(ctx, k, n)
}
func (s *snapshotReads) Lookup(ctx context.Context, q tuples.Query) ([]tuples.Tuple, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return (&Tuples{st: s.st}).Lookup(ctx, q)
}
func (s *snapshotReads) ForModel(m relationships.ReadModel) relationships.Reader {
	return &snapshotCheckReader{view: s, reader: (&Relationships{st: s.st}).ForModel(m)}
}

type snapshotCheckReader struct {
	view   *snapshotReads
	reader relationships.Reader
}

func (r *snapshotCheckReader) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) ([]string, error) {
	if err := r.view.check(ctx); err != nil {
		return nil, err
	}
	return r.reader.(relationships.RelationSetReader).FilterRelation(ctx, rt, ids, rel, st, sid, limit)
}

func (r *snapshotCheckReader) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]relationships.RelationTarget, error) {
	if err := r.view.check(ctx); err != nil {
		return nil, err
	}
	return r.reader.(relationships.RelationSetReader).RelationTargetsFor(ctx, rt, ids, rel)
}

func (r *snapshotCheckReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, id, rel, st, sid string, limit int) (bool, error) {
	if err := r.view.check(ctx); err != nil {
		return false, err
	}
	return r.reader.CheckRelationWithGroupExpansion(ctx, rt, id, rel, st, sid, limit)
}
func (r *snapshotCheckReader) GetRelationTargets(ctx context.Context, rt, id, rel string) ([]relationships.RelationTarget, error) {
	if err := r.view.check(ctx); err != nil {
		return nil, err
	}
	return r.reader.GetRelationTargets(ctx, rt, id, rel)
}
func (r *snapshotCheckReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) (map[string]bool, error) {
	if err := r.view.check(ctx); err != nil {
		return nil, err
	}
	return r.reader.CheckBatchDirect(ctx, rt, ids, rel, st, sid, limit)
}
