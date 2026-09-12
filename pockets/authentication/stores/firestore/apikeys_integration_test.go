//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The API-key cases the shared conformance suite cannot reach: the HASH CLAIM
// itself (no port returns one, and its lifecycle is what reproduces the SQL
// unique index), and the two search-paging shapes N4a names beyond the
// storetest search group — an offset that counts MATCHES rather than scanned
// documents, and a FULL previous-page window under a search.

// createKey mints one key under a parent through the port.
func createKey(t *testing.T, r auth.Repositories, saID, name, hash string, createdAt time.Time) apikey.APIKey {
	t.Helper()
	k, err := apikey.New(dbGenerated, saID, name, "prefix", hash, time.Time{}, createdAt)
	if err != nil {
		t.Fatalf("apikey.New(%q): %v", name, err)
	}
	created, err := r.APIKeys.Create(context.Background(), k)
	if err != nil {
		t.Fatalf("APIKeys.Create(%q): %v", name, err)
	}
	return created
}

// hashClaimOwner reports which key holds the claim on a hash, if any.
func hashClaimOwner(t *testing.T, db *firestoredb.DB, hash string) (apiKeyHashClaimDoc, bool) {
	t.Helper()
	var claim apiKeyHashClaimDoc
	if !readClaim(t, db, apiKeyHashClaimRef(db, hash), &claim) {
		return apiKeyHashClaimDoc{}, false
	}
	return claim, true
}

// TestAPIKeyHashClaimSurvivesRevocation is the predicate SCHEMA.md §5.4 pins,
// asserted where it is actually enforced. `idx_api_keys_key_hash` is an
// UNCONDITIONAL unique index — there is no `WHERE revoked_at IS NULL` — so a
// revoked key still occupies its hash in SQL and must still occupy it here.
//
// The conformance suite cannot see this: it never re-mints a revoked key's hash,
// and GetByHash returning the revoked record proves the ROW survived, not the
// claim. A store that released the claim on revocation would pass every port
// case and let a revoked credential's hash be minted again.
func TestAPIKeyHashClaimSurvivesRevocation(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	key := createKey(t, r, "sa-1", "revoked-later", "hash-revoke", testBase)

	claim, ok := hashClaimOwner(t, db, "hash-revoke")
	if !ok || claim.APIKeyID != key.ID {
		t.Fatalf("claim after create = %+v (present=%v), want the key %s", claim, ok, key.ID)
	}

	if err := r.APIKeys.Revoke(ctx, key.ID, testBase.Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	claim, ok = hashClaimOwner(t, db, "hash-revoke")
	if !ok || claim.APIKeyID != key.ID {
		t.Errorf("claim after revoke = %+v (present=%v), want it RETAINED for %s — the SQL index is unconditional", claim, ok, key.ID)
	}

	// The observable consequence: the hash is not re-mintable.
	dup, err := apikey.New(dbGenerated, "sa-1", "re-mint", "prefix", "hash-revoke", time.Time{}, testBase.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("apikey.New: %v", err)
	}
	if _, err := r.APIKeys.Create(ctx, dup); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("re-minting a revoked key's hash: err=%v, want ErrAlreadyExists", err)
	}

	// And the revoked record is still resolvable, which is the port's own rule.
	got, err := r.APIKeys.GetByHash(ctx, "hash-revoke")
	if err != nil || !got.Revoked() {
		t.Errorf("GetByHash after revoke = %+v err=%v, want the revoked record", got, err)
	}
}

// TestAPIKeyCreateRollsBackOnLostHashClaim is ruling R3's arbitration, asserted
// on the STATE rather than only on the error: the losing Create must leave
// NOTHING behind, because its row and its claim are one transaction. A store
// that wrote the row first and then failed the claim would return
// ErrAlreadyExists AND leave an orphan key that GetByHash can never resolve.
func TestAPIKeyCreateRollsBackOnLostHashClaim(t *testing.T) {
	r, db := openRepos(t)
	ctx := context.Background()

	winner := createKey(t, r, "sa-1", "winner", "hash-contended", testBase)

	loser, err := apikey.New(dbGenerated, "sa-1", "loser", "prefix", "hash-contended", time.Time{}, testBase.Add(time.Minute))
	if err != nil {
		t.Fatalf("apikey.New: %v", err)
	}
	if _, err := r.APIKeys.Create(ctx, loser); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding Create: err=%v, want ErrAlreadyExists", err)
	}

	page, err := r.APIKeys.ListByServiceAccount(ctx, "sa-1", list.Request{Limit: 50})
	if err != nil {
		t.Fatalf("ListByServiceAccount: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != winner.ID {
		t.Errorf("after the lost race the parent holds %d keys (%+v), want only the winner %s", len(page.Items), page.Items, winner.ID)
	}

	claim, ok := hashClaimOwner(t, db, "hash-contended")
	if !ok || claim.APIKeyID != winner.ID {
		t.Errorf("claim = %+v (present=%v), want the winner %s", claim, ok, winner.ID)
	}
}

