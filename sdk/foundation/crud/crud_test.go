package crud

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestListRequest_NormalizedLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"unset_defaults", 0, DefaultLimit},
		{"negative_defaults", -5, DefaultLimit},
		{"within_range", 10, 10},
		{"at_max", MaxLimit, MaxLimit},
		{"over_max_clamps", MaxLimit + 50, MaxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ListRequest{Limit: tt.limit}.NormalizedLimit(Limits{})
			if got != tt.want {
				t.Errorf("NormalizedLimit(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

// TestListRequest_NormalizedLimit_Limits proves the Limits resolution: the zero
// value falls back to the crud constants, a Default-only or Max-only Limits
// overrides just that field, both together apply both, and a declared Default
// greater than the effective Max clamps to the Max (the defensive rule).
func TestListRequest_NormalizedLimit_Limits(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		limits Limits
		want   int
	}{
		// zero Limits — const fallbacks.
		{"zero_unset_defaults", 0, Limits{}, DefaultLimit},
		{"zero_over_max_clamps", MaxLimit + 50, Limits{}, MaxLimit},
		// default-only.
		{"default_only_unset", 0, Limits{Default: 10}, 10},
		{"default_only_over_const_max_clamps", MaxLimit + 50, Limits{Default: 10}, MaxLimit},
		// max-only.
		{"max_only_unset_uses_const_default", 0, Limits{Max: 500}, DefaultLimit},
		{"max_only_over_const_max_allowed", 200, Limits{Max: 500}, 200},
		{"max_only_over_custom_max_clamps", 600, Limits{Max: 500}, 500},
		// both.
		{"both_unset_uses_custom_default", 0, Limits{Default: 50, Max: 500}, 50},
		{"both_over_custom_max_clamps", 600, Limits{Default: 50, Max: 500}, 500},
		// default greater than max — clamps to max.
		{"default_over_max_unset_clamps", 0, Limits{Default: 500, Max: 100}, 100},
		{"default_over_max_within_range", 40, Limits{Default: 500, Max: 100}, 40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ListRequest{Limit: tt.limit}.NormalizedLimit(tt.limits)
			if got != tt.want {
				t.Errorf("NormalizedLimit(%d, %+v) = %d, want %d", tt.limit, tt.limits, got, tt.want)
			}
		})
	}
}

func TestListRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     ListRequest
		wantErr bool
	}{
		{"empty_first_page", ListRequest{}, false},
		{"cursor_default_strategy", ListRequest{Cursor: "abc"}, false},
		{"cursor_strategy_first_page", ListRequest{Strategy: StrategyCursor}, false},
		{"cursor_strategy_with_cursor", ListRequest{Strategy: StrategyCursor, Cursor: "abc"}, false},
		{"cursor_strategy_with_offset", ListRequest{Strategy: StrategyCursor, Offset: 20}, true},
		{"default_strategy_with_offset", ListRequest{Offset: 20}, true},
		{"offset_strategy_zero", ListRequest{Strategy: StrategyOffset, Offset: 0}, false},
		{"offset_strategy_positive", ListRequest{Strategy: StrategyOffset, Offset: 20}, false},
		{"offset_strategy_with_cursor", ListRequest{Strategy: StrategyOffset, Cursor: "abc"}, true},
		{"offset_strategy_negative", ListRequest{Strategy: StrategyOffset, Offset: -1}, true},
		{"unknown_strategy", ListRequest{Strategy: Strategy("sideways")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want error")
				}
				if !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestListRequest_ResolvedStrategy(t *testing.T) {
	tests := []struct {
		name string
		req  ListRequest
		want Strategy
	}{
		{"empty_is_cursor", ListRequest{}, StrategyCursor},
		{"explicit_cursor", ListRequest{Strategy: StrategyCursor}, StrategyCursor},
		{"explicit_offset", ListRequest{Strategy: StrategyOffset}, StrategyOffset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.req.ResolvedStrategy(); got != tt.want {
				t.Errorf("ResolvedStrategy() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapPage(t *testing.T) {
	total := int64(7)
	src := Page[int]{
		Items:          []int{1, 2, 3},
		NextCursor:     "next",
		HasMore:        true,
		HasPrev:        true,
		PreviousCursor: "prev",
		Total:          &total,
	}
	got := MapPage(src, func(n int) string { return strconv.Itoa(n) })

	want := []string{"1", "2", "3"}
	if len(got.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d", len(got.Items), len(want))
	}
	for i := range want {
		if got.Items[i] != want[i] {
			t.Errorf("Items[%d] = %q, want %q", i, got.Items[i], want[i])
		}
	}
	if got.NextCursor != src.NextCursor {
		t.Errorf("NextCursor = %q, want %q", got.NextCursor, src.NextCursor)
	}
	if got.HasMore != src.HasMore {
		t.Errorf("HasMore = %v, want %v", got.HasMore, src.HasMore)
	}
	if got.HasPrev != src.HasPrev {
		t.Errorf("HasPrev = %v, want %v", got.HasPrev, src.HasPrev)
	}
	if got.PreviousCursor != src.PreviousCursor {
		t.Errorf("PreviousCursor = %q, want %q", got.PreviousCursor, src.PreviousCursor)
	}
	if got.Total == nil || *got.Total != total {
		t.Errorf("Total = %v, want %d", got.Total, total)
	}

	// nil Total is preserved as nil.
	none := MapPage(Page[int]{Items: []int{1}}, func(n int) string { return strconv.Itoa(n) })
	if none.Total != nil {
		t.Errorf("Total = %v, want nil", none.Total)
	}
}

func TestErrNotFound_AliasesErrs(t *testing.T) {
	if !errors.Is(ErrNotFound, sdk.ErrNotFound) {
		t.Error("crud.ErrNotFound must alias sdk.ErrNotFound")
	}
}

func TestPage_JSONTags(t *testing.T) {
	total := int64(3)
	p := Page[string]{
		Items:          []string{"a"},
		NextCursor:     "next",
		HasMore:        true,
		HasPrev:        true,
		PreviousCursor: "prev",
		Total:          &total,
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, key := range []string{`"items"`, `"next_cursor"`, `"has_more"`, `"has_prev"`, `"previous_cursor"`, `"total"`} {
		if !strings.Contains(got, key) {
			t.Errorf("marshaled Page missing %s: %s", key, got)
		}
	}

	// omitempty: a zero-value Page emits only items.
	empty, err := json.Marshal(Page[string]{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	for _, key := range []string{"next_cursor", "has_more", "has_prev", "previous_cursor", "total"} {
		if strings.Contains(string(empty), key) {
			t.Errorf("zero Page should omit %s: %s", key, empty)
		}
	}
}
