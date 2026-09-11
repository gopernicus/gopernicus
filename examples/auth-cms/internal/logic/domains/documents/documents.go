package documents

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

type Document struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
}

// Validate bounds persisted sort values as well as ordinary document input.
// Even JSON escaping the maximum name/ID fits the 4096-byte encrypted cursor.
func (d Document) Validate() error {
	if !validText(d.ID, 128) || !validText(d.TenantID, 128) || !validText(d.Name, 512) ||
		strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.TenantID) == "" || strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("documents: invalid ID, tenant or name: %w", sdk.ErrInvalidInput)
	}
	return nil
}

func validText(value string, maxBytes int) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type Query struct {
	TenantID string
	Search   string
	Desc     bool
	Limit    int
	Cursor   string
}

type Page struct {
	Items            []Document `json:"items"`
	HasMore          bool       `json:"has_more"`
	NextCursor       string     `json:"next_cursor,omitempty"`
	ScanLimitReached bool       `json:"scan_limit_reached"`
}

// Lister applies permission policy and the business query together. The domain
// does not need to know whether its adapter uses IDs, candidate checks or SQL.
type Lister interface {
	ListVisible(context.Context, sdk.Principal, Query) (Page, error)
}

type Service struct{ lister Lister }

func New(lister Lister) (*Service, error) {
	if lister == nil {
		return nil, fmt.Errorf("documents: lister is required: %w", sdk.ErrInvalidInput)
	}
	return &Service{lister: lister}, nil
}

func (s *Service) ListVisible(ctx context.Context, principal sdk.Principal, query Query) (Page, error) {
	if principal.Type == "" || principal.ID == "" {
		return Page{}, sdk.ErrUnauthorized
	}
	if err := query.Normalize(); err != nil {
		return Page{}, err
	}
	return s.lister.ListVisible(ctx, principal, query)
}

// Normalize defines the host query independently from the permission strategy.
func (q *Query) Normalize() error {
	if !validText(q.TenantID, 128) || !validText(q.Search, 200) || len(q.Cursor) > 4096 {
		return fmt.Errorf("documents: invalid tenant, search or cursor: %w", sdk.ErrInvalidInput)
	}
	q.TenantID = strings.TrimSpace(q.TenantID)
	q.Search = strings.ToLower(strings.TrimSpace(q.Search))
	if q.TenantID == "" {
		return fmt.Errorf("documents: invalid tenant, search or cursor: %w", sdk.ErrInvalidInput)
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > 100 {
		return fmt.Errorf("documents: limit must be between 1 and 100: %w", sdk.ErrInvalidInput)
	}
	return nil
}
