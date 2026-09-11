package documents

import "embed"

// FS is the host-owned document schema for the listing example. Apply it before
// boot with the same pgxdb schema option used by the document store.
//
//go:embed primary/*.sql
var FS embed.FS

const Dir = "primary"
