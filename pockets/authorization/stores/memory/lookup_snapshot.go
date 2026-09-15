package memory

import (
	"context"
	"fmt"
	"slices"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ relationships.LookupSnapshotter = (*Relationships)(nil)

// ReadLookupSnapshot owns one model-scoped relationship view for the callback.
func (s *Relationships) ReadLookupSnapshot(ctx context.Context, fn func(context.Context, relationships.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("authorization: nil lookup snapshot callback: %w", sdk.ErrInvalidInput)
	}
	s.st.mu.Lock()
	snapshot := &state{rel: slices.Clone(s.st.rel)}
	s.st.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	view := &snapshotReads{ctx: ctx, st: snapshot}
	defer view.closed.Store(true)
	reader := &Relationships{st: snapshot, model: s.model}
	if err := fn(ctx, &snapshotCheckReader{view: view, reader: reader}); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *snapshotCheckReader) LookupResourceIDs(ctx context.Context, rt string, relations []string, st, sid, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupResourceIDs(ctx, rt, relations, st, sid, after, limit)
}
func (s *snapshotCheckReader) LookupResourceIDsByRelationTarget(ctx context.Context, rt, relation, targetType string, targetIDs []string, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupResourceIDsByRelationTarget(ctx, rt, relation, targetType, targetIDs, after, limit)
}
func (s *snapshotCheckReader) LookupDescendantResourceIDs(ctx context.Context, rt string, relations []string, st string, roots []string, after string, limit int) ([]string, error) {
	if err := s.view.check(ctx); err != nil {
		return nil, err
	}
	return s.reader.LookupDescendantResourceIDs(ctx, rt, relations, st, roots, after, limit)
}
