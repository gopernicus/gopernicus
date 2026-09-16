package relationships

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Service) withCheckReader(ctx context.Context, needsReads bool, evaluate func(context.Context, CheckReader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if snapshot, ok := s.reader.(LookupSnapshotter); ok && needsReads && !s.inSnapshot {
		err = snapshot.ReadLookupSnapshot(ctx, func(ctx context.Context, reader Reader) error {
			if isNilReader(reader) {
				return fmt.Errorf("authorization: nil check snapshot reader: %w", sdk.ErrInvalidInput)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			err := evaluate(ctx, reader)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		})
	} else {
		err = evaluate(ctx, s.reader)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
