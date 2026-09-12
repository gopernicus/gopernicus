//go:build integration && !live

package firestore

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The audit-rail cases beyond the conformance suite's five-row ListFilters
// table: EVERY equality subset of the filter (the composite matrix SCHEMA.md
// §7.3 enumerates, one query shape per subset), and the half-open time window
// asserted at MICROSECOND resolution, which is where a store that truncates,
// rounds, or flips an inclusive bound is caught.

// listEvents pages a filter to exhaustion and returns the ids in page order.
func listEvents(t *testing.T, r auth.Repositories, filter securityevent.ListFilter, limit int) []string {
	t.Helper()
	ctx := context.Background()

	var ids []string
	cursor := ""
	for i := 0; i < 50; i++ {
		page, err := r.SecurityEvents.List(ctx, filter, list.Request{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("List(%+v): %v", filter, err)
		}
		for _, evt := range page.Items {
			ids = append(ids, evt.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("List(%+v) did not terminate", filter)
	return nil
}

// appendEvent writes one event through the port.
func appendEvent(t *testing.T, r auth.Repositories, userID, eventType, status string, at time.Time) securityevent.SecurityEvent {
	t.Helper()
	evt := securityevent.New(dbGenerated, eventType, status, at)
	evt.UserID = userID
	created, err := r.SecurityEvents.Create(context.Background(), evt)
	if err != nil {
		t.Fatalf("SecurityEvents.Create: %v", err)
	}
	return created
}

// TestSecurityEventFilterSubsets drives ALL EIGHT equality subsets of
// (user_id, event_type, event_status) against one seeded population, with
// securityevent.ListFilter.Match — the port's own reference predicate — as the
// oracle. Each subset is a distinct Firestore query shape and therefore a
// distinct composite index in production; a subset the adapter composed wrongly
// (an equality against "" for an unset dimension, say) returns a page the oracle
// disagrees with.
func TestSecurityEventFilterSubsets(t *testing.T) {
	r, _ := openRepos(t)

	// A population that separates every dimension: two users × two types × two
	// statuses, each at a distinct instant so the expected order is total.
	var all []securityevent.SecurityEvent
	at := testBase
	for _, userID := range []string{"ua", "ub"} {
		for _, eventType := range []string{securityevent.TypeLogin, securityevent.TypeLogout} {
			for _, status := range []string{securityevent.StatusSuccess, securityevent.StatusFailure} {
				all = append(all, appendEvent(t, r, userID, eventType, status, at))
				at = at.Add(time.Minute)
			}
		}
	}

	subsets := []securityevent.ListFilter{
		{},
		{UserID: "ua"},
		{EventType: securityevent.TypeLogin},
		{EventStatus: securityevent.StatusFailure},
		{UserID: "ua", EventType: securityevent.TypeLogin},
		{UserID: "ua", EventStatus: securityevent.StatusFailure},
		{EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusFailure},
		{UserID: "ua", EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusFailure},
	}

	for _, filter := range subsets {
		t.Run(fmt.Sprintf("user=%q/type=%q/status=%q", filter.UserID, filter.EventType, filter.EventStatus), func(t *testing.T) {
			want := expectedEventIDs(all, filter)
			// Paged two at a time, so the subset's composite is exercised by
			// the cursor and its reverse probe as well as by the first page.
			if got := listEvents(t, r, filter, 2); !equalEventIDs(got, want) {
				t.Errorf("filter %+v = %v, want %v (the ListFilter.Match oracle)", filter, got, want)
			}
		})
	}
}

// TestSecurityEventTimeWindowIsHalfOpenAtMicrosecond pins the two bounds at the
// finest resolution the storage layer keeps. Firestore stores microseconds and
// the connector truncates to them on write, so three events one MICROSECOND
// apart are three distinct instants — and the port's contract is that Since is
// INCLUSIVE and Until is EXCLUSIVE on exactly those values.
//
// The conformance suite's time_window case uses minute-scale gaps, which a store
// that compared with the wrong strictness could still pass by rounding. This one
// cannot be passed by luck: every bound is placed exactly ON a stored value.
func TestSecurityEventTimeWindowIsHalfOpenAtMicrosecond(t *testing.T) {
	r, _ := openRepos(t)

	t0 := testBase
	t1 := testBase.Add(time.Microsecond)
	t2 := testBase.Add(2 * time.Microsecond)

	e0 := appendEvent(t, r, "u-window", securityevent.TypeLogin, securityevent.StatusSuccess, t0)
	e1 := appendEvent(t, r, "u-window", securityevent.TypeLogin, securityevent.StatusSuccess, t1)
	e2 := appendEvent(t, r, "u-window", securityevent.TypeLogin, securityevent.StatusSuccess, t2)

	// The stored instants must be the ones asked for: a store that dropped
	// sub-millisecond precision would make every assertion below vacuous.
	for _, evt := range []securityevent.SecurityEvent{e0, e1, e2} {
		if evt.CreatedAt.Nanosecond()%1000 != 0 {
			t.Fatalf("stored created_at %v is not microsecond-truncated", evt.CreatedAt)
		}
	}
	if !e1.CreatedAt.After(e0.CreatedAt) || !e2.CreatedAt.After(e1.CreatedAt) {
		t.Fatalf("the three events collapsed to the same instant: %v %v %v", e0.CreatedAt, e1.CreatedAt, e2.CreatedAt)
	}

	cases := []struct {
		name   string
		filter securityevent.ListFilter
		want   []string
	}{
		{"since_is_inclusive", securityevent.ListFilter{UserID: "u-window", Since: t1}, []string{e2.ID, e1.ID}},
		{"until_is_exclusive", securityevent.ListFilter{UserID: "u-window", Until: t2}, []string{e1.ID, e0.ID}},
		{"half_open_window_is_one_event", securityevent.ListFilter{UserID: "u-window", Since: t1, Until: t2}, []string{e1.ID}},
		{"empty_window", securityevent.ListFilter{UserID: "u-window", Since: t1, Until: t1}, nil},
		{"whole_window", securityevent.ListFilter{UserID: "u-window", Since: t0, Until: t2.Add(time.Microsecond)}, []string{e2.ID, e1.ID, e0.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := listEvents(t, r, tc.filter, 10); !equalEventIDs(got, tc.want) {
				t.Errorf("filter %+v = %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

// expectedEventIDs is the oracle: the seeded events the port's own Match
// accepts, in the contractual created_at DESC, id DESC order.
func expectedEventIDs(all []securityevent.SecurityEvent, filter securityevent.ListFilter) []string {
	matched := make([]securityevent.SecurityEvent, 0, len(all))
	for _, evt := range all {
		if filter.Match(evt) {
			matched = append(matched, evt)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID > matched[j].ID
	})
	ids := make([]string, len(matched))
	for i, evt := range matched {
		ids[i] = evt.ID
	}
	return ids
}

func equalEventIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
