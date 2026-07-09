package content

import "github.com/gopernicus/gopernicus/sdk/crud"

// OrderFields is the allow-list of sortable columns for entry lists: only these
// vetted column names may reach a store's ORDER BY. The map key is the
// API-facing field name (it coincides with the column). created_at is the
// indexed spine column entries page by (idx_entries_type_created); the id
// tiebreak is applied by the store, not listed here. The EAV entry_fields are
// deliberately absent — per-type custom data is never sortable spine (ARCHITECTURE
// Registry-model rule), so only indexed spine columns appear.
var OrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
}

// DefaultOrder is the sort applied when an EntryQuery carries a zero-value Order:
// created_at DESC (with the store's id DESC tiebreak). Its Field is the resolved
// column, so a backend matches it against OrderFields by column.
var DefaultOrder = crud.NewOrder("created_at", crud.DESC)
