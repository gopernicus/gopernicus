package list

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// Order-value type tags carried in cursor tokens. JSON alone degrades Go types
// on the round trip — int64 becomes float64 (losing precision past 2^53) and
// time.Time becomes a string — so EncodeCursor records the type and
// DecodeCursor restores it.
const (
	cursorTypeInt    = "int"
	cursorTypeUint   = "uint"
	cursorTypeFloat  = "float"
	cursorTypeString = "string"
	cursorTypeBool   = "bool"
	cursorTypeTime   = "time"
)

// Cursor stores the keyset pagination position. It includes the order field
// name so a stale cursor created under a different sort order can be detected
// and ignored.
type Cursor struct {
	OrderField string `json:"order_field"`
	OrderValue any    `json:"order_value"`
	OrderType  string `json:"order_type,omitempty"`
	PK         string `json:"pk"`
}

// EncodeCursor creates a padded base64url cursor. Values may be nil, built-in
// strings/bools/numbers, or time.Time. Convert named scalar types explicitly;
// unsupported values are rejected rather than encoded with a lossy type tag.
func EncodeCursor(orderField string, orderValue any, pk string) (string, error) {
	if orderField == "" || pk == "" {
		return "", fmt.Errorf("cursor field and primary key must not be empty")
	}
	// Drivers store float32 as its exact float64 widening. Marshal that value
	// so decoding cannot move the keyset boundary to a shorter decimal.
	if value, ok := orderValue.(float32); ok {
		orderValue = float64(value)
	}
	typeTag, err := orderValueType(orderValue)
	if err != nil {
		return "", err
	}
	c := Cursor{OrderField: orderField, OrderValue: orderValue, OrderType: typeTag, PK: pk}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.URLEncoding.EncodeToString(data), nil
}

// DecodeCursor requires one complete cursor object with a supported order value.
// Malformed tokens wrap sdk.ErrInvalidInput. Empty tokens and well-formed tokens
// for a different order field return nil (start from the first page).
func DecodeCursor(token string, expectedOrderField string) (*Cursor, error) {
	if token == "" {
		return nil, nil
	}
	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w: %w", err, sdk.ErrInvalidInput)
	}
	var wire struct {
		OrderField string          `json:"order_field"`
		OrderValue json.RawMessage `json:"order_value"`
		OrderType  string          `json:"order_type,omitempty"`
		PK         string          `json:"pk"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("unmarshal cursor: %w: %w", err, sdk.ErrInvalidInput)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("trailing cursor data: %w: %w", err, sdk.ErrInvalidInput)
		}
		return nil, fmt.Errorf("cursor must contain one object: %w", sdk.ErrInvalidInput)
	}
	if wire.OrderField == "" || wire.PK == "" || len(wire.OrderValue) == 0 {
		return nil, fmt.Errorf("cursor requires order_field, order_value and pk: %w", sdk.ErrInvalidInput)
	}
	var value any
	valueDecoder := json.NewDecoder(bytes.NewReader(wire.OrderValue))
	valueDecoder.UseNumber()
	if err := valueDecoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("cursor order value: %w: %w", err, sdk.ErrInvalidInput)
	}
	restored, err := restoreOrderValue(value, wire.OrderType)
	if err != nil {
		return nil, fmt.Errorf("cursor order value: %w: %w", err, sdk.ErrInvalidInput)
	}
	if wire.OrderField != expectedOrderField {
		return nil, nil
	}
	return &Cursor{OrderField: wire.OrderField, OrderValue: restored, OrderType: wire.OrderType, PK: wire.PK}, nil
}

// orderValueType maps an order value to its cursor type tag.
func orderValueType(v any) (string, error) {
	switch v.(type) {
	case int, int8, int16, int32, int64:
		return cursorTypeInt, nil
	case uint, uint8, uint16, uint32, uint64:
		return cursorTypeUint, nil
	case float32, float64:
		return cursorTypeFloat, nil
	case string:
		return cursorTypeString, nil
	case bool:
		return cursorTypeBool, nil
	case time.Time:
		return cursorTypeTime, nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("unsupported cursor order value type %T", v)
}

// restoreOrderValue converts a decoded JSON order value back to the Go type
// recorded by its type tag.
func restoreOrderValue(v any, typeTag string) (any, error) {
	// Null is an intentional position only when no scalar type was declared.
	if v == nil && typeTag == "" {
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
		switch value := v.(type) {
		case string, bool:
			return value, nil
		case json.Number:
			// Old untagged numeric cursors decoded as float64. Preserve that
			// representation only within its exact integer range.
			const maxSafeInteger = 1<<53 - 1
			f, err := value.Float64()
			if err != nil {
				return nil, fmt.Errorf("order value: %w", err)
			}
			if math.Abs(f) > maxSafeInteger {
				return nil, fmt.Errorf("untagged cursor number exceeds the exact integer range")
			}
			return f, nil
		default:
			return nil, fmt.Errorf("unsupported cursor order value type %T", v)
		}
	default:
		return nil, fmt.Errorf("unknown order value type %q", typeTag)
	}
}
