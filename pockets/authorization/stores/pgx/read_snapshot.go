package pgx

import (
	"context"

	"errors"
	"fmt"
	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	jackpgx "github.com/jackc/pgx/v5"
	"sync/atomic"
)

type cacheSource struct {
	db    *pgxdb.DB
	cfg   config
	epoch string
}

func (s *cacheSource) CacheBinding() string { return s.cfg.cacheBinding }
func (s *cacheSource) CacheableContext(ctx context.Context) bool {
	_, ambient := pgxdb.TxFromContext(ctx)
	return !ambient
}
func (s *cacheSource) Observe(ctx context.Context) (decisions.CacheVersion, error) {
	if !s.CacheableContext(ctx) {
		return decisions.CacheVersion{}, fmt.Errorf("authorization cache: ambient observation: %w", sdk.ErrInvalidInput)
	}
	return s.readHead(ctx, s.db)
}
func (s *cacheSource) ReadSnapshot(ctx context.Context, fn func(context.Context, decisions.CacheVersion, decisions.CheckReads) error) (err error) {
	if !s.CacheableContext(ctx) || fn == nil {
		return fmt.Errorf("authorization cache: ambient transaction or nil callback: %w", sdk.ErrInvalidInput)
	}
	tx, err := s.db.BeginRead(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); err != nil && rollbackErr != nil && !errors.Is(rollbackErr, jackpgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	version, err := s.readHead(ctx, tx)
	if err != nil {
		return err
	}
	view := &cacheSnapshot{ctx: ctx, rel: newRelationshipStore(s.db, s.cfg), role: newRoleStore(s.db, s.cfg)}
	view.rel.readQuerier = tx
	view.role.readQuerier = tx
	defer view.closed.Store(true)
	if err = fn(ctx, version, view); err != nil {
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
		return decisions.ErrSnapshotClosed
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
func (s *relationshipStore) CacheBinding() string { return s.cacheBinding }
func (s *roleStore) CacheBinding() string         { return s.cacheBinding }
func (s *roleStore) cacheReader(ctx context.Context) pgxdb.Querier {
	q := s.readQuerier
	if q == nil {
		q = s.db.QuerierFrom(ctx)
	}

	return q
}
