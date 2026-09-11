package list_test

import (
	"fmt"
	"net/url"

	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func ExampleParseQuery() {
	query := url.Values{"limit": {"10"}, "order": {"created_at:asc"}, "q": {"guide"}}
	fields := map[string]list.OrderField{"created_at": {Column: "created_at"}}
	defaultOrder := list.NewOrder("created_at", list.DESC)
	req, err := list.ParseQuery(query, list.QueryOptions{})
	if err == nil {
		req.Order, err = list.ParseOrder(fields, query.Get(list.QueryKeyOrder), defaultOrder)
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(req.Limit, req.Order.Field, req.Order.Direction, req.Search, req.ResolvedStrategy())
	// Output: 10 created_at ASC guide cursor
}
