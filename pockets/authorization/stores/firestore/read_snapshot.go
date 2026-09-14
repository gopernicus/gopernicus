package firestore

import (
	"context"
	"fmt"
	"sync/atomic"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

type cacheSource struct {
	epoch   string
	db      *firestoredb.DB
	binding string
}

func (s *cacheSource) CacheBinding() string                      { return s.binding }
func (s *relationshipStore) CacheBinding() string                { return s.binding }
func (s *roleStore) CacheBinding() string                        { return s.binding }
func (s *cacheSource) CacheableContext(ctx context.Context) bool { return refuseAmbient(ctx) == nil }
func (s *cacheSource) Observe(ctx context.Context) (decisions.CacheVersion, error) {
	if err := refuseAmbient(ctx); err != nil {
		return decisions.CacheVersion{}, err
	}
	version, err := readCacheHead(ctx, s.db, s.db.ReaderFrom(ctx))
	if err == nil && version.Epoch != s.epoch {
		return decisions.CacheVersion{}, decisions.ErrCacheVersion
	}
	return version, err
}
func (s *cacheSource) ReadSnapshot(ctx context.Context, fn func(context.Context, decisions.CacheVersion, decisions.CheckReads) error) error {
	if err := refuseAmbient(ctx); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil snapshot callback: %w", sdk.ErrInvalidInput)
	}
	return s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		version, err := readCacheHead(ctx, s.db, r)
		if err != nil {
			return err
		}
		if version.Epoch != s.epoch {
			return decisions.ErrCacheVersion
		}
		view := &snapshotReads{db: s.db, reader: r, ctx: ctx}
		defer view.closed.Store(true)
		if err := fn(ctx, version, view); err != nil {
			return err
		}
		return ctx.Err()
	})
}

type snapshotReads struct {
	db     *firestoredb.DB
	reader firestoredb.Reader
	ctx    context.Context
	closed atomic.Bool
}

func (v *snapshotReads) check(ctx context.Context) error {
	if v.closed.Load() {
		return decisions.ErrSnapshotClosed
	}
	if err := v.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
func (v *snapshotReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &snapshotCheckReader{view: v, model: model}
}
func (v *snapshotReads) HasExactRole(ctx context.Context, st, sid, role, rt, rid string) (bool, error) {
	if err := v.check(ctx); err != nil {
		return false, err
	}
	return roleExists(ctx, v.db, v.reader, st, sid, role, rt, rid)
}

type snapshotCheckReader struct {
	view  *snapshotReads
	model relationships.ReadModel
}

func (s *snapshotCheckReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, rel, st, sid string, limit int) (bool, error) {
	v := s.view
	if err := v.check(ctx); err != nil {
		return false, err
	}
	reached, err := expand(ctx, v.db, v.reader, st, sid, limit, &s.model)
	if err != nil {
		return false, err
	}
	return anyTupleWithSubject(ctx, v.db, v.reader, rt, rid, rel, reached, &s.model)
}
func (s *snapshotCheckReader) GetRelationTargets(ctx context.Context, rt, rid, rel string) ([]relationships.RelationTarget, error) {
	v := s.view
	if err := v.check(ctx); err != nil {
		return nil, err
	}
	return relationTargets(ctx, v.db, v.reader, rt, rid, rel, &s.model)
}
func (s *snapshotCheckReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) (map[string]bool, error) {
	v := s.view
	if err := v.check(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = false
	}
	distinct := distinctSortedIDs(ids)
	if len(distinct) == 0 {
		return out, nil
	}
	matched, err := filterRelation(ctx, v.db, v.reader, rt, distinct, rel, st, sid, limit, &s.model)
	if err != nil {
		return nil, err
	}
	for _, id := range matched {
		out[id] = true
	}
	return out, nil
}
