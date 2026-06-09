package fop

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Order
// ---------------------------------------------------------------------------

func TestNewOrder_ValidDirection(t *testing.T) {
	o := NewOrder("created_at", ASC)
	if o.Field != "created_at" || o.Direction != ASC {
		t.Errorf("NewOrder(ASC) = %+v, want {created_at ASC}", o)
	}

	o = NewOrder("updated_at", DESC)
	if o.Field != "updated_at" || o.Direction != DESC {
		t.Errorf("NewOrder(DESC) = %+v, want {updated_at DESC}", o)
	}
}

func TestNewOrder_InvalidDirectionDefaultsToASC(t *testing.T) {
	o := NewOrder("created_at", "INVALID")
	if o.Direction != ASC {
		t.Errorf("NewOrder(invalid) direction = %q, want %q", o.Direction, ASC)
	}
}

func TestParseOrder_EmptyReturnsDefault(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}}
	def := NewOrder("created_at", DESC)

	o, err := ParseOrder(mappings, "", def)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o != def {
		t.Errorf("ParseOrder(empty) = %+v, want %+v", o, def)
	}
}

func TestParseOrder_FieldOnly(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}, "name": {Column: "name"}}

	o, err := ParseOrder(mappings, "created_at", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" || o.Direction != ASC {
		t.Errorf("got %+v, want {created_at ASC}", o)
	}
}

func TestParseOrder_FieldWithDirection(t *testing.T) {
	mappings := map[string]OrderField{"created_at": {Column: "created_at"}}

	o, err := ParseOrder(mappings, "created_at:DESC", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" || o.Direction != DESC {
		t.Errorf("got %+v, want {created_at DESC}", o)
	}
}

func TestParseOrder_CaseInsensitiveDirection(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	def := NewOrder("id", ASC)

	for _, input := range []string{"name:desc", "name:Desc", "name:DESC"} {
		o, err := ParseOrder(mappings, input, def)
		if err != nil {
			t.Fatalf("ParseOrder(%q) err = %v", input, err)
		}
		if o.Direction != DESC {
			t.Errorf("ParseOrder(%q) direction = %q, want DESC", input, o.Direction)
		}
	}
}

func TestParseOrder_UnknownField(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	_, err := ParseOrder(mappings, "unknown_field", NewOrder("id", ASC))
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown order field") {
		t.Errorf("err = %q, want containing %q", err.Error(), "unknown order field")
	}
}

func TestParseOrder_UnknownDirection(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}
	_, err := ParseOrder(mappings, "name:SIDEWAYS", NewOrder("id", ASC))
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown direction") {
		t.Errorf("err = %q, want containing %q", err.Error(), "unknown direction")
	}
}

