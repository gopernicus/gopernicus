package decisions

import "github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"

func (s *Service) TupleCache() *tuplecache.TupleCache {
	if s == nil {
		return nil
	}
	return s.tupleCache
}
