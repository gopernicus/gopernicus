package memory

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// WithCacheReads enables atomic generation tracking and combined snapshots.
// Each Store is a process-local, independently identified authority.
func WithCacheReads() Option { return func(c *storeConfig) { c.cacheReads = true } }

// CacheSource returns nil unless the shared bundle enables cache reads.
func (s *Store) CacheSource() decisions.CacheSource {
	if s.rel.st.cacheEpoch == "" {
		return nil
	}
	return &cacheSource{st: s.rel.st}
}
func (r *Relationships) CacheBinding() string { return r.st.cacheBinding() }
func (r *Roles) CacheBinding() string         { return r.st.cacheBinding() }
func (s *state) cacheBinding() string {
	if s.cacheEpoch == "" {
		return ""
	}
	return "memory/authorization-check-reads/v1/" + s.cacheEpoch
}

type cacheSource struct{ st *state }

func (s *cacheSource) CacheBinding() string                  { return s.st.cacheBinding() }
func (s *cacheSource) CacheableContext(context.Context) bool { return true }
func (s *cacheSource) Observe(ctx context.Context) (decisions.CacheVersion, error) {
	if err := ctx.Err(); err != nil {
		return decisions.CacheVersion{}, err
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return decisions.CacheVersion{}, err
	}
	v := decisions.CacheVersion{Epoch: s.st.cacheEpoch, Generation: s.st.cacheGeneration}
	return v, v.Validate()
}
func (s *cacheSource) ReadSnapshot(ctx context.Context, fn func(context.Context, decisions.CacheVersion, decisions.CheckReads) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil snapshot callback: %w", sdk.ErrInvalidInput)
	}
	s.st.mu.Lock()
	snapshot := &state{rel: slices.Clone(s.st.rel), role: slices.Clone(s.st.role)}
	v := decisions.CacheVersion{Epoch: s.st.cacheEpoch, Generation: s.st.cacheGeneration}
	s.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := v.Validate(); err != nil {
		return err
	}
	view := &snapshotReads{st: snapshot, ctx: ctx}
	defer view.closed.Store(true)
	if err := fn(ctx, v, view); err != nil {
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
		return decisions.ErrSnapshotClosed
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
