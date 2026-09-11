package list

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func encTest(s string) (string, error) { return "enc_" + s, nil }

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
		{"full_window_has_prev_and_cursor", []string{"a", "b", "c", "d"}, 3, true, "enc_a"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Page[string]{}
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

func TestParseRequest_Defaults(t *testing.T) {
	req, err := ParseRequest(Params{Limits: Limits{Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", req.Limit, DefaultLimit)
	}
	if req.Cursor != "" {
		t.Errorf("cursor = %q, want empty", req.Cursor)
	}
	if req.Offset != 0 {
		t.Errorf("offset = %d, want 0", req.Offset)
	}
	if req.WithCount {
		t.Errorf("withCount = true, want false")
	}
	if req.Strategy != StrategyCursor {
		t.Errorf("strategy = %q, want %q (default)", req.Strategy, StrategyCursor)
	}
}

func TestParseRequest_CustomLimitAndCursor(t *testing.T) {
	req, err := ParseRequest(Params{Limit: "10", Cursor: "abc123", Limits: Limits{Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 10 || req.Cursor != "abc123" {
		t.Errorf("got %+v, want {10 abc123}", req)
	}
	if req.Strategy != StrategyCursor {
		t.Errorf("strategy = %q, want %q (cursor param present)", req.Strategy, StrategyCursor)
	}
}

// TestParseRequest_Strategy covers transport-edge strategy resolution: an
// offset param (even "0") selects offset mode, a cursor param selects cursor
// mode, and neither falls back to DefaultStrategy ("" → cursor).
func TestParseRequest_Strategy(t *testing.T) {
	tests := []struct {
		name       string
		params     Params
		wantMode   Strategy
		wantOffset int
	}{
		{"offset_zero_is_offset_mode", Params{Limit: "10", Offset: "0", Limits: Limits{Max: 100}}, StrategyOffset, 0},
		{"offset_positive", Params{Limit: "10", Offset: "5", Limits: Limits{Max: 100}}, StrategyOffset, 5},
		{"cursor_param", Params{Limit: "10", Cursor: "abc", Limits: Limits{Max: 100}}, StrategyCursor, 0},
		{"neither_defaults_cursor", Params{Limit: "10", Limits: Limits{Max: 100}}, StrategyCursor, 0},
		{"neither_default_offset", Params{Limit: "10", Limits: Limits{Max: 100}, DefaultStrategy: StrategyOffset}, StrategyOffset, 0},
		{"offset_param_overrides_default", Params{Limit: "10", Offset: "3", Limits: Limits{Max: 100}, DefaultStrategy: StrategyCursor}, StrategyOffset, 3},
		{"cursor_param_overrides_default", Params{Limit: "10", Cursor: "abc", Limits: Limits{Max: 100}, DefaultStrategy: StrategyOffset}, StrategyCursor, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseRequest(tt.params)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if req.Strategy != tt.wantMode {
				t.Errorf("strategy = %q, want %q", req.Strategy, tt.wantMode)
			}
			if req.Offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", req.Offset, tt.wantOffset)
			}
			if err := req.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil for a parsed request", err)
			}
		})
	}
}

// TestParseRequest_NeverClamps proves the strict transport-edge contract:
// out-of-range input is rejected, never silently clamped.
func TestParseRequest_NeverClamps(t *testing.T) {
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
		_, err := ParseRequest(Params{Limit: tt.input, Limits: Limits{Max: 100}})
		if err == nil {
			t.Errorf("ParseRequest(%q) err = nil, want containing %q", tt.input, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("ParseRequest(%q) err = %q, want containing %q", tt.input, err.Error(), tt.wantErr)
		}
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ParseRequest(%q) err = %v, want wrapping sdk.ErrInvalidInput", tt.input, err)
		}
	}
}

// TestParseRequest_RejectionsPreserveStrconvCause proves the three strconv
// rejections keep their cause in the chain alongside the sentinel: a host can
// still reach the *strconv.NumError while web.ErrFromDomain classifies the 400.
func TestParseRequest_RejectionsPreserveStrconvCause(t *testing.T) {
	tests := []struct {
		name   string
		params Params
	}{
		{"limit", Params{Limit: "zero"}},
		{"offset", Params{Offset: "here"}},
		{"count", Params{Count: "yes-please"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRequest(tt.params)
			if err == nil {
				t.Fatal("err = nil, want error")
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
			}
			if !errors.As(err, new(*strconv.NumError)) {
				t.Errorf("err = %v, want wrapping *strconv.NumError", err)
			}
		})
	}
}

func TestParseRequest_MaxBoundary(t *testing.T) {
	req, err := ParseRequest(Params{Limit: "100", Limits: Limits{Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 100 {
		t.Errorf("limit = %d, want 100", req.Limit)
	}
}

func TestParseRequest_CustomMaxLimit(t *testing.T) {
	req, err := ParseRequest(Params{Limit: "200", Limits: Limits{Max: 500}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 200 {
		t.Errorf("limit = %d, want 200", req.Limit)
	}

	_, err = ParseRequest(Params{Limit: "501", Limits: Limits{Max: 500}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want containing %q and %q", err.Error(), "too large", "500")
	}
}

// TestParseRequest_CustomDefault proves the resource's effective default is
// applied when the limit param is empty, and a declared default above the
// effective max clamps to the max.
func TestParseRequest_CustomDefault(t *testing.T) {
	req, err := ParseRequest(Params{Limits: Limits{Default: 50, Max: 500}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 50 {
		t.Errorf("limit = %d, want 50 (custom default on empty)", req.Limit)
	}

	req, err = ParseRequest(Params{Limits: Limits{Default: 500, Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 100 {
		t.Errorf("limit = %d, want 100 (default clamped to max)", req.Limit)
	}
}

func TestParseRequest_ZeroMaxFallsBackToMaxLimit(t *testing.T) {
	_, err := ParseRequest(Params{Limit: "101", Limits: Limits{}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("err = %q, want containing %q", err.Error(), "100")
	}
}

// TestParseRequest_Offset covers offset-mode parsing edges: empty is 0,
// a positive value selects offset mode, and non-numeric or negative values are
// rejected.
func TestParseRequest_Offset(t *testing.T) {
	tests := []struct {
		name       string
		offsetStr  string
		wantOffset int
		wantErr    string
	}{
		{"empty_is_zero", "", 0, ""},
		{"positive", "40", 40, ""},
		{"garbage", "abc", 0, "page offset conversion"},
		{"negative", "-1", 0, "too small"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseRequest(Params{Limit: "10", Offset: tt.offsetStr, Limits: Limits{Max: 100}})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want containing %q", err.Error(), tt.wantErr)
				}
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if req.Offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", req.Offset, tt.wantOffset)
			}
		})
	}
}

// TestParseRequest_Count covers count parsing via strconv.ParseBool,
// including the empty=false default and rejection of non-bool values.
func TestParseRequest_Count(t *testing.T) {
	tests := []struct {
		name      string
		countStr  string
		wantCount bool
		wantErr   string
	}{
		{"empty_is_false", "", false, ""},
		{"true", "true", true, ""},
		{"one", "1", true, ""},
		{"false", "false", false, ""},
		{"zero", "0", false, ""},
		{"garbage", "yes-please", false, "page count conversion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseRequest(Params{Limit: "10", Count: tt.countStr, Limits: Limits{Max: 100}})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want containing %q", err.Error(), tt.wantErr)
				}
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if req.WithCount != tt.wantCount {
				t.Errorf("withCount = %v, want %v", req.WithCount, tt.wantCount)
			}
		})
	}
}

// TestParseRequest_CursorAndOffsetRejected proves a cursor param and an
// offset param together are rejected at the transport edge — including the
// former loophole where offset "0" plus a cursor was silently accepted.
func TestParseRequest_CursorAndOffsetRejected(t *testing.T) {
	_, err := ParseRequest(Params{Limit: "10", Cursor: "abc123", Offset: "40", Limits: Limits{Max: 100}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %q, want containing %q", err.Error(), "mutually exclusive")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
	}

	// An offset param of "0" alongside a cursor is now also rejected: an offset
	// param present at all means offset strategy, which excludes a cursor.
	_, err = ParseRequest(Params{Limit: "10", Cursor: "abc123", Offset: "0", Limits: Limits{Max: 100}})
	if err == nil {
		t.Fatal("err = nil, want error for cursor + offset=0")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %q, want containing %q", err.Error(), "mutually exclusive")
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
	}
}

// TestNilItemsNormalized pins D4: every SDK page constructor and bridge turns a
// nil item slice into an empty one, so an empty page can never marshal
// "items":null.
func TestNilItemsNormalized(t *testing.T) {
	if got := Items[string](nil).Items; got == nil || len(got) != 0 {
		t.Errorf("Items(nil).Items = %#v, want empty non-nil", got)
	}

	if got := MapItems[int, string](nil, strconv.Itoa).Items; got == nil || len(got) != 0 {
		t.Errorf("MapItems(nil).Items = %#v, want empty non-nil", got)
	}

	trimmed, err := TrimPage[string](nil, 10, encTest)
	if err != nil {
		t.Fatalf("TrimPage: %v", err)
	}
	if trimmed.Items == nil || len(trimmed.Items) != 0 {
		t.Errorf("TrimPage(nil).Items = %#v, want empty non-nil", trimmed.Items)
	}

	if got := MapPage(Page[int]{}, strconv.Itoa).Items; got == nil || len(got) != 0 {
		t.Errorf("MapPage(zero).Items = %#v, want empty non-nil", got)
	}

	mapped, err := MapPageErr(Page[int]{}, func(n int) (string, error) { return strconv.Itoa(n), nil })
	if err != nil {
		t.Fatalf("MapPageErr: %v", err)
	}
	if mapped.Items == nil || len(mapped.Items) != 0 {
		t.Errorf("MapPageErr(zero).Items = %#v, want empty non-nil", mapped.Items)
	}
}

// TestPageWireShape proves the bounded page on the wire: only "items", and an
// empty one is [] — no has_more, no next_cursor, no total.
func TestPageWireShape(t *testing.T) {
	trimmed, err := TrimPage[string](nil, 10, encTest)
	if err != nil {
		t.Fatalf("TrimPage: %v", err)
	}

	cases := []struct {
		name string
		page any
		want string
	}{
		{"items_nil", Items[string](nil), `{"items":[]}`},
		{"map_items", MapItems([]int{1, 2}, strconv.Itoa), `{"items":["1","2"]}`},
		{"trim_page_nil", trimmed, `{"items":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.page)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if got := string(b); got != tc.want {
				t.Fatalf("json = %s, want %s", got, tc.want)
			}
			for _, key := range []string{"has_more", "next_cursor", "previous_cursor", "has_prev", "total"} {
				if strings.Contains(string(b), key) {
					t.Errorf("json = %s, must not carry %q", b, key)
				}
			}
		})
	}
}
