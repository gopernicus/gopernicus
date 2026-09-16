package relationships

import (
	"context"
	"errors"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

// Only our discovery/verification mismatch is retryable, not an arbitrary
// conflict or other error from a store.
var errEnumerationChanged = errors.New("authorization: relationships changed during enumeration")

func (s *Service) runLookup(ctx context.Context, evaluate func(context.Context, *Service) (authmodel.LookupResult, error)) (authmodel.LookupResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return authmodel.LookupResult{}, err
		}
		var result authmodel.LookupResult
		var err error
		if snapshots, ok := s.reader.(LookupSnapshotter); ok {
			err = snapshots.ReadLookupSnapshot(ctx, func(ctx context.Context, reader Reader) error {
				if isNilReader(reader) {
					return fmt.Errorf("authorization: nil lookup snapshot reader: %w", sdk.ErrInvalidInput)
				}
				view := *s
				view.reader = reader
				view.inSnapshot = true
				var evaluateErr error
				result, evaluateErr = evaluate(ctx, &view)
				return evaluateErr
			})
		} else {
			result, err = evaluate(ctx, s)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return authmodel.LookupResult{}, ctxErr
		}
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errEnumerationChanged) {
			return authmodel.LookupResult{}, err
		}
	}
	return authmodel.LookupResult{}, authmodel.ErrEnumerationContended
}
