package decisions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// filterPageSchema is the flat postfilter shape: a doc's `view` is granted
// directly to a viewer, so FilterAuthorized takes the batched direct path.
func filterPageSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{{
		Name: "doc",
		Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("viewer")),
			},
		},
	}})
}

// filterPageThroughSchema is the container→items shape: a doc inherits `view`
// from its parent folder, so FilterAuthorized takes the SEQUENTIAL Check path.
func filterPageThroughSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{
			Name: "folder",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"view": relationships.AnyOf(relationships.Direct("viewer")),
				},
			},
		},
		{
			Name: "doc",
			Def: relationships.ResourceTypeDef{
				Relations: map[string]relationships.RelationDef{
					"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "folder"}}},
				},
				Permissions: map[string]relationships.PermissionRule{
					"view": relationships.AnyOf(relationships.Through("parent", "view")),
				},
			},
		},
	})
}

// docIDs is the candidate universe in the source's stable order: d01, d02, ….
func docIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, fmt.Sprintf("d%02d", i))
	}
	return ids
}

// candidateRecorder is a fake keyset CandidateSource over an ordered slice of
// row ids. The cursor IS the row id, with exclusive "after" semantics, so a
// candidate's NextCursor is its own id. Each knob makes it misbehave in exactly
// one way FilterPage must refuse.
type candidateRecorder struct {
	ids     []string
	limits  []int    // the limit requested by each pull, in order
	cursors []string // the cursor presented by each pull, in order

	overReturn    bool // return limit+1 candidates
	emptyWithMore bool // return no candidates but claim more
	emptyCursor   bool // blank the first candidate's cursor
	nonAdvancing  bool // repeat the previous candidate's cursor
	cycle         bool // return a cursor already seen earlier in the page
}

