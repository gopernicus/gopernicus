package documents

import (
	"context"
	"net/http"
	"strconv"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type Service interface {
	ListVisible(context.Context, sdk.Principal, domain.Query) (domain.Page, error)
}

// Handler sits behind the host's authentication middleware. No permission IDs
// or storage strategy appear in the HTTP flow.
func Handler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sdk.PrincipalFromContext(r.Context())
		if !ok {
			web.RespondJSONDomainError(w, sdk.ErrUnauthorized)
			return
		}
		values := r.URL.Query()
		query := domain.Query{TenantID: r.PathValue("tenant"), Search: values.Get("q"), Cursor: values.Get("cursor")}
		if raw := values.Get("limit"); raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil {
				web.RespondJSONDomainError(w, sdk.ErrInvalidInput)
				return
			}
			query.Limit = limit
		}
		switch values.Get("sort") {
		case "", "name":
		case "-name":
			query.Desc = true
		default:
			web.RespondJSONDomainError(w, sdk.ErrInvalidInput)
			return
		}
		page, err := service.ListVisible(r.Context(), principal, query)
		if err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
		_ = web.RespondJSONOK(w, page)
	}
}
