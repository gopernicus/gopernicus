package goth

import (
	"net/url"

	"github.com/gopernicus/gopernicus/features/cms"
)

// oldestOrder is the non-default sort the entries-list sort toggle switches to:
// created_at ascending (oldest first). The empty/default order is newest first
// (content.DefaultOrder = created_at DESC). Only created_at is on the content sort
// allow-list, so this is the one honest server-owned sort the admin list exposes.
const oldestOrder = "created_at:asc"

// olderHref builds the "Older →" pagination target: the list base with the next
// cursor plus the active order/status filter.
func olderHref(p cms.Pager) string { return listHref(p.BaseHref, p.NextCursor, p.Order, p.Status) }

// newerHref builds the "← Newer" pagination target: an empty PreviousCursor means
// the previous page is the first page (the bare base + filters); otherwise the
// previous cursor. The active order/status ride along either way.
func newerHref(p cms.Pager) string { return listHref(p.BaseHref, p.PreviousCursor, p.Order, p.Status) }

// filterHref is the entries-list filter/sort form target: the base carrying the
// active order (a filter change resubmits from page one, so no cursor). The status
// itself rides as the form's own select value.
func filterHref(p cms.Pager) string { return listHref(p.BaseHref, "", p.Order, "") }

// sortToggleHref is the target of the "Newest/Oldest first" sort toggle: it flips
// the order between default (newest) and oldest, preserving the active status
// filter, and resets to page one.
func sortToggleHref(p cms.Pager) string {
	next := oldestOrder
	if p.Order == oldestOrder {
		next = "" // back to the default newest-first order
	}
	return listHref(p.BaseHref, "", next, p.Status)
}

// sortToggleLabel is the sort toggle's visible label: it names the sort the toggle
// switches TO, so the control reads as an action.
func sortToggleLabel(p cms.Pager) string {
	if p.Order == oldestOrder {
		return "Newest first"
	}
	return "Oldest first"
}

// statusFilterLabel is the human label for a status filter option value.
func statusFilterLabel(value string) string {
	switch value {
	case "draft":
		return "Draft"
	case "published":
		return "Published"
	default:
		return "All statuses"
	}
}

// listHref composes an entries-list URL from its base plus optional cursor, order,
// and status query params (deterministically ordered by url.Values.Encode). A bare
// base is returned when none is set.
func listHref(base, cursor, order, status string) string {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if order != "" {
		q.Set("order", order)
	}
	if status != "" {
		q.Set("status", status)
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}
