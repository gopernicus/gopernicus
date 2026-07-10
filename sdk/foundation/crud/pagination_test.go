package crud

import (
	"strings"
	"testing"
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
		{"full_window_has_prev_and_cursor", []string{"a", "b", "c"}, 3, true, "enc_a"},
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

func TestParseListRequest_Defaults(t *testing.T) {
	req, err := ParseListRequest(ListParams{Limits: Limits{Max: 100}})
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

func TestParseListRequest_CustomLimitAndCursor(t *testing.T) {
	req, err := ParseListRequest(ListParams{Limit: "10", Cursor: "abc123", Limits: Limits{Max: 100}})
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

// TestParseListRequest_Strategy covers transport-edge strategy resolution: an
// offset param (even "0") selects offset mode, a cursor param selects cursor
// mode, and neither falls back to DefaultStrategy ("" → cursor).
func TestParseListRequest_Strategy(t *testing.T) {
	tests := []struct {
		name       string
		params     ListParams
		wantMode   Strategy
		wantOffset int
	}{
		{"offset_zero_is_offset_mode", ListParams{Limit: "10", Offset: "0", Limits: Limits{Max: 100}}, StrategyOffset, 0},
		{"offset_positive", ListParams{Limit: "10", Offset: "5", Limits: Limits{Max: 100}}, StrategyOffset, 5},
		{"cursor_param", ListParams{Limit: "10", Cursor: "abc", Limits: Limits{Max: 100}}, StrategyCursor, 0},
		{"neither_defaults_cursor", ListParams{Limit: "10", Limits: Limits{Max: 100}}, StrategyCursor, 0},
		{"neither_default_offset", ListParams{Limit: "10", Limits: Limits{Max: 100}, DefaultStrategy: StrategyOffset}, StrategyOffset, 0},
		{"offset_param_overrides_default", ListParams{Limit: "10", Offset: "3", Limits: Limits{Max: 100}, DefaultStrategy: StrategyCursor}, StrategyOffset, 3},
		{"cursor_param_overrides_default", ListParams{Limit: "10", Cursor: "abc", Limits: Limits{Max: 100}, DefaultStrategy: StrategyOffset}, StrategyCursor, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseListRequest(tt.params)
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

// TestParseListRequest_NeverClamps proves the strict transport-edge contract:
// out-of-range input is rejected, never silently clamped.
func TestParseListRequest_NeverClamps(t *testing.T) {
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
		_, err := ParseListRequest(ListParams{Limit: tt.input, Limits: Limits{Max: 100}})
		if err == nil {
			t.Errorf("ParseListRequest(%q) err = nil, want containing %q", tt.input, tt.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("ParseListRequest(%q) err = %q, want containing %q", tt.input, err.Error(), tt.wantErr)
		}
	}
}

func TestParseListRequest_MaxBoundary(t *testing.T) {
	req, err := ParseListRequest(ListParams{Limit: "100", Limits: Limits{Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 100 {
		t.Errorf("limit = %d, want 100", req.Limit)
	}
}

func TestParseListRequest_CustomMaxLimit(t *testing.T) {
	req, err := ParseListRequest(ListParams{Limit: "200", Limits: Limits{Max: 500}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 200 {
		t.Errorf("limit = %d, want 200", req.Limit)
	}

	_, err = ParseListRequest(ListParams{Limit: "501", Limits: Limits{Max: 500}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want containing %q and %q", err.Error(), "too large", "500")
	}
}

// TestParseListRequest_CustomDefault proves the resource's effective default is
// applied when the limit param is empty, and a declared default above the
// effective max clamps to the max.
func TestParseListRequest_CustomDefault(t *testing.T) {
	req, err := ParseListRequest(ListParams{Limits: Limits{Default: 50, Max: 500}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 50 {
		t.Errorf("limit = %d, want 50 (custom default on empty)", req.Limit)
	}

	req, err = ParseListRequest(ListParams{Limits: Limits{Default: 500, Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 100 {
		t.Errorf("limit = %d, want 100 (default clamped to max)", req.Limit)
	}
}

func TestParseListRequest_ZeroMaxFallsBackToMaxLimit(t *testing.T) {
	_, err := ParseListRequest(ListParams{Limit: "101", Limits: Limits{}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("err = %q, want containing %q", err.Error(), "100")
	}
}

// TestParseListRequest_Offset covers offset-mode parsing edges: empty is 0,
// a positive value selects offset mode, and non-numeric or negative values are
// rejected.
func TestParseListRequest_Offset(t *testing.T) {
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
			req, err := ParseListRequest(ListParams{Limit: "10", Offset: tt.offsetStr, Limits: Limits{Max: 100}})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want containing %q", err.Error(), tt.wantErr)
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

// TestParseListRequest_Count covers count parsing via strconv.ParseBool,
// including the empty=false default and rejection of non-bool values.
func TestParseListRequest_Count(t *testing.T) {
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
			req, err := ParseListRequest(ListParams{Limit: "10", Count: tt.countStr, Limits: Limits{Max: 100}})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %q, want containing %q", err.Error(), tt.wantErr)
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

// TestParseListRequest_CursorAndOffsetRejected proves a cursor param and an
// offset param together are rejected at the transport edge — including the
// former loophole where offset "0" plus a cursor was silently accepted.
func TestParseListRequest_CursorAndOffsetRejected(t *testing.T) {
	_, err := ParseListRequest(ListParams{Limit: "10", Cursor: "abc123", Offset: "40", Limits: Limits{Max: 100}})
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %q, want containing %q", err.Error(), "mutually exclusive")
	}

	// An offset param of "0" alongside a cursor is now also rejected: an offset
	// param present at all means offset strategy, which excludes a cursor.
	_, err = ParseListRequest(ListParams{Limit: "10", Cursor: "abc123", Offset: "0", Limits: Limits{Max: 100}})
	if err == nil {
		t.Fatal("err = nil, want error for cursor + offset=0")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("err = %q, want containing %q", err.Error(), "mutually exclusive")
	}
}