func (r *candidateRecorder) source(ctx context.Context, cursor string, limit int) (decisions.CandidatePage[string], error) {
	r.limits = append(r.limits, limit)
	r.cursors = append(r.cursors, cursor)
	if r.emptyWithMore {
		return decisions.CandidatePage[string]{HasMore: true}, nil
	}

	start := 0
	if cursor != "" {
		start = -1
		for i, id := range r.ids {
			if id == cursor {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return decisions.CandidatePage[string]{}, fmt.Errorf("candidateRecorder: unknown cursor %q", cursor)
		}
	}
	want := limit
	if r.overReturn {
		want = limit + 1
	}
	end := min(start+want, len(r.ids))

	items := make([]decisions.Candidate[string], 0, end-start)
	for _, id := range r.ids[start:end] {
		items = append(items, decisions.Candidate[string]{Item: id, NextCursor: id})
	}
	if r.emptyCursor && len(items) > 0 {
		items[0].NextCursor = ""
	}
	if r.nonAdvancing && len(items) > 1 {
		items[1].NextCursor = items[0].NextCursor
	}
	if r.cycle && len(items) > 2 {
		items[2].NextCursor = items[0].NextCursor
	}
	return decisions.CandidatePage[string]{Items: items, HasMore: end < len(r.ids)}, nil
}

// filterPageService builds a memstore-backed Service over filterPageSchema and
// seeds a direct viewer grant for user u1 on each allowed doc.
func filterPageService(t *testing.T, limits authmodel.EvaluationLimits, allowed []string) authorization.Components {
	t.Helper()
	svc, store := budgetService(t, filterPageSchema(), limits)
	tuples := make([]relationships.CreateRelationship, 0, len(allowed))
	for _, id := range allowed {
		tuples = append(tuples, relationships.CreateRelationship{
			ResourceType: "doc", ResourceID: id, Relation: "viewer", SubjectType: "user", SubjectID: "u1",
		})
	}
	if len(tuples) > 0 {
		if err := store.CreateRelationships(context.Background(), tuples); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return svc
}

// filterPageRequest is the standard doc#view request over src.
func filterPageRequest(src *candidateRecorder, limit int, cursor string) decisions.FilterPageRequest[string] {
	return decisions.FilterPageRequest[string]{
		Principal:    authmodel.PrincipalRef{Type: "user", ID: "u1"},
		Permission:   "view",
		ResourceType: "doc",
		ID:           func(id string) string { return id },
		Source:       src.source,
		Limit:        limit,
		Cursor:       cursor,
	}
}

func filterPageBatchRequest(src *candidateRecorder, limit int, cursor string, batchSize int) decisions.FilterPageRequest[string] {
	req := filterPageRequest(src, limit, cursor)
	req.BatchSize = batchSize
	return req
}

func TestFilterPageBatchSizeValidation(t *testing.T) {
	for _, batchSize := range []int{-1, 6} {
		svc := filterPageService(t, authmodel.EvaluationLimits{MaxBatchSize: 5}, nil)
		source := &candidateRecorder{ids: docIDs(10)}
		_, err := decisions.FilterPage(context.Background(), svc.Decisions, filterPageBatchRequest(source, 3, "", batchSize))
		if !errors.Is(err, sdk.ErrInvalidInput) || len(source.limits) != 0 {
			t.Fatalf("BatchSize %d: error %v, source calls %d", batchSize, err, len(source.limits))
		}
	}
}

func TestFilterPageDenseDefaultDoesNotOverfetch(t *testing.T) {
	universe := docIDs(20)
	svc := filterPageService(t, authmodel.EvaluationLimits{}, universe)
	source := &candidateRecorder{ids: universe}
	page, err := decisions.FilterPage(context.Background(), svc.Decisions, filterPageRequest(source, 3, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Items, universe[:3]) || !reflect.DeepEqual(source.limits, []int{3}) || page.ScanLimitReached {
		t.Fatalf("page %+v; candidate requests %v", page, source.limits)
	}
}

// TestFilterPageDense proves the dense path: one over-fetched pull fills the
// page, and HasMore distinguishes "candidates remain" from "exactly exhausted".
func TestFilterPageDense(t *testing.T) {
	ctx := context.Background()

	t.Run("fills_from_one_pull_with_candidates_remaining", func(t *testing.T) {
		universe := docIDs(10)
		svc := filterPageService(t, authmodel.EvaluationLimits{}, universe)
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if want := []string{"d01", "d02", "d03"}; !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("Items = %v, want %v", page.Items, want)
		}
		if !page.HasMore {
			t.Fatal("HasMore = false, want true (candidates remain)")
		}
		if page.NextCursor != "d03" {
			t.Fatalf("NextCursor = %q, want \"d03\"", page.NextCursor)
		}
		if len(src.limits) != 1 {
			t.Fatalf("source calls = %d, want 1", len(src.limits))
		}
	})

	t.Run("exactly_exhausted_reports_no_more", func(t *testing.T) {
		universe := docIDs(3)
		svc := filterPageService(t, authmodel.EvaluationLimits{}, universe)
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if want := []string{"d01", "d02", "d03"}; !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("Items = %v, want %v", page.Items, want)
		}
		if page.HasMore {
			t.Fatal("HasMore = true, want false (the source ended with the page)")
		}
		if page.NextCursor != "d03" {
			t.Fatalf("NextCursor = %q, want \"d03\"", page.NextCursor)
		}
	})
}

// TestFilterPageScanBound proves B3: reaching MaxFilterScan with the page
// unfilled returns a PARTIAL page plus a continuation — never
// ErrEvaluationLimit — and a source that ends exactly at the bound reports no
// more.
func TestFilterPageScanBound(t *testing.T) {
	ctx := context.Background()

	t.Run("sparse_access_stops_at_the_bound_with_a_continuation", func(t *testing.T) {
		universe := docIDs(20)
		svc := filterPageService(t, authmodel.EvaluationLimits{MaxFilterScan: 4}, []string{"d19"})
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("Items = %v, want none allowed within the bound", page.Items)
		}
		if page.Items == nil {
			t.Fatal("Items must be a non-nil slice")
		}
		if !page.HasMore || !page.ScanLimitReached {
			t.Fatal("scan-bound page must report remaining candidates and ScanLimitReached")
		}
		if page.NextCursor != "d04" {
			t.Fatalf("NextCursor = %q, want \"d04\" (after the last scanned candidate)", page.NextCursor)
		}
		if want := []int{3, 1}; !reflect.DeepEqual(src.limits, want) {
			t.Fatalf("requested limits = %v, want %v (clamped to the remaining scan budget)", src.limits, want)
		}
	})

	t.Run("exhaustion_exactly_at_the_bound_reports_no_more", func(t *testing.T) {
		universe := docIDs(4)
		svc := filterPageService(t, authmodel.EvaluationLimits{MaxFilterScan: 4}, []string{"d02"})
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if want := []string{"d02"}; !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("Items = %v, want %v", page.Items, want)
		}
		if page.HasMore || page.ScanLimitReached {
			t.Fatal("exhausted source must report neither HasMore nor ScanLimitReached")
		}
		if page.NextCursor != "d04" {
			t.Fatalf("NextCursor = %q, want \"d04\"", page.NextCursor)
		}
	})
}

