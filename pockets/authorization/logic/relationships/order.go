package relationships

import "github.com/gopernicus/gopernicus/sdk/pkg/list"

// OrderFields contains the natural ordering key for both relationship listings.
// tuple_key encodes the cursor version followed by scope kind, resource type/ID,
// relation and exact subject type/ID/relation, separated by U+0001. It preserves
// tuples.Compare's identity order and is not a stored surrogate identifier.
var OrderFields = map[string]list.OrderField{
	"tuple_key": {Column: "tuple_key"},
}

// DefaultOrder applies ascending byte order to the full tuple identity.
var DefaultOrder = list.NewOrder("tuple_key", list.ASC)
