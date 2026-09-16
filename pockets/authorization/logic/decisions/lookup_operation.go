package decisions

import (
	"context"
	"errors"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

var errEnumerationChanged = errors.New("authorization: tuples changed during enumeration")

func (s *Service) runLookup(ctx context.Context, evaluate func(context.Context, *Service) (authmodel.LookupResult, error)) (authmodel.LookupResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var result authmodel.LookupResult
		err := s.withOperation(ctx, func(ctx context.Context, view *Service) error { var e error; result, e = evaluate(ctx, view); return e })
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errEnumerationChanged) {
			return authmodel.LookupResult{}, err
		}
	}
	return authmodel.LookupResult{}, authmodel.ErrEnumerationContended
}
