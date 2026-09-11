package catalog

import (
	"context"
	"net/http"

	"github.com/gopernicus/gopernicus/examples/minimal/internal/logic/domains/catalog"
	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type Service interface {
	List(context.Context) ([]catalog.Item, error)
}

// Mount exposes the host's public data projection. The service caches data; this
// JSON route has no Pages middleware and supplies no per-user data.
func Mount(r pockets.RouteRegistrar, service Service) {
	r.Handle("GET", "/catalog.json", func(w http.ResponseWriter, r *http.Request) {
		items, err := service.List(r.Context())
		if err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
		_ = web.RespondJSONOK(w, items)
	}, web.NoStore())
}
