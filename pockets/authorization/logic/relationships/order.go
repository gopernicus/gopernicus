package relationships

import "github.com/gopernicus/gopernicus/sdk/pkg/list"

// OrderFields contains the natural ordering key for both relationship listings.
// tuple_key joins the validated full tuple in resource/type, resource/id,
// relation, subject/type, subject/id, subject/relation order with U+0001.
// It is a derived sort expression, not a stored or public surrogate identifier.
var OrderFields = map[string]list.OrderField{
	"tuple_key": {Column: "tuple_key"},
}

// DefaultOrder applies ascending byte order to the full tuple identity.
var DefaultOrder = list.NewOrder("tuple_key", list.ASC)
