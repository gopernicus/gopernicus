package memory

import (
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authorization/internal/tuplekey"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// unknownOrderField is the error pageMem returns for an order field absent from
// the kind's rim allow-list — the same sdk.ErrInvalidInput-class error the SQL
// stores' resolveOrder produces, so storetest asserts one rejection shape across
// every backend.
func unknownOrderField(field string) error {
	return fmt.Errorf("unknown order field %q: %w", field, sdk.ErrInvalidInput)
}

// orderAllowed reports whether field names a column in the kind's rim allow-list,
// mirroring the connectors' resolveOrder membership check (match by column).
func orderAllowed(field string, fields map[string]list.OrderField) bool {
	for _, of := range fields {
		if of.Column == field {
			return true
		}
	}
	return false
}

// =============================================================================
// Shared in-memory keyset paginator
// =============================================================================

// pageMemByKey paginates items by a single deterministic string key (the
// canonical tuple_key).
// Order field == pk, so the cursor carries the key as both
// the order value and the pk. It rejects an order field absent from fields with
// sdk.ErrInvalidInput, exactly as the connectors' resolveOrder does. Direction
// defaults to ASC (the zero Order).
func pageMemByKey[T any](all []T, req list.Request, fields map[string]list.OrderField, keyField string, keyOf func(T) string) (list.Page[T], error) {
	if err := req.Validate(); err != nil {
		return list.Page[T]{}, err
	}
	if keyField == "tuple_key" {
		if _, err := tuplekey.DecodeCursor(req.Cursor); err != nil {
			return list.Page[T]{}, err
		}
	}
	if req.Order.Field != "" && !orderAllowed(req.Order.Field, fields) {
		return list.Page[T]{}, unknownOrderField(req.Order.Field)
	}
	asc := true
	if req.Order.Field != "" {
		asc = req.Order.Direction != list.DESC
	}

	sort.SliceStable(all, func(i, j int) bool {
		ki, kj := keyOf(all[i]), keyOf(all[j])
		if asc {
			return ki < kj
		}
		return ki > kj
	})

	total := int64(len(all))
	limit := req.NormalizedLimit(list.Limits{})
	encode := func(item T) (string, error) {
		k := keyOf(item)
		return list.EncodeCursor(keyField, k, k)
	}

	if req.ResolvedStrategy() == list.StrategyOffset {
		window := all
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		page, err := list.TrimPage(window, limit, encode)
		if err != nil {
			return list.Page[T]{}, err
		}
		page.NextCursor = ""
		page.HasPrev = req.Offset > 0
		if req.WithCount {
			page.Total = &total
		}
		return page, nil
	}

	cur, err := list.DecodeCursor(req.Cursor, keyField)
	if err != nil {
		return list.Page[T]{}, err
	}

	var curKey string
	forward := all
	if cur != nil {
		var ok bool
		curKey, ok = cur.OrderValue.(string)
		if !ok {
			return list.Page[T]{}, fmt.Errorf("cursor order value must be a string: %w", sdk.ErrInvalidInput)
		}
		forward = forward[:0:0]
		for _, item := range all {
			if afterKeyMem(keyOf(item), curKey, asc) {
				forward = append(forward, item)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	page, err := list.TrimPage(window, limit, encode)
	if err != nil {
		return list.Page[T]{}, err
	}

	if cur != nil {
		var before []T
		for _, item := range all {
			if !afterKeyMem(keyOf(item), curKey, asc) {
				before = append(before, item)
			}
		}
		if len(before) > limit+1 {
			before = before[len(before)-limit-1:]
		}
		if err := list.MarkPrevPage(&page, before, limit, encode); err != nil {
			return list.Page[T]{}, err
		}
	}

	if req.WithCount {
		page.Total = &total
	}
	return page, nil
}

// afterKeyMem reports whether itemKey sorts strictly after curKey in the
// traversal direction (asc → greater keys, desc → lesser keys).
func afterKeyMem(itemKey, curKey string, asc bool) bool {
	if asc {
		return itemKey > curKey
	}
	return itemKey < curKey
}
