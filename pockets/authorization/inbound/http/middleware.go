package authorizationhttp

import (
	"errors"
	"net/http"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// ErrAlternativeNotApplicable makes a resolved predicate false. Hosts may wrap
// it when an input does not apply to a request. All other resolver errors fail
// the operation closed; this sentinel never suppresses a decision-store error.
var ErrAlternativeNotApplicable = errors.New("authorization: gate alternative not applicable to this request")

// ResourceResolver extracts a resource from a request. Resource declares its
// expected type; Require resolves it lazily through the decision service.
type ResourceResolver func(r *http.Request) (authmodel.Resource, error)
