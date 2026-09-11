package roles

import "github.com/gopernicus/gopernicus/sdk/pkg/list"

// OrderFields contains the natural ordering key for assignment listings.
// role_key joins validated subject type/id, role, and resource type/id with
// U+0001 and sorts in byte order; empty resource fields identify a global grant.
var OrderFields = map[string]list.OrderField{
	"role_key": {Column: "role_key"},
}

// DefaultOrder applies ascending byte order to the assignment identity.
var DefaultOrder = list.NewOrder("role_key", list.ASC)

// EffectiveOrderFields is the allow-list for ListEffectiveByResource. The
// effective set is de-duplicated by (subject, role), and ordered by its derived
// grant_key — the (subject_type, subject_id, role) tuple.
var EffectiveOrderFields = map[string]list.OrderField{
	"grant_key": {Column: "grant_key"},
}

// DefaultEffectiveOrder is the sort applied to ListEffectiveByResource when a
// Request carries a zero-value Order: grant_key ASC. Its Field is the
// derived key column so a backend matches it against EffectiveOrderFields.
var DefaultEffectiveOrder = list.NewOrder("grant_key", list.ASC)
