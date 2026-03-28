package fop_test

import (
	"context"
	"errors"
	"testing"

	bridfop "github.com/gopernicus/gopernicus/bridge/transit/fop"
	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

// =============================================================================
// Test helpers
// =============================================================================

// item is the generic type used in all PostfilterLoop tests.
type item struct{ id string }

func getID(i item) string { return i.id }

func items(ids ...string) []item {
	out := make([]item, len(ids))
	for i, id := range ids {
		out[i] = item{id}
	}
	return out
}

// listPage describes one response from the list function.
type listPage struct {
	batch      []item
	nextCursor string
	err        error
}

// makeList builds a list function from a sequence of pages.
// Each call to the returned function consumes the next page.
// calls is populated with every PageStringCursor the function received.
func makeList(pages []listPage, calls *[]fop.PageStringCursor) func(context.Context, fop.PageStringCursor) ([]item, fop.Pagination, error) {
	idx := 0
	return func(ctx context.Context, p fop.PageStringCursor) ([]item, fop.Pagination, error) {
		if calls != nil {
			*calls = append(*calls, p)
		}
		if idx >= len(pages) {
			return nil, fop.Pagination{}, nil
		}
		pg := pages[idx]
		idx++
		if pg.err != nil {
			return nil, fop.Pagination{}, pg.err
		}
		return pg.batch, fop.Pagination{NextCursor: pg.nextCursor}, nil
	}
}

// makeAuthorizer builds an Authorizer backed by a mock store.
// allowedIDs is the set of resource IDs that are authorized.
// If isPlatformAdmin is true, all IDs are allowed regardless of allowedIDs.
// batchErr, if non-nil, is returned by CheckBatchDirect.
func makeAuthorizer(allowedIDs map[string]bool, isPlatformAdmin bool, batchErr error) *authorization.Authorizer {
	schema := authorization.Schema{
		ResourceTypes: map[string]authorization.ResourceTypeDef{
			"item": {
				Permissions: map[string]authorization.PermissionRule{
					"read": authorization.AnyOf(authorization.Direct("reader")),
				},
			},
		},
	}
	store := &mockStorer{
		allowedIDs:      allowedIDs,
		isPlatformAdmin: isPlatformAdmin,
		batchErr:        batchErr,
	}
	return authorization.NewAuthorizer(store, schema, authorization.Config{})
}

// =============================================================================
// Tests
// =============================================================================

func TestPostfilterLoop(t *testing.T) {
	subject := authorization.Subject{Type: "user", ID: "user-1"}

	tests := []struct {
		name string

		// Input
		pages           []listPage
		allowedIDs      map[string]bool
		isPlatformAdmin bool
		batchErr        error
		limit           int
		startCursor     string

		// Expectations
		wantIDs        []string
		wantNextCursor string // from the last pagination returned
		wantListCalls  int
		wantErr        bool
	}{
		{
			name:          "empty_source",
			pages:         []listPage{{batch: nil, nextCursor: ""}},
			allowedIDs:    map[string]bool{},
			limit:         5,
			wantIDs:       nil,
			wantListCalls: 1,
		},
		{
			name: "all_authorized_fills_limit",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6"), nextCursor: "c1"},
			},
			allowedIDs: map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true, "6": true},
			limit:      3,
			// overfetch = 6, batch has 6 all authorized — accumulates 3 then breaks
			wantIDs:        []string{"1", "2", "3"},
			wantNextCursor: "c1",
			wantListCalls:  1,
		},
		{
			name: "source_exhausted_below_limit",
			pages: []listPage{
				// 5 items < overfetch(20) — source exhausted
				{batch: items("1", "2", "3", "4", "5"), nextCursor: ""},
			},
			allowedIDs: map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true},
			limit:      10,
			wantIDs:    []string{"1", "2", "3", "4", "5"},
			wantListCalls: 1,
		},
		{
			name: "none_authorized_no_next_cursor",
			pages: []listPage{
				// batch exactly equals overfetch, but no cursor — loop stops
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: ""},
			},
			allowedIDs:    map[string]bool{},
			limit:         5,
			wantIDs:       nil,
			wantListCalls: 1,
		},
		{
			name: "none_authorized_multi_page_then_exhausted",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: "c1"},
				{batch: items("11", "12", "13", "14", "15", "16", "17", "18", "19", "20"), nextCursor: ""},
			},
			allowedIDs:    map[string]bool{},
			limit:         5,
			wantIDs:       nil,
			wantListCalls: 2,
		},
		{
			name: "partial_auth_needs_two_pages",
			pages: []listPage{
				// 10 items, 3 authorized — still need 2 more
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: "c1"},
				// 10 items, 4 authorized — accumulates 2 more and stops at limit
				{batch: items("11", "12", "13", "14", "15", "16", "17", "18", "19", "20"), nextCursor: "c2"},
			},
			allowedIDs: map[string]bool{
				"1": true, "3": true, "5": true, // page 1: 3 authorized
				"11": true, "13": true, "15": true, "17": true, // page 2: 4 authorized (only 2 needed)
			},
			limit:          5,
			wantIDs:        []string{"1", "3", "5", "11", "13"},
			wantNextCursor: "c2",
			wantListCalls:  2,
		},
		{
			name: "partial_auth_stops_at_limit_mid_batch",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6"), nextCursor: "c1"},
			},
			allowedIDs: map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true, "6": true},
			limit:      3,
			// stops at limit mid-batch, no second page needed
			wantIDs:        []string{"1", "2", "3"},
			wantNextCursor: "c1",
			wantListCalls:  1,
		},
		{
			name: "single_item_at_limit",
			pages: []listPage{
				{batch: items("1", "2"), nextCursor: ""},
			},
			allowedIDs:    map[string]bool{"2": true},
			limit:         1,
			wantIDs:       []string{"2"},
			wantListCalls: 1,
		},
		{
			name: "exactly_fills_limit_across_pages",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6"), nextCursor: "c1"},
				{batch: items("7", "8", "9", "10", "11", "12"), nextCursor: ""},
			},
			// page 1: only 2 authorized; page 2: 1 authorized → total 3 = limit
			allowedIDs: map[string]bool{
				"2": true, "5": true,
				"9": true,
			},
			limit:          3,
			wantIDs:        []string{"2", "5", "9"},
			wantNextCursor: "",
			wantListCalls:  2,
		},
		{
			name: "platform_admin_all_allowed",
			pages: []listPage{
				{batch: items("1", "2", "3"), nextCursor: ""},
			},
			allowedIDs:      map[string]bool{}, // would block everything without admin
			isPlatformAdmin: true,
			limit:           5,
			wantIDs:         []string{"1", "2", "3"},
			wantListCalls:   1,
		},
		{
			name: "platform_admin_stops_at_limit",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6"), nextCursor: "c1"},
			},
			allowedIDs:      map[string]bool{},
			isPlatformAdmin: true,
			limit:           3,
			wantIDs:         []string{"1", "2", "3"},
			// pagination from the (only) list call is returned, even though we stopped at limit
			wantNextCursor: "c1",
			wantListCalls:  1,
		},
		{
			name: "zero_limit_defaults_to_25",
			pages: []listPage{
				// 12 items < 50 (default overfetch for limit=25) — exhausted
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"), nextCursor: ""},
			},
			allowedIDs: func() map[string]bool {
				m := map[string]bool{}
				for _, id := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"} {
					m[id] = true
				}
				return m
			}(),
			limit:         0, // triggers default=25
			wantIDs:       []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"},
			wantListCalls: 1,
		},
		{
			name: "initial_cursor_passed_to_first_call",
			pages: []listPage{
				{batch: items("1", "2", "3"), nextCursor: ""},
			},
			allowedIDs:  map[string]bool{"1": true, "2": true, "3": true},
			limit:       5,
			startCursor: "resume_from_here",
			wantIDs:     []string{"1", "2", "3"},
			wantListCalls: 1,
		},
		{
			name: "cursor_threads_through_pages",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: "after_10"},
				{batch: items("11", "12", "13", "14", "15", "16", "17", "18", "19", "20"), nextCursor: ""},
			},
			allowedIDs: map[string]bool{
				"2": true, "4": true,          // 2 from page 1
				"12": true, "14": true, "16": true, // 3 from page 2
			},
			limit:          5,
			wantIDs:        []string{"2", "4", "12", "14", "16"},
			wantNextCursor: "",
			wantListCalls:  2,
		},
		{
			name: "returns_last_pagination_even_when_stopped_at_limit",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6"), nextCursor: "cursor_from_store"},
			},
			allowedIDs: map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true, "6": true},
			limit:      3,
			// stops at limit — but pagination from the store call is still returned
			wantNextCursor: "cursor_from_store",
			wantIDs:        []string{"1", "2", "3"},
			wantListCalls:  1,
		},
		{
			name: "list_error_on_first_call",
			pages: []listPage{
				{err: errors.New("db timeout")},
			},
			allowedIDs:    map[string]bool{},
			limit:         5,
			wantErr:       true,
			wantListCalls: 1,
		},
		{
			name: "list_error_on_second_call",
			pages: []listPage{
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: "c1"},
				{err: errors.New("connection reset")},
			},
			allowedIDs:    map[string]bool{"1": true},
			limit:         5,
			wantErr:       true,
			wantListCalls: 2,
		},
		{
			name: "auth_error",
			pages: []listPage{
				{batch: items("1", "2", "3"), nextCursor: ""},
			},
			allowedIDs: map[string]bool{},
			batchErr:   errors.New("authorizer unavailable"),
			limit:      5,
			wantErr:    true,
		},
		{
			name: "three_pages_needed_sparse_authorization",
			pages: []listPage{
				// overfetch=4 for limit=2
				{batch: items("a", "b", "c", "d"), nextCursor: "c1"},
				{batch: items("e", "f", "g", "h"), nextCursor: "c2"},
				{batch: items("i", "j", "k", "l"), nextCursor: ""},
			},
			allowedIDs: map[string]bool{
				"b": true,  // page 1: 1 authorized
				"g": true,  // page 2: 1 authorized (total: 2 = limit — stops)
			},
			limit:          2,
			wantIDs:        []string{"b", "g"},
			wantNextCursor: "c2",
			wantListCalls:  2,
		},
		{
			name: "large_batch_one_authorized",
			pages: []listPage{
				{batch: items("a", "b", "c", "d", "e", "f"), nextCursor: ""},
			},
			allowedIDs:    map[string]bool{"e": true},
			limit:         5,
			wantIDs:       []string{"e"},
			wantListCalls: 1,
		},
		{
			name: "overfetch_equals_batch_size_but_cursor_empty",
			pages: []listPage{
				// batch size == overfetch(10), but cursor is "" — must stop
				{batch: items("1", "2", "3", "4", "5", "6", "7", "8", "9", "10"), nextCursor: ""},
			},
			allowedIDs:    map[string]bool{"1": true},
			limit:         5,
			wantIDs:       []string{"1"},
			wantListCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var listCalls []fop.PageStringCursor
			listFn := makeList(tc.pages, &listCalls)
			authorizer := makeAuthorizer(tc.allowedIDs, tc.isPlatformAdmin, tc.batchErr)

			got, pagination, err := bridfop.PostfilterLoop(
				context.Background(),
				authorizer,
				subject,
				"read",
				"item",
				getID,
				listFn,
				fop.PageStringCursor{Limit: tc.limit, Cursor: tc.startCursor},
			)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify returned IDs.
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("got %d records, want %d: %v", len(got), len(tc.wantIDs), gotIDs(got))
			}
			for i, rec := range got {
				if rec.id != tc.wantIDs[i] {
					t.Errorf("record[%d] id = %q, want %q", i, rec.id, tc.wantIDs[i])
				}
			}

			// Verify pagination cursor.
			if pagination.NextCursor != tc.wantNextCursor {
				t.Errorf("NextCursor = %q, want %q", pagination.NextCursor, tc.wantNextCursor)
			}

			// Verify list call count.
			if len(listCalls) != tc.wantListCalls {
				t.Errorf("list called %d times, want %d", len(listCalls), tc.wantListCalls)
			}
		})
	}
}

