package job

import "github.com/gopernicus/gopernicus/sdk/crud"

// OrderFields is the allow-list of sortable columns for List: only these vetted
// column names may reach a store's ORDER BY. The map key is the API-facing field
// name (it coincides with the column). created_at is the indexed spine column the
// queue pages by; the job_id tiebreak is applied by the store, not listed here.
// priority is deliberately excluded: no general index sorts the queue by priority
// (the only priority index is the partial pending-claim hot path
// idx_job_queue_claim, which leads with scheduled_for), so it is not offered as a
// List order per the "add priority only if indexed" rule.
var OrderFields = map[string]crud.OrderField{
	"created_at": {Column: "created_at"},
}

// DefaultOrder is the sort applied when a ListRequest carries a zero-value Order:
// created_at DESC (with the store's job_id DESC tiebreak). Its Field is the
// resolved column, so a backend matches it against OrderFields by column.
var DefaultOrder = crud.NewOrder("created_at", crud.DESC)