func TestParseOrder_TrimsWhitespace(t *testing.T) {
	mappings := map[string]OrderField{"name": {Column: "name"}}

	o, err := ParseOrder(mappings, " name : desc ", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "name" || o.Direction != DESC {
		t.Errorf("got %+v, want {name DESC}", o)
	}
}

func TestParseOrder_FieldMapping(t *testing.T) {
	mappings := map[string]OrderField{"created": {Column: "created_at"}}

	o, err := ParseOrder(mappings, "created:asc", NewOrder("id", ASC))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if o.Field != "created_at" {
		t.Errorf("field = %q, want %q", o.Field, "created_at")
	}
}

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

func TestParsePageStringCursor_Defaults(t *testing.T) {
	page, err := ParsePageStringCursor("", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 25 {
		t.Errorf("limit = %d, want 25", page.Limit)
	}
	if page.Cursor != "" {
		t.Errorf("cursor = %q, want empty", page.Cursor)
	}
}

func TestParsePageStringCursor_CustomLimit(t *testing.T) {
	page, err := ParsePageStringCursor("50", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 50 {
		t.Errorf("limit = %d, want 50", page.Limit)
	}
}

func TestParsePageStringCursor_WithCursor(t *testing.T) {
	page, err := ParsePageStringCursor("10", "abc123", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 10 || page.Cursor != "abc123" {
		t.Errorf("got %+v, want {10 abc123}", page)
	}
}

func TestParsePageStringCursor_BoundaryErrors(t *testing.T) {
	tests := []struct {
		input   string
		wantErr string
	}{
		{"0", "too small"},
		{"-5", "too small"},
		{"101", "too large"},
		{"150", "too large"},
		{"not-a-number", "page limit conversion"},
	}
	for _, tt := range tests {
		_, err := ParsePageStringCursor(tt.input, "", 100)
		if err == nil {
			t.Errorf("ParsePageStringCursor(%q) err = nil, want containing %q", tt.input, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("ParsePageStringCursor(%q) err = %q, want containing %q", tt.input, err.Error(), tt.wantErr)
		}
	}
}

func TestParsePageStringCursor_MaxLimit(t *testing.T) {
	page, err := ParsePageStringCursor("100", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 100 {
		t.Errorf("limit = %d, want 100", page.Limit)
	}
}

func TestParsePageStringCursor_MinLimit(t *testing.T) {
	page, err := ParsePageStringCursor("1", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 1 {
		t.Errorf("limit = %d, want 1", page.Limit)
	}
}

func TestParsePageStringCursor_CustomMaxLimit(t *testing.T) {
	// Within custom max.
	page, err := ParsePageStringCursor("200", "", 500)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if page.Limit != 200 {
		t.Errorf("limit = %d, want 200", page.Limit)
	}

	// Exceeds custom max.
	_, err = ParsePageStringCursor("501", "", 500)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %q, want containing %q", err.Error(), "too large")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want containing %q", err.Error(), "500")
	}
}

func TestParsePageStringCursor_ZeroMaxFallsBackToDefault(t *testing.T) {
	// maxLimit=0 should fall back to DefaultMaxLimit (100).
	_, err := ParsePageStringCursor("101", "", 0)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("err = %q, want containing %q", err.Error(), "100")
	}
}

// ---------------------------------------------------------------------------
// TrimPage / MarkPrevPage
// ---------------------------------------------------------------------------

func encTest(s string) (string, error) { return "enc_" + s, nil }

func TestTrimPage(t *testing.T) {
	tests := []struct {
		name           string
		records        []string
		limit          int
		wantRecords    []string
		wantNextCursor string
	}{
		{"overfull_trims_and_encodes", []string{"a", "b", "c", "d"}, 3, []string{"a", "b", "c"}, "enc_c"},
		{"exact_fit_no_cursor", []string{"a", "b", "c"}, 3, []string{"a", "b", "c"}, ""},
		{"underfull_no_cursor", []string{"a"}, 3, []string{"a"}, ""},
		{"empty", nil, 3, nil, ""},
		{"zero_limit_passes_through", []string{"a", "b"}, 0, []string{"a", "b"}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, page, err := TrimPage(tc.records, tc.limit, encTest)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(got) != len(tc.wantRecords) {
				t.Fatalf("got %d records, want %d", len(got), len(tc.wantRecords))
			}
			for i := range got {
				if got[i] != tc.wantRecords[i] {
					t.Errorf("record[%d] = %q, want %q", i, got[i], tc.wantRecords[i])
				}
			}
			if page.NextCursor != tc.wantNextCursor {
				t.Errorf("NextCursor = %q, want %q", page.NextCursor, tc.wantNextCursor)
			}
			if page.Limit != tc.limit {
				t.Errorf("Limit = %d, want %d", page.Limit, tc.limit)
			}
			if page.PageTotal != len(tc.wantRecords) {
				t.Errorf("PageTotal = %d, want %d", page.PageTotal, len(tc.wantRecords))
			}
		})
	}
}

func TestMarkPrevPage(t *testing.T) {
	tests := []struct {
		name           string
		prevRecords    []string
		limit          int
		wantHasPrev    bool
		wantPrevCursor string
	}{
		{"empty_probe_no_prev", nil, 3, false, ""},
		{"partial_window_has_prev_no_cursor", []string{"a"}, 3, true, ""},
		{"full_window_has_prev_and_cursor", []string{"a", "b", "c"}, 3, true, "enc_a"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p Pagination
			if err := MarkPrevPage(&p, tc.prevRecords, tc.limit, encTest); err != nil {
				t.Fatalf("err = %v", err)
			}
			if p.HasPrev != tc.wantHasPrev {
				t.Errorf("HasPrev = %v, want %v", p.HasPrev, tc.wantHasPrev)
			}
			if p.PreviousCursor != tc.wantPrevCursor {
				t.Errorf("PreviousCursor = %q, want %q", p.PreviousCursor, tc.wantPrevCursor)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cursor
// ---------------------------------------------------------------------------

func TestCursor_Roundtrip(t *testing.T) {
	token, err := EncodeCursor("created_at", "2024-01-15T10:30:00Z", "user_abc123")
	if err != nil {
		t.Fatalf("EncodeCursor err = %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}

	cursor, err := DecodeCursor(token, "created_at")
	if err != nil {
		t.Fatalf("DecodeCursor err = %v", err)
	}
	if cursor == nil {
		t.Fatal("cursor = nil")
	}
	if cursor.OrderField != "created_at" {
		t.Errorf("OrderField = %q, want %q", cursor.OrderField, "created_at")
	}
	if cursor.PK != "user_abc123" {
		t.Errorf("PK = %q, want %q", cursor.PK, "user_abc123")
	}
}

func TestCursor_NumericOrderValue(t *testing.T) {
	token, err := EncodeCursor("priority", 42, "task_xyz")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "priority")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cursor == nil {
		t.Fatal("cursor = nil")
	}
	// The type tag restores integer order values as int64.
	val, ok := cursor.OrderValue.(int64)
	if !ok {
		t.Fatalf("OrderValue type = %T, want int64", cursor.OrderValue)
	}
	if val != 42 {
		t.Errorf("OrderValue = %v, want 42", val)
	}
}

func TestCursor_Int64Precision(t *testing.T) {
	// 2^53 + 1 is not representable as float64 — a plain JSON round trip
	// would silently corrupt it.
	big := int64(9007199254740993)
	token, err := EncodeCursor("id", big, "row_1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "id")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	val, ok := cursor.OrderValue.(int64)
	if !ok {
		t.Fatalf("OrderValue type = %T, want int64", cursor.OrderValue)
	}
	if val != big {
		t.Errorf("OrderValue = %d, want %d", val, big)
	}
}

func TestCursor_TimeOrderValue(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC)
	token, err := EncodeCursor("created_at", ts, "user_1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "created_at")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	val, ok := cursor.OrderValue.(time.Time)
	if !ok {
		t.Fatalf("OrderValue type = %T, want time.Time", cursor.OrderValue)
	}
	if !val.Equal(ts) {
		t.Errorf("OrderValue = %v, want %v", val, ts)
	}
}

func TestCursor_NilPointerOrderValue(t *testing.T) {
	var nilTime *time.Time
	token, err := EncodeCursor("last_login_at", nilTime, "user_1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "last_login_at")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cursor.OrderValue != nil {
		t.Errorf("OrderValue = %v (%T), want nil", cursor.OrderValue, cursor.OrderValue)
	}
}

func TestCursor_LegacyUntypedToken(t *testing.T) {
	// Tokens minted before the type tag have no order_type field; numbers
	// must keep the historical float64 decode behavior.
	raw := `{"order_field":"priority","order_value":42,"pk":"task_1"}`
	token := base64.URLEncoding.EncodeToString([]byte(raw))

	cursor, err := DecodeCursor(token, "priority")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	val, ok := cursor.OrderValue.(float64)
	if !ok {
		t.Fatalf("OrderValue type = %T, want float64", cursor.OrderValue)
	}
	if val != 42 {
		t.Errorf("OrderValue = %v, want 42", val)
	}
}

func TestDecodeCursor_EmptyToken(t *testing.T) {
	cursor, err := DecodeCursor("", "anything")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cursor != nil {
		t.Errorf("cursor = %+v, want nil", cursor)
	}
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!!!", "field")
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "decode cursor") {
		t.Errorf("err = %q, want containing %q", err.Error(), "decode cursor")
	}
}

func TestDecodeCursor_InvalidJSON(t *testing.T) {
	_, err := DecodeCursor("bm90LWpzb24AAAA=", "field")
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "unmarshal cursor") {
		t.Errorf("err = %q, want containing %q", err.Error(), "unmarshal cursor")
	}
}

func TestDecodeCursor_MismatchedOrderField(t *testing.T) {
	token, err := EncodeCursor("email", "alice@example.com", "user_1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "created_at")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if cursor != nil {
		t.Errorf("cursor = %+v, want nil (stale cursor)", cursor)
	}
}

func TestDecodeCursor_MatchingOrderField(t *testing.T) {
	token, err := EncodeCursor("email", "alice@example.com", "user_1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	cursor, err := DecodeCursor(token, "email")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cursor == nil {
		t.Fatal("cursor = nil, want non-nil")
	}
	if cursor.PK != "user_1" {
		t.Errorf("PK = %q, want %q", cursor.PK, "user_1")
	}
}
