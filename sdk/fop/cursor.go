package fop

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Cursor stores the pagination position for keyset (cursor-based) pagination.
// It includes the order field name so that stale cursors created under a different
// sort order can be detected and gracefully ignored.
type Cursor struct {
	OrderField string `json:"order_field"`
	OrderValue any    `json:"order_value"`
	PK         string `json:"pk"`
}

// EncodeCursor creates a base64url-encoded cursor token from the given values.
func EncodeCursor(orderField string, orderValue any, pk string) (string, error) {
	c := Cursor{
		OrderField: orderField,
		OrderValue: orderValue,
		PK:         pk,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.URLEncoding.EncodeToString(data), nil
}

// DecodeCursor decodes a cursor token and validates it matches the expected order field.
// Returns nil (not an error) when:
//   - token is empty (first page)
//   - cursor's order field does not match expectedOrderField (stale cursor, treat as first page)
//
// Returns an error only for malformed tokens (invalid base64 or JSON).
func DecodeCursor(token string, expectedOrderField string) (*Cursor, error) {
	if token == "" {
		return nil, nil
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}

	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("unmarshal cursor: %w", err)
	}

	if c.OrderField != expectedOrderField {
		return nil, nil
	}

	return &c, nil
}
