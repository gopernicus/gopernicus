package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The filtered API-key listing conformance cases (SOC 2 S4d). The capability
// is OPTIONAL (apikey.FilteredLister on Repositories.APIKeys), so the group
// skips LOUDLY when the store does not implement it.

// runAPIKeyFilter registers the apikey.FilteredLister group.
func runAPIKeyFilter(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()
	t.Run("APIKeyFilter", func(t *testing.T) {
		repos := newRepos(t)
		if _, ok := repos.APIKeys.(apikey.FilteredLister); !ok || repos.APIKeys == nil {
			t.Skip("apikey.FilteredLister not implemented — the filtered key listing is NOT verified for this Repositories")
		}
		t.Run("StatusAndNeverExpires", func(t *testing.T) { testAPIKeyFilterMatrix(t, newRepos(t)) })
		t.Run("Paginates", func(t *testing.T) { testAPIKeyFilterPaging(t, newRepos(t)) })
	})
}

// filterNow is the instant the filter cases judge expiry at; whole seconds, so
// every dialect stores the boundary exactly.
var filterNow = suiteBase.Add(30 * 24 * time.Hour)

// seedFilterKeys creates one key of each shape on sa-filter, plus a bystander
// on another account, and returns their ids by name.
func seedFilterKeys(t *testing.T, repos auth.Repositories) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	mk := func(name string, expiresAt time.Time, revoked bool, offset time.Duration) {
		k := mustCreateAPIKey(t, repos.APIKeys, "sa-filter", name, "hash-filter-"+name, expiresAt, suiteBase.Add(offset))
		if revoked {
			if err := repos.APIKeys.Revoke(ctx, k.ID, suiteBase.Add(time.Hour)); err != nil {
				t.Fatalf("Revoke(%s): %v", name, err)
			}
		}
		out[name] = k.ID
	}
	mk("active-never", time.Time{}, false, time.Second)
	mk("active-future", filterNow.Add(time.Second), false, 2*time.Second)
	// Expiry is at-or-before: a key expiring exactly at filterNow is expired.
	mk("expired-boundary", filterNow, false, 3*time.Second)
	mk("expired-past", filterNow.Add(-time.Hour), false, 4*time.Second)
	mk("revoked-never", time.Time{}, true, 5*time.Second)
	// Revoked takes precedence over expired.
	mk("revoked-expired", filterNow.Add(-time.Hour), true, 6*time.Second)
	mustCreateAPIKey(t, repos.APIKeys, "sa-other", "bystander", "hash-filter-bystander", time.Time{}, suiteBase)
	return out
}

func filteredNames(t *testing.T, repos auth.Repositories, ids map[string]string, f apikey.Filter) []string {
	t.Helper()
	page, err := repos.APIKeys.(apikey.FilteredLister).ListByServiceAccountFiltered(context.Background(), "sa-filter", f, listing.Request{Limit: 50})
	if err != nil {
		t.Fatalf("ListByServiceAccountFiltered(%+v): %v", f, err)
	}
	byID := map[string]string{}
	for name, id := range ids {
		byID[id] = name
	}
	var names []string
	for _, k := range page.Items {
		name, ok := byID[k.ID]
		if !ok {
			t.Fatalf("filter %+v returned a key of another account: %+v", f, k)
		}
		if !f.Matches(k) {
			t.Errorf("filter %+v returned %s, which its predicate refuses", f, name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func testAPIKeyFilterMatrix(t *testing.T, repos auth.Repositories) {
	ids := seedFilterKeys(t, repos)
	for _, c := range []struct {
		filter apikey.Filter
		want   []string
	}{
		{apikey.Filter{Now: filterNow}, []string{"active-future", "active-never", "expired-boundary", "expired-past", "revoked-expired", "revoked-never"}},
		{apikey.Filter{Status: apikey.StatusActive, Now: filterNow}, []string{"active-future", "active-never"}},
		{apikey.Filter{Status: apikey.StatusExpired, Now: filterNow}, []string{"expired-boundary", "expired-past"}},
		{apikey.Filter{Status: apikey.StatusRevoked, Now: filterNow}, []string{"revoked-expired", "revoked-never"}},
		{apikey.Filter{NeverExpires: true, Now: filterNow}, []string{"active-never", "revoked-never"}},
		{apikey.Filter{Status: apikey.StatusActive, NeverExpires: true, Now: filterNow}, []string{"active-never"}},
		{apikey.Filter{Status: apikey.StatusRevoked, NeverExpires: true, Now: filterNow}, []string{"revoked-never"}},
	} {
		if got := filteredNames(t, repos, ids, c.filter); !slices.Equal(got, c.want) {
			t.Errorf("filter %+v = %v, want %v", c.filter, got, c.want)
		}
	}
}

// testAPIKeyFilterPaging pages a filtered listing one key at a time in the
// pinned created_at DESC, id DESC order.
func testAPIKeyFilterPaging(t *testing.T, repos auth.Repositories) {
	ids := seedFilterKeys(t, repos)
	lister := repos.APIKeys.(apikey.FilteredLister)
	f := apikey.Filter{Status: apikey.StatusExpired, Now: filterNow}
	var got []string
	cursor := ""
	for range 5 {
		page, err := lister.ListByServiceAccountFiltered(context.Background(), "sa-filter", f, listing.Request{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, k := range page.Items {
			got = append(got, k.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if want := []string{ids["expired-past"], ids["expired-boundary"]}; !slices.Equal(got, want) {
		t.Fatalf("paged %v, want %v (newest first)", got, want)
	}
}
