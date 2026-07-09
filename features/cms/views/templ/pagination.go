package templ

import (
	"net/url"

	"github.com/gopernicus/gopernicus/features/cms"
)

// olderHref builds the "Older →" link target: the list base with the next cursor
// and, when the active order is non-default, that order.
func olderHref(p cms.Pager) string { return pageHref(p.BaseHref, p.NextCursor, p.Order) }

// newerHref builds the "← Newer" link target: an empty PreviousCursor means the
// previous page is the first page (the bare base); otherwise the previous cursor.
// The active order rides along in either case.
func newerHref(p cms.Pager) string { return pageHref(p.BaseHref, p.PreviousCursor, p.Order) }

// pageHref composes a list URL from its base plus optional cursor and order query
// params (deterministically ordered by url.Values.Encode). A bare base is
// returned when neither is set.
func pageHref(base, cursor, order string) string {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if order != "" {
		q.Set("order", order)
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}
