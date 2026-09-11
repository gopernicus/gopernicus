package mutations

import (
	"errors"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

func ReasonFor(err error) (authmodel.Reason, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, authmodel.ErrEvaluationLimit):
		return authmodel.ReasonEvaluationLimit, true
	case errors.Is(err, ErrConcurrentMutation):
		return authmodel.ReasonConcurrentMutation, true
	case errors.Is(err, ErrInvariantBlocked):
		return authmodel.ReasonInvariantConflict, true
	case errors.Is(err, ErrSemanticConflict):
		return authmodel.ReasonSemanticConflict, true
	case errors.Is(err, authmodel.ErrUnknownSymbol):
		return authmodel.ReasonUnknownSymbol, true
	case errors.Is(err, authmodel.ErrInvalidRequest), errors.Is(err, sdk.ErrInvalidInput):
		return authmodel.ReasonInvalidRequest, true
	case errors.Is(err, sdk.ErrUnavailable):
		return authmodel.ReasonEvaluationLimit, true
	default:
		return "", false
	}
}
