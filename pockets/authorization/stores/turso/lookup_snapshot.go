package turso

import "context"

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
