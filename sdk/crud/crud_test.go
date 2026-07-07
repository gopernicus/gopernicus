package crud

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/errs"
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
			got := ListRequest{Limit: tt.limit}.NormalizedLimit()
			if got != tt.want {
				t.Errorf("NormalizedLimit(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

func TestErrNotFound_AliasesErrs(t *testing.T) {
	if !errors.Is(ErrNotFound, errs.ErrNotFound) {
		t.Error("crud.ErrNotFound must alias errs.ErrNotFound")
	}
}

func TestPage_JSONTags(t *testing.T) {
	p := Page[string]{
		Items:          []string{"a"},
		NextCursor:     "next",
		HasMore:        true,
		HasPrev:        true,
		PreviousCursor: "prev",
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, key := range []string{`"items"`, `"next_cursor"`, `"has_more"`, `"has_prev"`, `"previous_cursor"`} {
		if !strings.Contains(got, key) {
			t.Errorf("marshaled Page missing %s: %s", key, got)
		}
	}

	// omitempty: a zero-value Page emits only items.
	empty, err := json.Marshal(Page[string]{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	for _, key := range []string{"next_cursor", "has_more", "has_prev", "previous_cursor"} {
		if strings.Contains(string(empty), key) {
			t.Errorf("zero Page should omit %s: %s", key, empty)
		}
	}
}
