package list

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestDecodeCursorRejectsMalformedStructure(t *testing.T) {
	for name, payload := range map[string]string{
		"null object":            `null`,
		"empty object":           `{}`,
		"missing value":          `{"order_field":"id","pk":"e1"}`,
		"missing pk":             `{"order_field":"id","order_value":"e1"}`,
		"invalid null tag":       `{"order_field":"id","order_value":null,"order_type":"unknown","pk":"e1"}`,
		"object value":           `{"order_field":"id","order_value":{},"pk":"e1"}`,
		"array value":            `{"order_field":"id","order_value":[],"pk":"e1"}`,
		"extra object":           `{"order_field":"id","order_value":"e1","pk":"e1"} {}`,
		"trailing junk":          `{"order_field":"id","order_value":"e1","pk":"e1"} junk`,
		"malformed stale":        `{"order_field":"old","pk":"e1"}`,
		"inexact legacy integer": `{"order_field":"id","order_value":9007199254740993,"pk":"e1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeCursor(base64.URLEncoding.EncodeToString([]byte(payload)), "id")
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("error = %v, want invalid input", err)
			}
		})
	}
	if _, err := DecodeCursor("!!!", "id"); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("bad base64 error = %v", err)
	}
}

func TestCursorValueSupport(t *testing.T) {
	type sequence int64
	for _, value := range []any{sequence(9007199254740993), map[string]int{"n": 1}, []int{1}, new(int)} {
		if token, err := EncodeCursor("id", value, "e1"); err == nil {
			t.Errorf("unsupported %T encoded as %q", value, token)
		}
	}
	for _, value := range []any{nil, int64(9007199254740993), uint64(1<<64 - 1), float64(1.25), true, "e1"} {
		token, err := EncodeCursor("id", value, "e1")
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeCursor(token, "id")
		if err != nil {
			t.Fatal(err)
		}
		if got.OrderValue != value {
			t.Errorf("round trip %T(%v) = %T(%v)", value, value, got.OrderValue, got.OrderValue)
		}
	}
	// Older valid cursors without a type tag keep the existing scalar decoding.
	token := base64.URLEncoding.EncodeToString([]byte(`{"order_field":"id","order_value":42,"pk":"e1"}`))
	got, err := DecodeCursor(token, "id")
	if err != nil || got.OrderValue != float64(42) {
		t.Fatalf("legacy scalar: %+v, %v", got, err)
	}
}

func TestCursorFloat32PreservesWidenedValue(t *testing.T) {
	original := float32(1.1)
	token, err := EncodeCursor("n", original, "a")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := DecodeCursor(token, "n")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cursor.OrderValue, float64(original); got != want {
		t.Fatalf("restored %v, want exact widened value %.17g", got, want)
	}
}