// seedSearchPaging seeds one parent with an ALTERNATING population: five
// matching names interleaved with four that do not match, newest first in the
// default created_at DESC order. The interleaving is the point — a store that
// applies its offset to SCANNED documents instead of MATCHES lands on a
// different row for every offset above zero.
func seedSearchPaging(t *testing.T, r auth.Repositories) []string {
	t.Helper()
	var matches []string
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("filler-%d", i)
		if i%2 == 0 {
			name = fmt.Sprintf("target-%d", i)
		}
		// Descending created_at: the newest row is written last with the
		// largest offset, so the default page order is target-8, filler-7, …
		k := createKey(t, r, "sa-paging", name, fmt.Sprintf("paging-hash-%d", i), testBase.Add(time.Duration(i)*time.Minute))
		if i%2 == 0 {
			matches = append([]string{k.ID}, matches...)
		}
	}
	return matches
}

// TestSearchOffsetSkipsMatchesNotScannedRows is the filtered-offset case N4a
// names. The offset counts MATCHES: skipping two rows of a searched list must
// land on the third MATCH, not on whatever document happens to be third in the
// parent's population.
func TestSearchOffsetSkipsMatchesNotScannedRows(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	matches := seedSearchPaging(t, r)

	for offset := 0; offset < len(matches); offset++ {
		page, err := r.APIKeys.ListByServiceAccount(ctx, "sa-paging", list.Request{
			Limit:    1,
			Strategy: list.StrategyOffset,
			Offset:   offset,
			Search:   "target",
		})
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("offset %d returned %d items, want 1", offset, len(page.Items))
		}
		if page.Items[0].ID != matches[offset] {
			t.Errorf("offset %d = %s (%q), want the match at that position %s",
				offset, page.Items[0].ID, page.Items[0].Name, matches[offset])
		}
		if want := offset < len(matches)-1; page.HasMore != want {
			t.Errorf("offset %d HasMore = %v, want %v", offset, page.HasMore, want)
		}
	}

	// Past the last MATCH the page is empty, even though unmatched documents
	// remain in the parent.
	page, err := r.APIKeys.ListByServiceAccount(ctx, "sa-paging", list.Request{
		Limit: 1, Strategy: list.StrategyOffset, Offset: len(matches), Search: "target",
	})
	if err != nil {
		t.Fatalf("offset past the end: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("offset past the last match returned %+v, want an empty page", page.Items)
	}
}

// TestSearchPreviousPageWindowIsFullUnderSearch is the other paging shape N4a
// names: a FULL previous-page window under a search. The storetest search group
// only reaches a PARTIAL prior window (its second page's previous is the first
// page, so PreviousCursor is deliberately empty). Here the third page has a full
// window of matches behind it, so PreviousCursor must be populated AND must
// address exactly page two — which is only true if the reverse probe page-fills
// with the SAME predicate the forward query used.
func TestSearchPreviousPageWindowIsFullUnderSearch(t *testing.T) {
	r, _ := openRepos(t)
	ctx := context.Background()

	matches := seedSearchPaging(t, r)
	if len(matches) < 5 {
		t.Fatalf("this case needs at least 5 matching rows, the seed has %d", len(matches))
	}

	pageOf := func(cursor string) list.Page[apikey.APIKey] {
		t.Helper()
		page, err := r.APIKeys.ListByServiceAccount(ctx, "sa-paging", list.Request{
			Limit: 2, Cursor: cursor, Search: "target",
		})
		if err != nil {
			t.Fatalf("page(cursor=%q): %v", cursor, err)
		}
		return page
	}
	idsOf := func(page list.Page[apikey.APIKey]) []string {
		out := make([]string, 0, len(page.Items))
		for _, k := range page.Items {
			out = append(out, k.ID)
		}
		return out
	}

	first := pageOf("")
	if got := idsOf(first); len(got) != 2 || got[0] != matches[0] || got[1] != matches[1] {
		t.Fatalf("page1 = %v, want %v", got, matches[0:2])
	}
	second := pageOf(first.NextCursor)
	if got := idsOf(second); len(got) != 2 || got[0] != matches[2] || got[1] != matches[3] {
		t.Fatalf("page2 = %v, want %v", got, matches[2:4])
	}

	third := pageOf(second.NextCursor)
	if !third.HasPrev {
		t.Fatal("page3 HasPrev = false; the reverse probe lost the search predicate")
	}
	if third.PreviousCursor == "" {
		t.Fatal("page3 PreviousCursor is empty; a FULL prior window of matches must be addressable")
	}
	if got := idsOf(third); len(got) != 1 || got[0] != matches[4] {
		t.Fatalf("page3 = %v, want %v", got, matches[4:5])
	}

	back := pageOf(third.PreviousCursor)
	if got := idsOf(back); len(got) != 2 || got[0] != matches[2] || got[1] != matches[3] {
		t.Errorf("previous of page3 = %v, want page2 %v", got, matches[2:4])
	}
}
