package pgx

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	jackpgx "github.com/jackc/pgx/v5"
)

var _ relationships.LookupSnapshotter = (*relationshipStore)(nil)

// ReadLookupSnapshot keeps reverse discovery and forward verification in the
// same durable view. It does not require TupleCache configuration or migrations.
func (s *relationshipStore) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, relationships.Reader) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil lookup snapshot callback: %w", sdk.ErrInvalidInput)
	}
	reader := *s
	var owned *pgxdb.Tx
	if reader.readQuerier == nil {
		if ambient, ok := pgxdb.TxFromContext(ctx); ok {
			// Preserve the caller's pending writes, isolation and transaction ownership.
			reader.readQuerier = ambient
		} else {
			owned, err = s.db.BeginRead(ctx)
			if err != nil {
				return err
			}
			defer func() {
				if rollbackErr := owned.Rollback(); err != nil && rollbackErr != nil && !errors.Is(rollbackErr, jackpgx.ErrTxClosed) {
					err = errors.Join(err, rollbackErr)
				}
			}()
			reader.readQuerier = owned
		}
	}
	view := &cacheSnapshot{ctx: ctx, rel: &reader}
	defer view.closed.Store(true)
	if err = fn(ctx, &cacheCheckReader{view: view, reader: &reader}); err != nil {
		return err
	}
	view.closed.Store(true)
	if err = ctx.Err(); err != nil {
		return err
	}
	if owned != nil {
		return owned.Commit()
	}
	return nil
}

func (s *cacheCheckReader) LookupResourceIDs(ctx context.Context, rt string, relations []string, st, sid, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupResourceIDs(ctx, rt, relations, st, sid, after, limit)
}
func (s *cacheCheckReader) LookupResourceIDsByRelationTarget(ctx context.Context, rt, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupResourceIDsByRelationTarget(ctx, rt, relation, targetType, targetIDs, after, limit)
}
func (s *cacheCheckReader) LookupDescendantResourceIDs(ctx context.Context, rt string, relations []string, st string, roots []string, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupDescendantResourceIDs(ctx, rt, relations, st, roots, after, limit)
}