// TestFilterPageMidBatchFill proves the mid-batch stop: when the page fills at
// candidate k of an over-fetched pull, the continuation is candidate k's cursor
// and the next call resumes at k+1 — no skipped row, no repeated row. Walking
// the whole universe must reproduce exactly the allowed rows in source order.
func TestFilterPageMidBatchFill(t *testing.T) {
	ctx := context.Background()
	universe := docIDs(12)
	allowed := []string{"d01", "d02", "d03", "d05", "d08", "d09", "d10", "d12"}
	svc := filterPageService(t, authmodel.EvaluationLimits{}, allowed)

	t.Run("continuation_is_the_last_consumed_candidate", func(t *testing.T) {
		src := &candidateRecorder{ids: universe}
		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageBatchRequest(src, 2, "", 4))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if want := []string{"d01", "d02"}; !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("Items = %v, want %v", page.Items, want)
		}
		if !page.HasMore {
			t.Fatal("HasMore = false, want true (the pull has an unconsumed suffix)")
		}
		if page.NextCursor != "d02" {
			t.Fatalf("NextCursor = %q, want \"d02\" (the last CONSUMED candidate, not the pull's end)", page.NextCursor)
		}
		if want := []int{4}; !reflect.DeepEqual(src.limits, want) {
			t.Fatalf("requested limits = %v, want %v (2× the page size)", src.limits, want)
		}

		// The next call resumes at d03 — the candidate after the continuation.
		next := &candidateRecorder{ids: universe}
		page2, err := decisions.FilterPage(ctx, svc.Decisions, filterPageBatchRequest(next, 2, page.NextCursor, 4))
		if err != nil {
			t.Fatalf("FilterPage second: %v", err)
		}
		if want := []string{"d03", "d05"}; !reflect.DeepEqual(page2.Items, want) {
			t.Fatalf("second Items = %v, want %v (d04 denied, nothing skipped or repeated)", page2.Items, want)
		}
		if page2.NextCursor != "d05" {
			t.Fatalf("second NextCursor = %q, want \"d05\"", page2.NextCursor)
		}
	})

	t.Run("walking_the_universe_yields_every_allowed_row_once", func(t *testing.T) {
		for _, limit := range []int{1, 2, 3, 5} {
			var got []string
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > len(universe)+2 {
					t.Fatalf("limit %d: walk did not terminate", limit)
				}
				src := &candidateRecorder{ids: universe}
				page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageBatchRequest(src, limit, cursor, 2*limit))
				if err != nil {
					t.Fatalf("limit %d: FilterPage: %v", limit, err)
				}
				got = append(got, page.Items...)
				if !page.HasMore {
					break
				}
				if page.NextCursor == "" {
					t.Fatalf("limit %d: HasMore with no continuation", limit)
				}
				cursor = page.NextCursor
			}
			if !reflect.DeepEqual(got, allowed) {
				t.Fatalf("limit %d: walk = %v, want %v", limit, got, allowed)
			}
		}
	})
}

