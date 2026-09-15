package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

type tupleSource struct {
	db      *tursodb.DB
	cfg     config
	binding string
}

func (s *tupleSource) Binding() string { return s.cfg.tupleBinding }
func (s *tupleSource) CacheableContext(ctx context.Context) bool {
	_, ambient := tursodb.TxFromContext(ctx)
	return !ambient
}
func (s *tupleSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) (err error) {
	if !s.CacheableContext(ctx) || fn == nil {
		return fmt.Errorf("authorization cache: ambient transaction or nil callback: %w", sdk.ErrInvalidInput)
	}
	tx, err := s.db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); err != nil && rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrConnDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	_, _, err = s.readHead(ctx, tx)
	if err != nil {
		return err
	}
	view := &cacheSnapshot{ctx: ctx, rel: newRelationshipStore(s.db, s.cfg), role: newRoleStore(s.db, s.cfg)}
	view.rel.readQuerier = tx
	view.role.readQuerier = tx
	defer view.closed.Store(true)
	if err = fn(ctx, view); err != nil {
		return err
	}
	view.closed.Store(true)
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

type cacheSnapshot struct {
	ctx    context.Context
	closed atomic.Bool
	rel    *relationshipStore
	role   *roleStore
}

func (s *cacheSnapshot) check(ctx context.Context) error {
	if s.closed.Load() {
		return tuplecache.ErrSnapshotClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
func (s *cacheSnapshot) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return &cacheCheckReader{view: s, reader: s.rel.ForModel(model)}
}
func (s *cacheSnapshot) HasExactRole(ctx context.Context, st, sid, role, rt, rid string) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	return s.role.HasExactRole(ctx, st, sid, role, rt, rid)
}

type cacheCheckReader struct {
	view   *cacheSnapshot
	reader relationships.Reader
}

func (s *cacheCheckReader) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.(relationships.RelationSetReader).FilterRelation(ctx, rt, ids, rel, st, sid, limit)
}

func (s *cacheCheckReader) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]relationships.RelationTarget, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.(relationships.RelationSetReader).RelationTargetsFor(ctx, rt, ids, rel)
}

func (s *cacheCheckReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, relation, st, sid string, limit int) (bool, error) {
	if err := s.view.check(ctx); err != nil {
		return false, err
	}
	return s.reader.CheckRelationWithGroupExpansion(ctx, rt, rid, relation, st, sid, limit)
}
func (s *cacheCheckReader) GetRelationTargets(ctx context.Context, rt, rid, relation string) ([]relationships.RelationTarget, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.GetRelationTargets(ctx, rt, rid, relation)
}
func (s *cacheCheckReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, relation, st, sid string, limit int) (map[string]bool, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.CheckBatchDirect(ctx, rt, ids, relation, st, sid, limit)
}
func (s *relationshipStore) TupleCacheBinding() string { return s.tupleBinding }
func (s *roleStore) TupleCacheBinding() string         { return s.tupleBinding }
func (s *roleStore) cacheReader(ctx context.Context) tursodb.Querier {
	q := s.readQuerier
	if q == nil {
		q = s.db.QuerierFrom(ctx)
	}
	if s.tupleBinding != "" {
		q = mainCacheQuerier{Querier: q}
	}
	return q
}

// Only adapter-owned static read statements reach this wrapper. Qualify actual
// facts after model rewriting so TEMP objects cannot shadow validated main data.
type mainCacheQuerier struct{ tursodb.Querier }

func mainCacheSQL(q string) string {
	return strings.NewReplacer("iam_relationships", "main.iam_relationships", "iam_roles", "main.iam_roles").Replace(q)
}
func (q mainCacheQuerier) Query(ctx context.Context, sql string, args ...any) (*sql.Rows, error) {
	return q.Querier.Query(ctx, mainCacheSQL(sql), args...)
}
func (q mainCacheQuerier) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return q.Querier.QueryRow(ctx, mainCacheSQL(query), args...)
}
