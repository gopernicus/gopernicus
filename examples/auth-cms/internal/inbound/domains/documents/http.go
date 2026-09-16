package documents

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

type Query struct {
	TenantID string
	Search   string
	Desc     bool
	Limit    int
	Cursor   string
}

func (q Query) businessQuery() domain.Query {
	return domain.Query{TenantID: q.TenantID, Search: q.Search, Desc: q.Desc, Limit: q.Limit}
}

func (q *Query) normalize() error {
	if len(q.Cursor) > 4096 {
		return fmt.Errorf("documents: invalid cursor: %w", sdk.ErrInvalidInput)
	}
	business := q.businessQuery()
	if err := business.Normalize(); err != nil {
		return err
	}
	q.TenantID, q.Search, q.Desc, q.Limit = business.TenantID, business.Search, business.Desc, business.Limit
	return nil
}

type Page struct {
	Items            []domain.Document `json:"items"`
	HasMore          bool              `json:"has_more"`
	NextCursor       string            `json:"next_cursor,omitempty"`
	ScanLimitReached bool              `json:"scan_limit_reached"`
}

type Lister interface {
	ListVisible(context.Context, sdk.Principal, Query) (Page, error)
}

// Handler sits behind authentication and invokes the inbound listing policy
// before exposing business rows.
func Handler(lister Lister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := sdk.PrincipalFromContext(r.Context())
		if !ok {
			web.RespondJSONDomainError(w, sdk.ErrUnauthorized)
			return
		}
		values := r.URL.Query()
		query := Query{TenantID: r.PathValue("tenant"), Search: values.Get("q"), Cursor: values.Get("cursor")}
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
		page, err := lister.ListVisible(r.Context(), principal, query)
		if err != nil {
			web.RespondJSONDomainError(w, err)
			return
		}
		_ = web.RespondJSONOK(w, page)
	}
}
