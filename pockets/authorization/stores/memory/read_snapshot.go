package memory

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

// ReadSnapshot evaluates against an owned, callback-scoped copy of this store.
func (s *Store) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil snapshot callback: %w", sdk.ErrInvalidInput)
	}
	s.rel.st.mu.Lock()
	snapshot := &state{rel: slices.Clone(s.rel.st.rel), role: slices.Clone(s.rel.st.role)}
	s.rel.st.mu.Unlock()
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
		return tuplecache.ErrSnapshotClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
func (s *snapshotReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &snapshotCheckReader{view: s, reader: (&Relationships{st: s.st}).ForModel(model)}
}
func (s *snapshotReads) HasExactRole(ctx context.Context, st, sid, role, rt, rid string) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	return (&Roles{st: s.st}).HasExactRole(ctx, st, sid, role, rt, rid)
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
