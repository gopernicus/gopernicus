package role

import "github.com/gopernicus/gopernicus/sdk/crud"

// OrderFields is the allow-list of sortable columns for the role listings
// (ListBySubject, ListByResource): only these vetted column names may reach a
// store's ORDER BY. The map key is the API-facing field name (it coincides with
// the column). created_at is the indexed spine column both listings page by; the
// 5-tuple tiebreak is applied by the store, not listed here.
var OrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
}

// DefaultOrder is the sort applied when a ListRequest carries a zero-value Order:
// created_at DESC (with the store's 5-tuple DESC tiebreak). Its Field is the
// resolved column, so a backend matches it against OrderFields by column.
var DefaultOrder = crud.NewOrder("created_at", crud.DESC)
