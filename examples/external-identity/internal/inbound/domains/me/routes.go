// Package me is the HTTP side of the me domain: GET /api/me and a mutating
// POST /api/echo that proves a cookie-borne provider credential pays exactly
// one local status read on an unsafe method.
package me

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gopernicus/gopernicus/examples/external-identity/internal/logic/domains/me"
	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

const maxEchoBytes = 4 << 10

// Service is the slice of the me domain these routes use.
type Service interface {
	Get(ctx context.Context, p sdk.Principal) (me.Profile, error)
}

// Mount registers the routes behind gate, the host's authenticator chain.
func Mount(r pockets.RouteRegistrar, service Service, gate ...web.Middleware) {
	r.Handle(http.MethodGet, "/api/me", func(w http.ResponseWriter, r *http.Request) {
		p, _ := sdk.PrincipalFromContext(r.Context())
		profile, err := service.Get(r.Context(), p)
		if err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
		_ = web.RespondJSONOK(w, profile)
	}, append([]web.Middleware{web.NoStore()}, gate...)...)

	r.Handle(http.MethodPost, "/api/echo", func(w http.ResponseWriter, r *http.Request) {
		p, ok := sdk.PrincipalFromContext(r.Context())
		if !ok {
			web.RespondJSONError(w, web.ErrUnauthorized("authentication required"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEchoBytes))
		if err != nil {
			web.RespondJSONError(w, web.ErrBadRequest("body too large"))
			return
		}
		echo := json.RawMessage("null")
		if len(body) > 0 {
			if !json.Valid(body) {
				web.RespondJSONError(w, web.ErrBadRequest("body must be JSON"))
				return
			}
			echo = body
		}
		_ = web.RespondJSONOK(w, map[string]any{"principal_id": p.ID, "echo": echo})
	}, append([]web.Middleware{web.NoStore()}, gate...)...)
}
