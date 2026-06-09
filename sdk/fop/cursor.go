package fop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Order-value type tags carried in cursor tokens. JSON alone degrades Go
// types on the round trip — int64 becomes float64 (losing precision past
// 2^53) and time.Time becomes a string — so EncodeCursor records the type
// and DecodeCursor restores it.
const (
	cursorTypeInt    = "int"
	cursorTypeUint   = "uint"
	cursorTypeFloat  = "float"
	cursorTypeString = "string"
	cursorTypeBool   = "bool"
	cursorTypeTime   = "time"
)

// Cursor stores the pagination position for keyset (cursor-based) pagination.
// It includes the order field name so that stale cursors created under a different
// sort order can be detected and gracefully ignored.
type Cursor struct {
	OrderField string `json:"order_field"`
	OrderValue any    `json:"order_value"`
	// OrderType records the Go type of OrderValue at encode time so DecodeCursor
	// can restore it exactly. Empty on tokens from older encoders or for
	// unrecognized types — the raw decoded JSON value is kept in that case.
	OrderType string `json:"order_type,omitempty"`
	PK        string `json:"pk"`
}

// EncodeCursor creates a base64url-encoded cursor token from the given values.
func EncodeCursor(orderField string, orderValue any, pk string) (string, error) {
	c := Cursor{
		OrderField: orderField,
		OrderValue: orderValue,
		OrderType:  orderValueType(orderValue),
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
// Returns an error only for malformed tokens (invalid base64, JSON, or order value).
func DecodeCursor(token string, expectedOrderField string) (*Cursor, error) {
	if token == "" {
		return nil, nil
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}

	var c Cursor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep numbers textual so int64 restores without float64 precision loss
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("unmarshal cursor: %w", err)
	}

	if c.OrderField != expectedOrderField {
		return nil, nil
	}

	restored, err := restoreOrderValue(c.OrderValue, c.OrderType)
	if err != nil {
		return nil, fmt.Errorf("unmarshal cursor: %w", err)
	}
	c.OrderValue = restored

	return &c, nil
}

// orderValueType maps an order value to its cursor type tag. Pointer variants
// cover nullable columns surfaced by generated Get*OrderValue helpers; nil
// pointers and unrecognized types return "" (no restoration on decode).
func orderValueType(v any) string {
	switch t := v.(type) {
	case int, int8, int16, int32, int64:
		return cursorTypeInt
	case uint, uint8, uint16, uint32, uint64:
		return cursorTypeUint
	case float32, float64:
		return cursorTypeFloat
	case string:
		return cursorTypeString
	case bool:
		return cursorTypeBool
	case time.Time:
		return cursorTypeTime
	case *time.Time:
		if t != nil {
			return cursorTypeTime
		}
	case *string:
		if t != nil {
			return cursorTypeString
		}
	case *int:
		if t != nil {
			return cursorTypeInt
		}
	case *int64:
		if t != nil {
			return cursorTypeInt
		}
	case *float64:
		if t != nil {
			return cursorTypeFloat
		}
	case *bool:
		if t != nil {
			return cursorTypeBool
		}
	}
	return ""
}

// restoreOrderValue converts a decoded JSON order value back to the Go type
// recorded by its type tag. Values without a tag (legacy tokens, nil values,
// unrecognized types) pass through with json.Number normalized to float64,
// matching the pre-tag decode behavior.
func restoreOrderValue(v any, typeTag string) (any, error) {
	if v == nil {
		return nil, nil
	}

	switch typeTag {
	case cursorTypeInt:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a number", v)
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("order value: %w", err)
		}
		return i, nil
	case cursorTypeUint:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a number", v)
		}
		u, err := strconv.ParseUint(n.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("order value: %w", err)
		}
		return u, nil
	case cursorTypeFloat:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a number", v)
		}
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("order value: %w", err)
		}
		return f, nil
	case cursorTypeString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a string", v)
		}
		return s, nil
	case cursorTypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a bool", v)
		}
		return b, nil
	case cursorTypeTime:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("order value %v is not a timestamp", v)
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fmt.Errorf("order value: %w", err)
		}
		return t, nil
	case "":
		if n, ok := v.(json.Number); ok {
			f, err := n.Float64()
			if err != nil {
				return nil, fmt.Errorf("order value: %w", err)
			}
			return f, nil
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unknown order value type %q", typeTag)
	}
}
