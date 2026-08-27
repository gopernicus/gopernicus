package crud

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestParseListQuery_Keys proves every canonical page key is read from the
// query — and that `order` is not one of them.
func TestParseListQuery_Keys(t *testing.T) {
	q := url.Values{
		QueryKeyLimit:  {"10"},
		QueryKeyCursor: {"abc123"},
		QueryKeyCount:  {"true"},
		QueryKeySearch: {"needle"},
	}

	req, err := ParseListQuery(q, ListQueryOptions{Limits: Limits{Max: 100}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Limit != 10 {
		t.Errorf("limit = %d, want 10", req.Limit)
	}
	if req.Cursor != "abc123" {
		t.Errorf("cursor = %q, want %q", req.Cursor, "abc123")
	}
	if !req.WithCount {
		t.Errorf("withCount = false, want true")
	}
	if req.Search != "needle" {
		t.Errorf("search = %q, want %q", req.Search, "needle")
	}
	if req.Strategy != StrategyCursor {
		t.Errorf("strategy = %q, want %q", req.Strategy, StrategyCursor)
	}
}

// TestParseListQuery_OffsetKey covers the offset key, which is mutually
// exclusive with cursor and so cannot ride the same query as one.
func TestParseListQuery_OffsetKey(t *testing.T) {
	req, err := ParseListQuery(url.Values{QueryKeyOffset: {"40"}}, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Offset != 40 {
		t.Errorf("offset = %d, want 40", req.Offset)
	}
	if req.Strategy != StrategyOffset {
		t.Errorf("strategy = %q, want %q (offset param present)", req.Strategy, StrategyOffset)
	}
}

func TestParseListQuery_TrimsSearch(t *testing.T) {
	req, err := ParseListQuery(url.Values{QueryKeySearch: {"  needle  "}}, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Search != "needle" {
		t.Errorf("search = %q, want %q", req.Search, "needle")
	}

	req, err = ParseListQuery(url.Values{QueryKeySearch: {"   "}}, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Search != "" {
		t.Errorf("search = %q, want empty (blank means no search)", req.Search)
	}
}

// TestParseListQuery_DefaultStrategy proves the resource-side default applies
// when the query names neither a cursor nor an offset.
func TestParseListQuery_DefaultStrategy(t *testing.T) {
	req, err := ParseListQuery(url.Values{}, ListQueryOptions{DefaultStrategy: StrategyOffset})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Strategy != StrategyOffset {
		t.Errorf("strategy = %q, want %q", req.Strategy, StrategyOffset)
	}

	req, err = ParseListQuery(url.Values{QueryKeyCursor: {"abc"}}, ListQueryOptions{DefaultStrategy: StrategyOffset})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Strategy != StrategyCursor {
		t.Errorf("strategy = %q, want %q (cursor param overrides the default)", req.Strategy, StrategyCursor)
	}

	req, err = ParseListQuery(url.Values{}, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if req.Strategy != StrategyCursor {
		t.Errorf("strategy = %q, want %q (zero DefaultStrategy)", req.Strategy, StrategyCursor)
	}
}

// TestParseListQuery_LimitsMatchParseListRequest proves ParseListQuery is
// ParseListRequest over the query keys: the same Limits resolve the same way at
// both entry points.
func TestParseListQuery_LimitsMatchParseListRequest(t *testing.T) {
	tests := []struct {
		name   string
		limit  string
		limits Limits
	}{
		{"empty_uses_effective_default", "", Limits{Default: 50, Max: 500}},
		{"empty_zero_limits_uses_constants", "", Limits{}},
		{"default_above_max_clamps", "", Limits{Default: 500, Max: 100}},
		{"explicit_within_max", "200", Limits{Max: 500}},
		{"explicit_above_max_rejected", "101", Limits{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := url.Values{}
			if tt.limit != "" {
				q.Set(QueryKeyLimit, tt.limit)
			}
			got, gotErr := ParseListQuery(q, ListQueryOptions{Limits: tt.limits})
			want, wantErr := ParseListRequest(ListParams{Limit: tt.limit, Limits: tt.limits})
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("err = %v, want %v", gotErr, wantErr)
			}
			if gotErr != nil {
				if gotErr.Error() != wantErr.Error() {
					t.Errorf("err = %q, want %q", gotErr.Error(), wantErr.Error())
				}
				return
			}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

// TestParseListQuery_IgnoresOrderKey pins D1: order has a per-aggregate
// allow-list, so ParseListQuery never reads it — ParseOrder does.
func TestParseListQuery_IgnoresOrderKey(t *testing.T) {
	q := url.Values{QueryKeyLimit: {"10"}, QueryKeyOrder: {"nope:sideways"}}

	got, err := ParseListQuery(q, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v, want nil (order is not this parser's concern)", err)
	}
	want, err := ParseListQuery(url.Values{QueryKeyLimit: {"10"}}, ListQueryOptions{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v (order key must not change the request)", got, want)
	}
}

// TestParseListQuery_RejectionsWrapInvalidInput proves the rejections reaching a
// transport through this parser carry both the sentence and the kernel sentinel
// web.ErrFromDomain classifies on.
func TestParseListQuery_RejectionsWrapInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		query   url.Values
		wantErr string
	}{
		{"limit_not_a_number", url.Values{QueryKeyLimit: {"zero"}}, "page limit conversion"},
		{"limit_too_small", url.Values{QueryKeyLimit: {"0"}}, "rows value too small"},
		{"limit_too_large", url.Values{QueryKeyLimit: {"101"}}, "rows value too large"},
		{"cursor_and_offset", url.Values{QueryKeyCursor: {"abc"}, QueryKeyOffset: {"1"}}, "mutually exclusive"},
		{"offset_not_a_number", url.Values{QueryKeyOffset: {"abc"}}, "page offset conversion"},
		{"offset_negative", url.Values{QueryKeyOffset: {"-1"}}, "offset value too small"},
		{"count_not_a_bool", url.Values{QueryKeyCount: {"yes-please"}}, "page count conversion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseListQuery(tt.query, ListQueryOptions{})
			if err == nil {
				t.Fatalf("err = nil, want containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want containing %q", err.Error(), tt.wantErr)
			}
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Errorf("err = %v, want wrapping sdk.ErrInvalidInput", err)
			}
		})
	}
}