// TestPostfilterLoop_CursorThreading verifies that cursors flow correctly:
// the initial cursor is passed to the first call, and each page's NextCursor
// is passed to the subsequent call.
//
// Setup: limit=3, overfetch=6. Each page returns exactly 6 items (= overfetch,
// so the loop does not stop on exhaustion). Authorization is sparse (1 per page)
// so all 3 pages are needed to accumulate 3 items.
func TestPostfilterLoop_CursorThreading(t *testing.T) {
	var listCalls []fop.PageStringCursor

	pages := []listPage{
		// Each page has exactly 6 items (= overfetch) so the loop continues.
		{batch: items("a1", "a2", "a3", "a4", "a5", "a6"), nextCursor: "page2_cursor"},
		{batch: items("b1", "b2", "b3", "b4", "b5", "b6"), nextCursor: "page3_cursor"},
		{batch: items("c1", "c2", "c3", "c4", "c5", "c6"), nextCursor: ""},
	}

	// Only 1 item authorized per page — forces all 3 pages to fill limit=3.
	authorizer := makeAuthorizer(map[string]bool{
		"a2": true,
		"b3": true,
		"c4": true,
	}, false, nil)

	got, _, err := bridfop.PostfilterLoop(
		context.Background(),
		authorizer,
		authorization.Subject{Type: "user", ID: "u1"},
		"read", "item",
		getID,
		makeList(pages, &listCalls),
		fop.PageStringCursor{Limit: 3, Cursor: "initial_cursor"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(listCalls) != 3 {
		t.Fatalf("expected 3 list calls, got %d", len(listCalls))
	}
	if listCalls[0].Cursor != "initial_cursor" {
		t.Errorf("call[0] cursor = %q, want %q", listCalls[0].Cursor, "initial_cursor")
	}
	if listCalls[1].Cursor != "page2_cursor" {
		t.Errorf("call[1] cursor = %q, want %q", listCalls[1].Cursor, "page2_cursor")
	}
	if listCalls[2].Cursor != "page3_cursor" {
		t.Errorf("call[2] cursor = %q, want %q", listCalls[2].Cursor, "page3_cursor")
	}

	wantIDs := []string{"a2", "b3", "c4"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d records, want %d", len(got), len(wantIDs))
	}
	for i, rec := range got {
		if rec.id != wantIDs[i] {
			t.Errorf("record[%d] = %q, want %q", i, rec.id, wantIDs[i])
		}
	}
}

// TestPostfilterLoop_OverfetchSize verifies that each list call uses 2× the
// requested limit as its page size.
func TestPostfilterLoop_OverfetchSize(t *testing.T) {
	var listCalls []fop.PageStringCursor

	pages := []listPage{
		{batch: items("1", "2"), nextCursor: ""},
	}
	authorizer := makeAuthorizer(map[string]bool{"1": true, "2": true}, false, nil)

	bridfop.PostfilterLoop(
		context.Background(),
		authorizer,
		authorization.Subject{Type: "user", ID: "u1"},
		"read", "item",
		getID,
		makeList(pages, &listCalls),
		fop.PageStringCursor{Limit: 7},
	)

	if len(listCalls) != 1 {
		t.Fatalf("expected 1 list call, got %d", len(listCalls))
	}
	if listCalls[0].Limit != 14 {
		t.Errorf("list called with Limit=%d, want 14 (2× requested limit of 7)", listCalls[0].Limit)
	}
}

// TestPostfilterLoop_ZeroLimitOverfetch verifies that a zero limit defaults to
// 25 and therefore uses an overfetch of 50.
func TestPostfilterLoop_ZeroLimitOverfetch(t *testing.T) {
	var listCalls []fop.PageStringCursor

	pages := []listPage{{batch: nil, nextCursor: ""}}
	authorizer := makeAuthorizer(map[string]bool{}, false, nil)

	bridfop.PostfilterLoop(
		context.Background(),
		authorizer,
		authorization.Subject{Type: "user", ID: "u1"},
		"read", "item",
		getID,
		makeList(pages, &listCalls),
		fop.PageStringCursor{Limit: 0},
	)

	if len(listCalls) != 1 {
		t.Fatalf("expected 1 list call, got %d", len(listCalls))
	}
	if listCalls[0].Limit != 50 {
		t.Errorf("list called with Limit=%d, want 50 (2× default limit of 25)", listCalls[0].Limit)
	}
}

// TestPostfilterLoop_NoAuthCallOnEmptyBatch verifies that FilterAuthorized is
// never called when the list function returns an empty batch.
func TestPostfilterLoop_NoAuthCallOnEmptyBatch(t *testing.T) {
	authCallCount := 0
	pages := []listPage{{batch: nil, nextCursor: ""}}

	// Use a storer that tracks calls.
	trackingStore := &mockStorer{
		allowedIDs: map[string]bool{},
		onBatchCheck: func(ids []string) {
			authCallCount += len(ids)
		},
	}
	schema := authorization.Schema{
		ResourceTypes: map[string]authorization.ResourceTypeDef{
			"item": {Permissions: map[string]authorization.PermissionRule{
				"read": authorization.AnyOf(authorization.Direct("reader")),
			}},
		},
	}
	authorizer := authorization.NewAuthorizer(trackingStore, schema, authorization.Config{})

	bridfop.PostfilterLoop(
		context.Background(),
		authorizer,
		authorization.Subject{Type: "user", ID: "u1"},
		"read", "item",
		getID,
		makeList(pages, nil),
		fop.PageStringCursor{Limit: 5},
	)

	if authCallCount != 0 {
		t.Errorf("expected 0 auth checks on empty batch, got %d", authCallCount)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func gotIDs(recs []item) []string {
	ids := make([]string, len(recs))
	for i, r := range recs {
		ids[i] = r.id
	}
	return ids
}

// =============================================================================
// Mock Storer
// =============================================================================

// mockStorer is a minimal authorization.Storer for testing.
// It controls platform admin status and per-ID authorization via CheckBatchDirect.
type mockStorer struct {
	allowedIDs      map[string]bool
	isPlatformAdmin bool
	batchErr        error
	onBatchCheck    func(ids []string) // optional observer
}

func (m *mockStorer) CheckRelationExists(_ context.Context, resourceType, resourceID, relation, _, _ string) (bool, error) {
	if resourceType == "platform" && resourceID == "main" && relation == "admin" {
		return m.isPlatformAdmin, nil
	}
	return false, nil
}

func (m *mockStorer) CheckBatchDirect(_ context.Context, _ string, resourceIDs []string, _, _, _ string) (map[string]bool, error) {
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	if m.onBatchCheck != nil {
		m.onBatchCheck(resourceIDs)
	}
	result := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		result[id] = m.allowedIDs[id]
	}
	return result, nil
}

// Stubs for unused interface methods.
func (m *mockStorer) CheckRelationWithGroupExpansion(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (m *mockStorer) GetRelationTargets(_ context.Context, _, _, _ string) ([]authorization.RelationTarget, error) {
	return nil, nil
}
func (m *mockStorer) CreateRelationships(_ context.Context, _ []authorization.CreateRelationship) error {
	return nil
}
func (m *mockStorer) DeleteResourceRelationships(_ context.Context, _, _ string) error { return nil }
func (m *mockStorer) DeleteRelationship(_ context.Context, _, _, _, _, _ string) error { return nil }
func (m *mockStorer) DeleteByResourceAndSubject(_ context.Context, _, _, _, _ string) error       { return nil }
func (m *mockStorer) CountByResourceAndRelation(_ context.Context, _, _, _ string) (int, error) {
	return 0, nil
}
func (m *mockStorer) ListRelationshipsBySubject(_ context.Context, _, _ string, _ authorization.SubjectRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.SubjectRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, nil
}
func (m *mockStorer) ListRelationshipsByResource(_ context.Context, _, _ string, _ authorization.ResourceRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.ResourceRelationship, fop.Pagination, error) {
	return nil, fop.Pagination{}, nil
}
func (m *mockStorer) LookupResourceIDs(_ context.Context, _ string, _ []string, _, _ string) ([]string, error) {
	return nil, nil
}
func (m *mockStorer) LookupResourceIDsByRelationTarget(_ context.Context, _, _, _ string, _ []string) ([]string, error) {
	return nil, nil
}
