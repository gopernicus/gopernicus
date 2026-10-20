package memstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestEntryListPreviousPageRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := New().Repositories().Entries
	stamp := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"e3", "e1", "e5", "e2", "e4"} {
		_, err := repo.Create(ctx, content.Entry{ID: id, Type: "article", Slug: id, CreatedAt: stamp})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		for _, limit := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s/limit%d", direction, limit), func(t *testing.T) {
				req := list.Request{Limit: limit, Order: list.NewOrder("created_at", direction), WithCount: true}
				want := []string{"e1", "e2", "e3", "e4", "e5"}
				if direction == list.DESC {
					slices.Reverse(want)
				}
				var seen, previous []string
				for pageNumber := 0; pageNumber < 6; pageNumber++ {
					page, err := repo.List(ctx, content.EntryQuery{Type: "article", Request: req})
					if err != nil {
						t.Fatal(err)
					}
					if page.Total == nil || *page.Total != 5 || page.HasPrev != (pageNumber > 0) {
						t.Fatalf("page %d: total=%v HasPrev=%v", pageNumber, page.Total, page.HasPrev)
					}
					if page.HasPrev {
						backRequest := req
						backRequest.Cursor = page.PreviousCursor
						back, err := repo.List(ctx, content.EntryQuery{Type: "article", Request: backRequest})
						if err != nil {
							t.Fatal(err)
						}
						if got := entryIDs(back.Items); !slices.Equal(got, previous) {
							t.Fatalf("page %d previous items=%v, want %v", pageNumber, got, previous)
						}
					}
					previous = entryIDs(page.Items)
					seen = append(seen, previous...)
					if !page.HasMore {
						break
					}
					if page.NextCursor == "" {
						t.Fatal("HasMore without a next cursor")
					}
					req.Cursor = page.NextCursor
				}
				if !slices.Equal(seen, want) {
					t.Fatalf("forward items=%v, want %v", seen, want)
				}
			})
		}
	}
}

func TestEntryListRejectsWrongCursorValueType(t *testing.T) {
	repo := New().Repositories().Entries
	for _, value := range []any{nil, "2026-09-09T12:00:00Z", int64(1), true} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			token, err := list.EncodeCursor("created_at", value, "e1")
			if err != nil {
				t.Fatal(err)
			}
			_, err = repo.List(context.Background(), content.EntryQuery{
				Type: "article", Request: list.Request{Limit: 1, Cursor: token},
			})
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("wrong cursor type error=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func entryIDs(entries []content.Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}
