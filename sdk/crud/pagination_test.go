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
	req, err := ParseListRequest("", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", req.Limit, DefaultLimit)
	}
	if req.Cursor != "" {
		t.Errorf("cursor = %q, want empty", req.Cursor)
	}
}

func TestParseListRequest_CustomLimitAndCursor(t *testing.T) {
	req, err := ParseListRequest("10", "abc123", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 10 || req.Cursor != "abc123" {
		t.Errorf("got %+v, want {10 abc123}", req)
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
		_, err := ParseListRequest(tt.input, "", 100)
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
	req, err := ParseListRequest("100", "", 100)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 100 {
		t.Errorf("limit = %d, want 100", req.Limit)
	}
}

func TestParseListRequest_CustomMaxLimit(t *testing.T) {
	req, err := ParseListRequest("200", "", 500)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 200 {
		t.Errorf("limit = %d, want 200", req.Limit)
	}

	_, err = ParseListRequest("501", "", 500)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "too large") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want containing %q and %q", err.Error(), "too large", "500")
	}
}

func TestParseListRequest_ZeroMaxFallsBackToMaxLimit(t *testing.T) {
	_, err := ParseListRequest("101", "", 0)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("err = %q, want containing %q", err.Error(), "100")
	}
}
