package roles

import "github.com/gopernicus/gopernicus/sdk/pkg/list"

// Assignment listings use the same canonical identity order as every tuple view.
var OrderFields = map[string]list.OrderField{"tuple_key": {Column: "tuple_key"}}
var DefaultOrder = list.NewOrder("tuple_key", list.ASC)
