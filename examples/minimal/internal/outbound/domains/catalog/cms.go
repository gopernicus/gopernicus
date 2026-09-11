package catalog

import (
	"context"

	"github.com/gopernicus/gopernicus/examples/minimal/internal/logic/domains/catalog"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// CMS adapts the CMS repository to the host's public catalog port.
type CMS struct{ entries content.EntryRepository }

func NewCMS(entries content.EntryRepository) *CMS { return &CMS{entries: entries} }

var _ catalog.Source = (*CMS)(nil)

func (s *CMS) ListPublished(ctx context.Context) ([]catalog.Item, error) {
	page, err := s.entries.List(ctx, content.EntryQuery{Type: "product", Status: content.StatusPublished, Request: list.Request{Limit: 100}})
	if err != nil {
		return nil, err
	}
	items := make([]catalog.Item, len(page.Items))
	for i, entry := range page.Items {
		items[i] = catalog.Item{Slug: entry.Slug, Title: entry.Title, Excerpt: entry.Excerpt}
	}
	return items, nil
}
