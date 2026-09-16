package authorizationhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Require mounts a nested policy over the context principal. It validates every
// branch at mount, then evaluates the entire expression in one tuple snapshot
// with shared budgets and ordered short-circuiting. Resource inputs are lazy and
// memoized for the request, including cache fallback. Missing authentication is
// 401; denial is 403; evaluation exhaustion is 503; other errors are 500. Errors
// always fail closed. Invalid configuration panics before traffic.
func (a *Adapter) Require(predicate Predicate) web.Middleware {
	if a == nil || a.decisions == nil || typedNil(a.decisions) {
		panic("authorization: Require requires a decision service")
	}
	evaluator := a.decisions
	expression, resolvers, err := lowerPredicate(predicate)
	if err == nil {
		err = evaluator.ValidateExpression(expression)
	}
	if err != nil {
		panic(fmt.Sprintf("authorization: Require: %s", err))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := sdk.PrincipalFromContext(r.Context())
			if !ok {
				web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
				return
			}
			resolve := func(ctx context.Context, key string) (authmodel.Resource, error) {
				resolver, ok := resolvers[key]
				if !ok {
					return authmodel.Resource{}, fmt.Errorf("authorization: unknown resource input %q: %w", key, sdk.ErrInvalidInput)
				}
				resource, err := resolver(r.WithContext(ctx))
				if errors.Is(err, ErrAlternativeNotApplicable) {
					return authmodel.Resource{}, decisions.ErrResourceNotApplicable
				}
				return resource, err
			}
			result, err := evaluator.EvaluateResolved(r.Context(), authmodel.PrincipalFrom(principal), expression, resolve)
			if err == nil {
				err = r.Context().Err()
			}
			if err != nil {
				if errors.Is(err, authmodel.ErrEvaluationLimit) {
					web.RespondJSONError(w, web.NewError(http.StatusServiceUnavailable, "authorization temporarily unavailable"))
				} else {
					web.RespondJSONError(w, web.ErrInternal("internal error"))
				}
				return
			}
			if !result.Allowed {
				web.RespondJSONError(w, web.ErrForbidden("permission denied"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
