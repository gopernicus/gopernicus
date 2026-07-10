package crud

import (
	"testing"
	"time"
)

func TestEncodeDecodeCursor_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		orderField string
		orderValue any
		pk         string
	}{
		{"string", "created_at", "2026-06-21T00:00:00Z", "id-1"},
		{"int", "seq", int64(9007199254740993), "id-2"}, // beyond 2^53
		{"time", "created_at", time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC), "id-3"},
		{"bool", "active", true, "id-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := EncodeCursor(tt.orderField, tt.orderValue, tt.pk)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			c, err := DecodeCursor(tok, tt.orderField)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if c == nil {
				t.Fatal("decoded cursor is nil")
			}
			if c.PK != tt.pk {
				t.Errorf("pk = %q, want %q", c.PK, tt.pk)
			}
			// time restores as time.Time, others compare by value
			switch want := tt.orderValue.(type) {
			case time.Time:
				got, ok := c.OrderValue.(time.Time)
				if !ok || !got.Equal(want) {
					t.Errorf("order value = %v, want %v", c.OrderValue, want)
				}
			default:
				if c.OrderValue != tt.orderValue {
					t.Errorf("order value = %v (%T), want %v (%T)", c.OrderValue, c.OrderValue, tt.orderValue, tt.orderValue)
				}
			}
		})
	}
}

func TestDecodeCursor_EmptyAndStale(t *testing.T) {
	if c, err := DecodeCursor("", "created_at"); err != nil || c != nil {
		t.Errorf("empty token: got %v,%v want nil,nil", c, err)
	}

	tok, _ := EncodeCursor("created_at", "x", "id")
	if c, err := DecodeCursor(tok, "different_field"); err != nil || c != nil {
		t.Errorf("stale cursor: got %v,%v want nil,nil", c, err)
	}
}

func TestDecodeCursor_Malformed(t *testing.T) {
	if _, err := DecodeCursor("!!!not base64!!!", "f"); err == nil {
		t.Error("expected error for malformed token")
	}
}

func TestTrimPage(t *testing.T) {
	encode := func(s string) (string, error) { return EncodeCursor("created_at", s, s) }

	// over-fetched: limit 2, 3 records -> trim, HasMore, NextCursor set
	page, err := TrimPage([]string{"a", "b", "c"}, 2, encode)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Errorf("over-fetch: items=%v hasMore=%v next=%q", page.Items, page.HasMore, page.NextCursor)
	}

	// exact fit: limit 3, 3 records -> no more, empty cursor
	page, err = TrimPage([]string{"a", "b", "c"}, 3, encode)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.HasMore || page.NextCursor != "" {
		t.Errorf("exact fit: items=%v hasMore=%v next=%q", page.Items, page.HasMore, page.NextCursor)
	}
}

// TestPaginateSynthetic walks a synthetic slice through ListRequest -> Page
// twice, proving NextCursor advances and the walk terminates. This is the
// "real behavior" check for this pure-logic package.
func TestPaginateSynthetic(t *testing.T) {
	type row struct{ id string }
	all := []row{{"r1"}, {"r2"}, {"r3"}, {"r4"}, {"r5"}}

	// fetch simulates a keyset query: returns up to limit+1 rows strictly
	// after the cursor's PK (ids are monotonically sortable here).
	fetch := func(req ListRequest) Page[row] {
		limit := req.NormalizedLimit(Limits{})
		start := 0
		if req.Cursor != "" {
			c, err := DecodeCursor(req.Cursor, "id")
			if err != nil {
				t.Fatalf("decode in fetch: %v", err)
			}
			for i, r := range all {
				if r.id == c.PK {
					start = i + 1
					break
				}
			}
		}
		end := start + limit + 1
		if end > len(all) {
			end = len(all)
		}
		page, err := TrimPage(all[start:end], limit, func(r row) (string, error) {
			return EncodeCursor("id", r.id, r.id)
		})
		if err != nil {
			t.Fatalf("trim: %v", err)
		}
		return page
	}

	var seen []string
	req := ListRequest{Limit: 2}
	for i := 0; i < 10; i++ { // bounded loop guard
		page := fetch(req)
		for _, r := range page.Items {
			seen = append(seen, r.id)
		}
		if !page.HasMore {
			break
		}
		req.Cursor = page.NextCursor
	}

	want := []string{"r1", "r2", "r3", "r4", "r5"}
	if len(seen) != len(want) {
		t.Fatalf("walked %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("walked %v, want %v", seen, want)
		}
	}
}
