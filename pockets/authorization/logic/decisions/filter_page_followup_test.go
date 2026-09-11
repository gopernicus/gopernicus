package decisions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

func TestFilterPageDefaultKeepsRequestedPullSize(t *testing.T) {
	for _, tc := range []struct{ every, pulls int }{{1, 1}, {2, 2}, {100, 91}} {
		t.Run(fmt.Sprintf("one_in_%d", tc.every), func(t *testing.T) {
			universe := docIDs(1000)
			var allowed []string
			for i, id := range universe {
				if i%tc.every == 0 {
					allowed = append(allowed, id)
				}
			}
			svc := filterPageService(t, authmodel.EvaluationLimits{}, allowed)
			source := &candidateRecorder{ids: universe}
			page, err := decisions.FilterPage(context.Background(), svc.Decisions, filterPageRequest(source, 10, ""))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(page.Items, allowed[:10]) || len(source.limits) != tc.pulls {
				t.Fatalf("items=%v pulls=%d want=%d", page.Items, len(source.limits), tc.pulls)
			}
			for _, limit := range source.limits {
				if limit != 10 {
					t.Fatalf("shrinking candidate pull: %v", source.limits)
				}
			}
			if page.NextCursor != allowed[9] || !page.HasMore || page.ScanLimitReached {
				t.Fatalf("continuation: %+v", page)
			}
			// A partly consumed last pull must not skip its suffix on the next call.
			next, err := decisions.FilterPage(context.Background(), svc.Decisions, filterPageRequest(source, 10, page.NextCursor))
			if err != nil {
				t.Fatal(err)
			}
			if want := allowed[10:min(20, len(allowed))]; !reflect.DeepEqual(next.Items, want) {
				t.Fatalf("next items=%v want=%v", next.Items, want)
			}
		})
	}
}

func TestFilterPageSourceCancellationWinsOverSuccessfulEmptyPage(t *testing.T) {
	svc := filterPageService(t, authmodel.EvaluationLimits{}, nil)
	for _, hasMore := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		req := filterPageRequest(&candidateRecorder{}, 3, "")
		calls := 0
		req.Source = func(context.Context, string, int) (decisions.CandidatePage[string], error) {
			calls++
			cancel()
			return decisions.CandidatePage[string]{HasMore: hasMore}, nil
		}
		got, err := decisions.FilterPage(ctx, svc.Decisions, req)
		cancel()
		if !errors.Is(err, context.Canceled) || got.Items != nil || got.HasMore || calls != 1 {
			t.Fatalf("canceled source: %+v %v calls=%d", got, err, calls)
		}
	}
}