// TestFilterPageSourceContractViolations proves every way a candidate source
// can break its contract is refused with sdk.ErrInvalidInput rather than
// spinning, skipping, or repeating rows.
func TestFilterPageSourceContractViolations(t *testing.T) {
	ctx := context.Background()
	universe := docIDs(10)
	svc := filterPageService(t, authmodel.EvaluationLimits{}, universe)

	cases := map[string]*candidateRecorder{
		"over_return":     {ids: universe, overReturn: true},
		"empty_with_more": {ids: universe, emptyWithMore: true},
		"empty_cursor":    {ids: universe, emptyCursor: true},
		"non_advancing":   {ids: universe, nonAdvancing: true},
		"cycle":           {ids: universe, cycle: true},
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
			if !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
			}
		})
	}
}

// TestFilterPageEmptySource proves a source with nothing to offer is normal
// exhaustion: a non-nil empty page, no continuation, and no decision spent.
func TestFilterPageEmptySource(t *testing.T) {
	ctx := context.Background()
	svc := filterPageService(t, authmodel.EvaluationLimits{}, nil)
	src := &candidateRecorder{ids: nil}

	page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
	if err != nil {
		t.Fatalf("FilterPage: %v", err)
	}
	if page.Items == nil {
		t.Fatal("Items must be a non-nil slice so an empty page marshals items:[]")
	}
	if len(page.Items) != 0 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("empty exhaustion = %+v, want an empty page with no continuation", page)
	}
}

// TestFilterPageCanceledContext proves a canceled context is refused before the
// source is touched at all.
func TestFilterPageCanceledContext(t *testing.T) {
	svc := filterPageService(t, authmodel.EvaluationLimits{}, docIDs(4))
	src := &candidateRecorder{ids: docIDs(4)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 3, ""))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(src.limits) != 0 {
		t.Fatalf("source calls = %d, want 0 (no pull after cancellation)", len(src.limits))
	}
}

// TestFilterPageLimits proves the page-size contract: 0 is list.DefaultLimit,
// negative and over-MaxBatchSize are sdk.ErrInvalidInput, and no pull ever
// exceeds MaxBatchSize.
func TestFilterPageLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("zero_limit_is_crud_default", func(t *testing.T) {
		universe := docIDs(10)
		svc := filterPageService(t, authmodel.EvaluationLimits{}, universe)
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 0, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if len(page.Items) != len(universe) {
			t.Fatalf("Items = %v, want the whole universe under the default limit", page.Items)
		}
		want := min(list.DefaultLimit, authmodel.DefaultMaxBatchSize, authmodel.DefaultMaxFilterScan)
		if got := src.limits[0]; got != want {
			t.Fatalf("first requested limit = %d, want %d", got, want)
		}
	})

	t.Run("negative_limit_rejected", func(t *testing.T) {
		svc := filterPageService(t, authmodel.EvaluationLimits{}, nil)
		src := &candidateRecorder{ids: docIDs(4)}
		_, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, -1, ""))
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
		if len(src.limits) != 0 {
			t.Fatalf("source calls = %d, want 0 (validation precedes any pull)", len(src.limits))
		}
	})

	t.Run("limit_above_max_batch_size_rejected", func(t *testing.T) {
		svc := filterPageService(t, authmodel.EvaluationLimits{MaxBatchSize: 5}, nil)
		src := &candidateRecorder{ids: docIDs(20)}
		_, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 6, ""))
		if !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
		if len(src.limits) != 0 {
			t.Fatalf("source calls = %d, want 0", len(src.limits))
		}
	})

	t.Run("pull_never_exceeds_max_batch_size", func(t *testing.T) {
		universe := docIDs(20)
		svc := filterPageService(t, authmodel.EvaluationLimits{MaxBatchSize: 4}, []string{"d20"})
		src := &candidateRecorder{ids: universe}

		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 4, ""))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		if want := []string{"d20"}; !reflect.DeepEqual(page.Items, want) {
			t.Fatalf("Items = %v, want %v", page.Items, want)
		}
		for i, got := range src.limits {
			if got > 4 {
				t.Fatalf("pull %d requested %d candidates, want <= MaxBatchSize 4", i, got)
			}
		}
		if len(src.limits) < 2 {
			t.Fatalf("source calls = %d, want several bounded pulls", len(src.limits))
		}
	})
}

