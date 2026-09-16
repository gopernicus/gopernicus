package turso

import (
	"context"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
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
func (s *tupleSource) ReadSnapshot(ctx context.Context, fn func(context.Context, tuplecache.CheckReads) error) error {
	return newTupleStore(s.db, s.cfg).ReadTupleSnapshot(ctx, fn)
}
func (s *tupleStore) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	return s.ForModel(model)
}
func (s *tupleStore) ForModel(model relationships.ReadModel) relationships.Reader {
	rel := newRelationshipStore(s.db, s.cfg)
	rel.readQuerier = s.readQuerier
	return &cacheCheckReader{view: s, reader: rel.ForModel(model)}
}

type cacheCheckReader struct {
	view   *tupleStore
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
