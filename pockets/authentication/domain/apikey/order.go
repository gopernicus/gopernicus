package apikey

import "github.com/gopernicus/gopernicus/sdk/foundation/crud"

// OrderFields is the allow-list of sortable columns for ListByServiceAccount:
// only these vetted column names may reach a store's ORDER BY. The map key is
// the API-facing field name (it coincides with the column). created_at is the
// indexed spine column every auth port pages by; the id tiebreak is applied by
// the store, not listed here.
var OrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
}

// DefaultOrder is the sort applied when a ListRequest carries a zero-value Order:
// created_at DESC (with the store's id DESC tiebreak). Its Field is the resolved
// column, so a backend matches it against OrderFields by column.
var DefaultOrder = crud.NewOrder("created_at", crud.DESC)

// SearchFields is the allow-list of SEARCHABLE columns for ListByServiceAccount
// (crud-search-upstream T4) — the twin of OrderFields, declared the same way for
// the same reason: only vetted column names may reach a store's WHERE clause.
//
// v1 declares the human-chosen `name` and nothing else. The key hash and prefix
// are credential material and are deliberately NOT searchable: a searchable
// prefix would let a caller probe for a key by fragment.
var SearchFields = []crud.SearchField{
	{Column: "name"},
}