// TestFilterPageInvalidRequest proves the request validation runs before any
// source call: a malformed principal/permission/resource type and a missing ID
// or Source are all refused.
func TestFilterPageInvalidRequest(t *testing.T) {
	ctx := context.Background()
	svc := filterPageService(t, authmodel.EvaluationLimits{}, nil)
	universe := docIDs(4)

	t.Run("blank_principal", func(t *testing.T) {
		src := &candidateRecorder{ids: universe}
		req := filterPageRequest(src, 3, "")
		req.Principal = authmodel.PrincipalRef{}
		if _, err := decisions.FilterPage(ctx, svc.Decisions, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
		if len(src.limits) != 0 {
			t.Fatalf("source calls = %d, want 0", len(src.limits))
		}
	})

	t.Run("blank_permission", func(t *testing.T) {
		src := &candidateRecorder{ids: universe}
		req := filterPageRequest(src, 3, "")
		req.Permission = ""
		if _, err := decisions.FilterPage(ctx, svc.Decisions, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
	})

	t.Run("blank_resource_type", func(t *testing.T) {
		src := &candidateRecorder{ids: universe}
		req := filterPageRequest(src, 3, "")
		req.ResourceType = ""
		if _, err := decisions.FilterPage(ctx, svc.Decisions, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
	})

	t.Run("nil_id_function", func(t *testing.T) {
		src := &candidateRecorder{ids: universe}
		req := filterPageRequest(src, 3, "")
		req.ID = nil
		if _, err := decisions.FilterPage(ctx, svc.Decisions, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
	})

	t.Run("nil_source", func(t *testing.T) {
		req := filterPageRequest(&candidateRecorder{ids: universe}, 3, "")
		req.Source = nil
		if _, err := decisions.FilterPage(ctx, svc.Decisions, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("want sdk.ErrInvalidInput, got %v", err)
		}
	})
}

// TestFilterPageNoDecisionKind proves a host with no model-bearing kind fails
// closed on the wiring fault before it pulls a single candidate.
func TestFilterPageNoDecisionKind(t *testing.T) {
	comps, err := authorization.New(authorization.Repositories{Roles: memory.NewRoles()})
	if err != nil {
		t.Fatalf("NewService roles-only: %v", err)
	}
	src := &candidateRecorder{ids: docIDs(4)}

	if _, err := decisions.FilterPage(context.Background(), comps.Decisions, filterPageRequest(src, 3, "")); !errors.Is(err, authmodel.ErrNoDecisionKind) {
		t.Fatalf("want ErrNoDecisionKind, got %v", err)
	}
	if len(src.limits) != 0 {
		t.Fatalf("source calls = %d, want 0", len(src.limits))
	}
}

// TestFilterPageThroughPermission proves the loop composes with the SEQUENTIAL
// Check path: a container→items permission (doc inherits view from its parent
// folder) pages exactly like a direct one.
func TestFilterPageThroughPermission(t *testing.T) {
	ctx := context.Background()
	svc, store := budgetService(t, filterPageThroughSchema(), authmodel.EvaluationLimits{})

	universe := docIDs(6)
	tuples := []relationships.CreateRelationship{
		{ResourceType: "folder", ResourceID: "f1", Relation: "viewer", SubjectType: "user", SubjectID: "u1"},
	}
	var allowed []string
	for i, id := range universe {
		if i%2 == 1 { // d02, d04, d06 live in the folder u1 can view.
			tuples = append(tuples, relationships.CreateRelationship{
				ResourceType: "doc", ResourceID: id, Relation: "parent", SubjectType: "folder", SubjectID: "f1",
			})
			allowed = append(allowed, id)
		}
	}
	if err := store.CreateRelationships(ctx, tuples); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var got []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(universe) {
			t.Fatal("walk did not terminate")
		}
		src := &candidateRecorder{ids: universe}
		page, err := decisions.FilterPage(ctx, svc.Decisions, filterPageRequest(src, 2, cursor))
		if err != nil {
			t.Fatalf("FilterPage: %v", err)
		}
		got = append(got, page.Items...)
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(got, allowed) {
		t.Fatalf("Through walk = %v, want %v", got, allowed)
	}
}
