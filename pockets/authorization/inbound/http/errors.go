package authorizationhttp

import (
	"errors"
	"net/http"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// errorResponse maps a pocket sentinel to a *web.Error carrying its named
// machine [relationships.Reason] code, following auth v3's pocket-local mapper precedent. It
// returns ok=false for anything else so the caller falls back to the generic
// sdk-kind mapping. The sdk mapper (web.ErrFromDomain) is untouched.
func errorResponse(err error) (*web.Error, bool) {
	switch {
	case errors.Is(err, authmodel.ErrEvaluationLimit), errors.Is(err, sdk.ErrUnavailable):
		return web.NewError(http.StatusServiceUnavailable, "authorization temporarily unavailable").WithCode(string(authmodel.ReasonEvaluationLimit)), true
	case errors.Is(err, mutations.ErrConcurrentMutation):
		return web.NewError(http.StatusConflict, "concurrent authorization change").WithCode(string(authmodel.ReasonConcurrentMutation)), true
	case errors.Is(err, mutations.ErrInvariantBlocked):
		return web.NewError(http.StatusConflict, "invariant conflict").WithCode(string(authmodel.ReasonInvariantConflict)), true
	case errors.Is(err, mutations.ErrSemanticConflict):
		return web.NewError(http.StatusConflict, "relationship conflict").WithCode(string(authmodel.ReasonSemanticConflict)), true
	case errors.Is(err, authmodel.ErrUnknownSymbol):
		return web.NewError(http.StatusBadRequest, "unknown model symbol").WithCode(string(authmodel.ReasonUnknownSymbol)), true
	case errors.Is(err, authmodel.ErrInvalidRequest):
		return web.NewError(http.StatusBadRequest, "invalid request").WithCode(string(authmodel.ReasonInvalidRequest)), true
	default:
		return nil, false
	}
}

// RespondError writes a domain error using stable authorization codes, falling
// back to the SDK's domain-error mapping for other errors.
func RespondError(w http.ResponseWriter, err error) {
	if mapped, ok := errorResponse(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	web.RespondJSONDomainError(w, err)
}
