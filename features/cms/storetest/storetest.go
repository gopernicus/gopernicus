// Package storetest is the conformance suite for the CMS feature's repository
// ports: every store that fills a cms.Repositories — the in-package reference
// implementation, a host memstore (examples/minimal), and each dialect adapter
// (features/cms/stores/turso, .../postgres) — should pass Run against a freshly
// wired, isolated Repositories. Modeled on sdk/capabilities/cacher/cachertest's
// Run(t, newImpl) pattern, scaled to a repository set so cross-table behavior
// (entry↔term association, cascade on entry delete) is exercised, not ports in
// isolation.
//
// The port doc comments are the spec; this suite is their executable form. It
// imports stdlib + sdk + the cms feature's own packages only (guard G2 forbids
// a driver import here), so features/cms's own `go test ./...` runs it against
// the reference implementation (see reference_test.go).
package storetest

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// ids is the suite's entity-ID generator: the default nanoid strategy, matching
// the feature's zero-value Config.IDs.
var ids = cryptids.IDGenerator{}

// dbIDs is the cryptids.Database strategy: entities reach Create with an empty
// ID and the store must assign the database-generated key (amended D10).
var dbIDs = cryptids.NewGenerator(cryptids.Database)

// suiteBase is the reference instant the suite stamps its rows from. Kept
// microsecond-aligned so the precision case (below) controls the sub-microsecond
// digits explicitly rather than inheriting them from the wall clock.
var suiteBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// orderField is the keyset order column entries are paginated by; it must match
// the cursor's order field so a stale cursor from a different sort is ignored.
const orderField = "created_at"

// Run exercises the cms.Repositories contract against a clean, isolated set
// obtained from newRepos for each leaf subtest. newRepos MUST return a CLEAN,
// isolated Repositories per call: SQL-backed harnesses truncate their tables
// (via t.Cleanup and/or up front); memory harnesses return a fresh instance.
func Run(t *testing.T, newRepos func(t *testing.T) cms.Repositories) {
	t.Helper()

	t.Run("Entries", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testEntriesCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testEntriesAbsent(t, newRepos(t)) })
		t.Run("TypeSlugUniqueness", func(t *testing.T) { testEntriesUniqueness(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testEntriesDBGeneratedID(t, newRepos(t)) })
		t.Run("TermAssociationAndCascade", func(t *testing.T) { testEntriesTerms(t, newRepos(t)) })
		t.Run("CursorPagination", func(t *testing.T) { testEntriesPagination(t, newRepos(t)) })
		t.Run("StaleAndEmptyCursor", func(t *testing.T) { testEntriesCursorEdges(t, newRepos(t)) })
		t.Run("TimestampPrecision", func(t *testing.T) { testEntriesPrecision(t, newRepos(t)) })
		t.Run("List", func(t *testing.T) {
			runEntriesPagedFamily(t, newRepos,
				func(repos cms.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[content.Entry], error) {
					return repos.Entries.List(ctx, content.EntryQuery{Type: "article", ListRequest: req})
				},
				seedFamilyEntries,
			)
		})
		t.Run("ByTerm", func(t *testing.T) {
			runEntriesPagedFamily(t, newRepos,
				func(repos cms.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[content.Entry], error) {
					return repos.Entries.ListByTerm(ctx, "term-fam", content.EntryQuery{Type: "article", ListRequest: req})
				},
				seedFamilyEntriesByTerm,
			)
		})
	})

	t.Run("Terms", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testTermsCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testTermsAbsent(t, newRepos(t)) })
		t.Run("KindSlugUniqueness", func(t *testing.T) { testTermsUniqueness(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testTermsDBGeneratedID(t, newRepos(t)) })
		t.Run("ListByKindOrdered", func(t *testing.T) { testTermsListByKind(t, newRepos(t)) })
	})

	t.Run("Menus", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testMenusCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testMenusAbsent(t, newRepos(t)) })
		t.Run("SlugUniqueness", func(t *testing.T) { testMenusUniqueness(t, newRepos(t)) })
		t.Run("Items", func(t *testing.T) { testMenuItems(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testMenusDBGeneratedID(t, newRepos(t)) })
	})

	t.Run("Media", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testMediaCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testMediaAbsent(t, newRepos(t)) })
		t.Run("ListNewestFirst", func(t *testing.T) { testMediaListOrder(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testMediaDBGeneratedID(t, newRepos(t)) })
	})

	t.Run("Inquiries", func(t *testing.T) {
		t.Run("CreateAndListNewestFirst", func(t *testing.T) { testInquiries(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testInquiriesDBGeneratedID(t, newRepos(t)) })
	})
}

// --- helpers ---

// newEntry builds a spine-complete draft entry with the given identity and
// timestamp; callers set Fields/Status/Terms as a case needs. Constructed
// directly (not via content.NewEntry) so the suite controls (id, created_at) —
// the two columns keyset pagination and the precision case turn on.
func newEntry(id, typ, slug, title string, status content.Status, createdAt time.Time) content.Entry {
	if status == "" {
		status = content.StatusDraft
	}
	return content.Entry{
		ID:        id,
		Type:      typ,
		Slug:      slug,
		Title:     title,
		Status:    status,
		Template:  "default",
		Fields:    content.Fields{},
		CreatedAt: createdAt.UTC(),
		UpdatedAt: createdAt.UTC(),
	}
}

// collectEntries walks every page of repo.List(q) with the given limit,
// following NextCursor until HasMore is false. It fails on a duplicate id across
// pages or a page that exceeds the limit, and returns the full traversal in
// order — the raw material for the completeness and ordering assertions.
func collectEntries(t *testing.T, repo content.EntryRepository, q content.EntryQuery, limit int) []content.Entry {
	t.Helper()
	ctx := context.Background()
	q.Limit = limit
	q.Cursor = ""

	var out []content.Entry
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		page, err := repo.List(ctx, q)
		if err != nil {
			t.Fatalf("List(page %d): %v", i, err)
		}
		if len(page.Items) > limit {
			t.Fatalf("page %d returned %d items, exceeds limit %d", i, len(page.Items), limit)
		}
		for _, e := range page.Items {
			if seen[e.ID] {
				t.Errorf("id %q returned on more than one page (skipped/duplicated boundary)", e.ID)
			}
			seen[e.ID] = true
			out = append(out, e)
		}
		if !page.HasMore {
			return out
		}
		if page.NextCursor == "" {
			t.Fatal("List reported HasMore but returned an empty NextCursor")
		}
		q.Cursor = page.NextCursor
	}
	t.Fatal("pagination did not terminate within 1000 pages")
	return nil
}

// assertEntriesDesc verifies items are non-increasing under (created_at, id) —
// the stable order every store promises. It reads the RETURNED created_at values
// (a store may have truncated them to its native precision), so it holds for
// nanosecond stores and microsecond stores alike; it asserts ordering, never
// that a store preserved sub-microsecond fidelity.
func assertEntriesDesc(t *testing.T, items []content.Entry) {
	t.Helper()
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		if prev.CreatedAt.Before(cur.CreatedAt) ||
			(prev.CreatedAt.Equal(cur.CreatedAt) && prev.ID < cur.ID) {
			t.Errorf("entries not in (created_at, id) DESC order at index %d: %q@%s then %q@%s",
				i, prev.ID, prev.CreatedAt, cur.ID, cur.CreatedAt)
		}
	}
}

// assertSameIDs checks the returned set equals want exactly (no skips, no dupes).
func assertSameIDs(t *testing.T, got []content.Entry, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("traversal returned %d entries, want %d", len(got), len(want))
	}
	gotSet := map[string]bool{}
	for _, e := range got {
		gotSet[e.ID] = true
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Errorf("entry %q missing from traversal (skipped)", id)
		}
	}
}

// --- Entries ---

func testEntriesCRUD(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	e := newEntry("e-crud", "article", "first-post", "First Post", content.StatusPublished, suiteBase)
	e.Excerpt = "the excerpt"
	e.Author = "demo"
	e.Body = "the body"
	e.Fields = content.Fields{
		"subtitle": {Kind: content.KindText, Raw: "sub"},
		"rating":   {Kind: content.KindNumber, Raw: "5"},
	}
	if _, err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "e-crud")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "First Post" || got.Excerpt != "the excerpt" || got.Author != "demo" {
		t.Errorf("Get spine mismatch: %+v", got)
	}
	if got.Fields.String("subtitle") != "sub" || got.Fields.Int("rating") != 5 {
		t.Errorf("Get fields mismatch: %+v", got.Fields)
	}

	bySlug, err := repo.GetBySlug(ctx, "article", "first-post")
	if err != nil || bySlug.ID != "e-crud" {
		t.Fatalf("GetBySlug: id=%q err=%v", bySlug.ID, err)
	}

	// Update replaces both spine and the entire custom-field set.
	upd := got
	upd.Excerpt = "edited excerpt"
	upd.Fields = content.Fields{"subtitle": {Kind: content.KindText, Raw: "sub2"}}
	if _, err := repo.Update(ctx, "e-crud", upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reget, err := repo.Get(ctx, "e-crud")
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if reget.Excerpt != "edited excerpt" || reget.Fields.String("subtitle") != "sub2" {
		t.Errorf("Update not reflected: %+v", reget)
	}
	if reget.Fields.String("rating") != "" {
		t.Errorf("Update did not replace fields; stale rating=%q remains", reget.Fields.String("rating"))
	}

	if err := repo.Delete(ctx, "e-crud"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "e-crud"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testEntriesAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetBySlug(ctx, "article", "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetBySlug(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.Update(ctx, "nope", newEntry("nope", "article", "nope", "Nope", content.StatusDraft, suiteBase)); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testEntriesUniqueness(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	a := newEntry("e-uniq-1", "article", "same-slug", "Same Slug", content.StatusDraft, suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	dup := newEntry("e-uniq-2", "article", "same-slug", "Same Slug", content.StatusDraft, suiteBase)
	if _, err := repo.Create(ctx, dup); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("Create colliding (type,slug): err=%v, want ErrAlreadyExists", err)
	}
	// Same slug under a different type does not collide — uniqueness is (type,slug).
	other := newEntry("e-uniq-3", "page", "same-slug", "Same Slug", content.StatusDraft, suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Errorf("Create same slug different type: err=%v, want nil", err)
	}
}

func testEntriesTerms(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	entries := repos.Entries

	term, err := taxonomy.NewTerm(ids, taxonomy.KindTag, "News", "", suiteBase)
	if err != nil {
		t.Fatalf("NewTerm: %v", err)
	}
	term, err = repos.Terms.Create(ctx, term)
	if err != nil {
		t.Fatalf("Create term: %v", err)
	}

	e1 := newEntry("e-term-1", "article", "post-one", "Post One", content.StatusPublished, suiteBase.Add(time.Minute))
	e2 := newEntry("e-term-2", "article", "post-two", "Post Two", content.StatusPublished, suiteBase)
	if _, err := entries.Create(ctx, e1); err != nil {
		t.Fatalf("Create e1: %v", err)
	}
	if _, err := entries.Create(ctx, e2); err != nil {
		t.Fatalf("Create e2: %v", err)
	}

	if err := entries.SetTerms(ctx, "e-term-1", []string{term.ID}); err != nil {
		t.Fatalf("SetTerms e1: %v", err)
	}
	if err := entries.SetTerms(ctx, "e-term-2", []string{term.ID}); err != nil {
		t.Fatalf("SetTerms e2: %v", err)
	}

	// Get hydrates the association.
	got, err := entries.Get(ctx, "e-term-1")
	if err != nil {
		t.Fatalf("Get e1: %v", err)
	}
	if len(got.TermIDs) != 1 || got.TermIDs[0] != term.ID {
		t.Errorf("Get e1 TermIDs=%v, want [%s]", got.TermIDs, term.ID)
	}

	// ListByTerm returns both associated entries.
	q := content.EntryQuery{Type: "article", ListRequest: crud.ListRequest{Limit: 10}}
	page, err := entries.ListByTerm(ctx, term.ID, q)
	if err != nil {
		t.Fatalf("ListByTerm: %v", err)
	}
	assertSameIDs(t, page.Items, []string{"e-term-1", "e-term-2"})

	// SetTerms replaces (not appends): clearing e2 drops it from the term.
	if err := entries.SetTerms(ctx, "e-term-2", nil); err != nil {
		t.Fatalf("SetTerms e2 clear: %v", err)
	}
	page, err = entries.ListByTerm(ctx, term.ID, q)
	if err != nil {
		t.Fatalf("ListByTerm after clear: %v", err)
	}
	assertSameIDs(t, page.Items, []string{"e-term-1"})

	// Deleting e1 cascades its association: ListByTerm is now empty.
	if err := entries.Delete(ctx, "e-term-1"); err != nil {
		t.Fatalf("Delete e1: %v", err)
	}
	page, err = entries.ListByTerm(ctx, term.ID, q)
	if err != nil {
		t.Fatalf("ListByTerm after delete: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("ListByTerm after entry delete = %d items, want 0 (association should cascade)", len(page.Items))
	}
}

func testEntriesPagination(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	// Five entries with distinct, ascending timestamps → clean (created_at DESC)
	// order and ≥2 page boundaries at limit 2.
	var ids []string
	for i := 0; i < 5; i++ {
		id := entryID("pg", i)
		ids = append(ids, id)
		e := newEntry(id, "article", slugFor("pg", i), "Page Entry", content.StatusDraft, suiteBase.Add(time.Duration(i)*time.Minute))
		if _, err := repo.Create(ctx, e); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	q := content.EntryQuery{Type: "article"}
	got := collectEntries(t, repo, q, 2)
	assertSameIDs(t, got, ids)
	assertEntriesDesc(t, got)

	// The largest timestamp (last created) sorts first under DESC order.
	if len(got) == 5 && got[0].ID != entryID("pg", 4) {
		t.Errorf("first item = %q, want newest %q", got[0].ID, entryID("pg", 4))
	}
}

func testEntriesCursorEdges(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	for i := 0; i < 3; i++ {
		e := newEntry(entryID("edge", i), "article", slugFor("edge", i), "Edge", content.StatusDraft, suiteBase.Add(time.Duration(i)*time.Minute))
		if _, err := repo.Create(ctx, e); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Empty cursor → first page.
	first, err := repo.List(ctx, content.EntryQuery{Type: "article", ListRequest: crud.ListRequest{Limit: 10}})
	if err != nil {
		t.Fatalf("List empty cursor: %v", err)
	}
	if len(first.Items) != 3 {
		t.Fatalf("empty cursor page = %d items, want 3", len(first.Items))
	}

	// A cursor whose order field does not match is stale — DecodeCursor treats it
	// as the first page, so the store must return the same first page, not error
	// or skip.
	stale, err := crud.EncodeCursor("wrong_field", suiteBase, "whatever")
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	stalePage, err := repo.List(ctx, content.EntryQuery{Type: "article", ListRequest: crud.ListRequest{Limit: 10, Cursor: stale}})
	if err != nil {
		t.Fatalf("List stale cursor: %v", err)
	}
	if len(stalePage.Items) != len(first.Items) {
		t.Errorf("stale cursor page = %d items, want %d (same as first page)", len(stalePage.Items), len(first.Items))
	}
}

// testEntriesPrecision is the mandatory timestamp-precision case (design §4.1 /
// §5). Rows are spaced one nanosecond apart from a microsecond-aligned base, so
// a store that truncates created_at to microseconds collapses them into one
// equal-timestamp group straddling a page boundary. Correct pagination then
// hinges on the (created_at, id) tie-break AND on encoding the cursor from the
// STORED (truncated) value, not the in-memory nanosecond value. The suite asserts
// full traversal identity and self-consistent ordering — never that nanosecond
// fidelity survives the round trip.
func testEntriesPrecision(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	base := suiteBase.Truncate(time.Microsecond)
	var ids []string
	for i := 0; i < 6; i++ {
		id := entryID("prec", i)
		ids = append(ids, id)
		e := newEntry(id, "article", slugFor("prec", i), "Precision", content.StatusDraft, base.Add(time.Duration(i)*time.Nanosecond))
		if _, err := repo.Create(ctx, e); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	got := collectEntries(t, repo, content.EntryQuery{Type: "article"}, 2)
	assertSameIDs(t, got, ids)
	assertEntriesDesc(t, got)
}

// --- Terms ---

func testTermsCRUD(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	term, err := taxonomy.NewTerm(ids, taxonomy.KindCategory, "News", "", suiteBase)
	if err != nil {
		t.Fatalf("NewTerm: %v", err)
	}
	created, err := repo.Create(ctx, term)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Name != "News" {
		t.Fatalf("Get: name=%q err=%v", got.Name, err)
	}
	bySlug, err := repo.GetBySlug(ctx, taxonomy.KindCategory, created.Slug)
	if err != nil || bySlug.ID != created.ID {
		t.Fatalf("GetBySlug: id=%q err=%v", bySlug.ID, err)
	}

	if err := got.ApplyEdit("Announcements", "", suiteBase.Add(time.Minute)); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if _, err := repo.Update(ctx, created.ID, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reget, err := repo.Get(ctx, created.ID)
	if err != nil || reget.Name != "Announcements" {
		t.Fatalf("Get after Update: name=%q err=%v", reget.Name, err)
	}

	list, err := repo.ListByKind(ctx, taxonomy.KindCategory)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListByKind: %v (err=%v)", list, err)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testTermsAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetBySlug(ctx, taxonomy.KindTag, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetBySlug(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := taxonomy.NewTerm(ids, taxonomy.KindTag, "Ghost", "", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
}

func testTermsUniqueness(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	t1, _ := taxonomy.NewTerm(ids, taxonomy.KindCategory, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t1); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	t2, _ := taxonomy.NewTerm(ids, taxonomy.KindCategory, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("Create colliding (kind,slug): err=%v, want ErrAlreadyExists", err)
	}
	// Same slug under a different kind does not collide — uniqueness is (kind,slug).
	t3, _ := taxonomy.NewTerm(ids, taxonomy.KindTag, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t3); err != nil {
		t.Errorf("Create same slug different kind: err=%v, want nil", err)
	}
}

func testTermsListByKind(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	for _, name := range []string{"Zebra", "Apple", "Mango"} {
		term, _ := taxonomy.NewTerm(ids, taxonomy.KindCategory, name, "", suiteBase)
		if _, err := repo.Create(ctx, term); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	// A tag should not appear in the category list.
	tag, _ := taxonomy.NewTerm(ids, taxonomy.KindTag, "Sidebar", "", suiteBase)
	if _, err := repo.Create(ctx, tag); err != nil {
		t.Fatalf("Create tag: %v", err)
	}

	list, err := repo.ListByKind(ctx, taxonomy.KindCategory)
	if err != nil {
		t.Fatalf("ListByKind: %v", err)
	}
	want := []string{"Apple", "Mango", "Zebra"}
	if len(list) != len(want) {
		t.Fatalf("ListByKind returned %d terms, want %d", len(list), len(want))
	}
	for i, name := range want {
		if list[i].Name != name {
			t.Errorf("ListByKind[%d].Name = %q, want %q (ordered by name)", i, list[i].Name, name)
		}
	}
}

// --- Menus ---

func testMenusCRUD(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	menu, err := menus.NewMenu(ids, "Main", suiteBase)
	if err != nil {
		t.Fatalf("NewMenu: %v", err)
	}
	created, err := repo.CreateMenu(ctx, menu)
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	got, err := repo.GetMenu(ctx, created.ID)
	if err != nil || got.Name != "Main" {
		t.Fatalf("GetMenu: name=%q err=%v", got.Name, err)
	}
	bySlug, err := repo.GetMenuBySlug(ctx, created.Slug)
	if err != nil || bySlug.ID != created.ID {
		t.Fatalf("GetMenuBySlug: id=%q err=%v", bySlug.ID, err)
	}

	list, err := repo.ListMenus(ctx)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListMenus: %v (err=%v)", list, err)
	}
}

func testMenusAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	if _, err := repo.GetMenu(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetMenu(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetMenuBySlug(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetMenuBySlug(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetItem(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetItem(absent): err=%v, want ErrNotFound", err)
	}
	item, _ := menus.NewMenuItem(ids, "m", "Home", "/", "", 0, suiteBase)
	if _, err := repo.UpdateItem(ctx, "nope", item); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("UpdateItem(absent): err=%v, want ErrNotFound", err)
	}
}

func testMenusUniqueness(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	m1, _ := menus.NewMenu(ids, "Footer", suiteBase)
	if _, err := repo.CreateMenu(ctx, m1); err != nil {
		t.Fatalf("first CreateMenu: %v", err)
	}
	m2, _ := menus.NewMenu(ids, "Footer", suiteBase)
	if _, err := repo.CreateMenu(ctx, m2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("CreateMenu colliding slug: err=%v, want ErrAlreadyExists", err)
	}
}

func testMenuItems(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	menu, _ := menus.NewMenu(ids, "Nav", suiteBase)
	menu, err := repo.CreateMenu(ctx, menu)
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// Two top-level items in reverse position order to verify ordering.
	second, _ := menus.NewMenuItem(ids, menu.ID, "About", "/about", "", 1, suiteBase)
	first, _ := menus.NewMenuItem(ids, menu.ID, "Home", "/", "", 0, suiteBase)
	if _, err := repo.AddItem(ctx, second); err != nil {
		t.Fatalf("AddItem second: %v", err)
	}
	if _, err := repo.AddItem(ctx, first); err != nil {
		t.Fatalf("AddItem first: %v", err)
	}

	got, err := repo.GetItem(ctx, first.ID)
	if err != nil || got.Label != "Home" {
		t.Fatalf("GetItem: label=%q err=%v", got.Label, err)
	}

	items, err := repo.ItemsForMenu(ctx, menu.ID)
	if err != nil {
		t.Fatalf("ItemsForMenu: %v", err)
	}
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID {
		t.Errorf("ItemsForMenu order = %v, want [Home, About] by position", items)
	}

	if err := got.ApplyEdit("Homepage", "/", "", 0, suiteBase.Add(time.Minute)); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if _, err := repo.UpdateItem(ctx, first.ID, got); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	reget, err := repo.GetItem(ctx, first.ID)
	if err != nil || reget.Label != "Homepage" {
		t.Fatalf("GetItem after Update: label=%q err=%v", reget.Label, err)
	}

	if err := repo.DeleteItem(ctx, first.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := repo.GetItem(ctx, first.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetItem after Delete: err=%v, want ErrNotFound", err)
	}
}

// --- Media ---

func testMediaCRUD(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Media

	asset, err := media.NewAsset(ids, "photo.jpg", "image/jpeg", 1024, suiteBase)
	if err != nil {
		t.Fatalf("NewAsset: %v", err)
	}
	created, err := repo.Create(ctx, asset)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Filename != "photo.jpg" {
		t.Fatalf("Get: filename=%q err=%v", got.Filename, err)
	}

	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List: %v (err=%v)", list, err)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testMediaAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	if _, err := repos.Media.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

func testMediaListOrder(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Media

	older, _ := media.NewAsset(ids, "old.jpg", "image/jpeg", 10, suiteBase)
	newer, _ := media.NewAsset(ids, "new.jpg", "image/jpeg", 20, suiteBase.Add(time.Hour))
	if _, err := repo.Create(ctx, older); err != nil {
		t.Fatalf("Create older: %v", err)
	}
	if _, err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("List order = %v, want newest first", list)
	}
}

// --- Inquiries ---

func testInquiries(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Inquiries

	older, err := messaging.NewInquiry(ids, "Jane", "jane@example.com", "First", suiteBase)
	if err != nil {
		t.Fatalf("NewInquiry older: %v", err)
	}
	newer, err := messaging.NewInquiry(ids, "John", "john@example.com", "Second", suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewInquiry newer: %v", err)
	}
	if _, err := repo.Create(ctx, older); err != nil {
		t.Fatalf("Create older: %v", err)
	}
	if _, err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Errorf("List order = %v, want newest first", list)
	}
}

// --- amended D10: database-generated keys on empty ID ---
//
// The tests below prove the Create side of the cryptids.Database strategy: an
// entity constructed with the Database generator reaches the store with an
// empty ID, and Create must hand back a store-assigned, non-empty key under
// which the row is readable. SQL adapters satisfy this by omitting the id
// column and reading the schema default back with RETURNING (migration
// 0022_id_defaults); memory implementations assign at insert.

func testEntriesDBGeneratedID(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	e, err := content.NewEntry(dbIDs, "article", "DB Gen", "", "", "", content.StatusDraft, "", suiteBase)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if e.ID != "" {
		t.Fatalf("Database strategy minted a non-empty ID: %q", e.ID)
	}
	created, err := repos.Entries.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.Entries.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.Title != "DB Gen" {
		t.Errorf("row under generated key has title %q", got.Title)
	}
}

func testTermsDBGeneratedID(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	term, err := taxonomy.NewTerm(dbIDs, taxonomy.KindCategory, "DB Gen", "", suiteBase)
	if err != nil {
		t.Fatalf("NewTerm: %v", err)
	}
	created, err := repos.Terms.Create(ctx, term)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.Terms.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.Name != "DB Gen" {
		t.Errorf("row under generated key has name %q", got.Name)
	}
}

func testMenusDBGeneratedID(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	menu, err := menus.NewMenu(dbIDs, "DB Gen", suiteBase)
	if err != nil {
		t.Fatalf("NewMenu: %v", err)
	}
	createdMenu, err := repo.CreateMenu(ctx, menu)
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if createdMenu.ID == "" {
		t.Fatal("CreateMenu returned an empty ID — the store did not assign a database-generated key")
	}
	gotMenu, err := repo.GetMenu(ctx, createdMenu.ID)
	if err != nil {
		t.Fatalf("GetMenu by generated id: %v", err)
	}
	if gotMenu.Name != "DB Gen" {
		t.Errorf("menu under generated key has name %q", gotMenu.Name)
	}

	item, err := menus.NewMenuItem(dbIDs, createdMenu.ID, "Home", "/", "", 0, suiteBase)
	if err != nil {
		t.Fatalf("NewMenuItem: %v", err)
	}
	createdItem, err := repo.AddItem(ctx, item)
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if createdItem.ID == "" {
		t.Fatal("AddItem returned an empty ID — the store did not assign a database-generated key")
	}
	gotItem, err := repo.GetItem(ctx, createdItem.ID)
	if err != nil {
		t.Fatalf("GetItem by generated id: %v", err)
	}
	if gotItem.Label != "Home" {
		t.Errorf("item under generated key has label %q", gotItem.Label)
	}
}

func testMediaDBGeneratedID(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	asset, err := media.NewAsset(dbIDs, "photo.jpg", "image/jpeg", 1024, suiteBase)
	if err != nil {
		t.Fatalf("NewAsset: %v", err)
	}
	created, err := repos.Media.Create(ctx, asset)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.Media.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.Filename != "photo.jpg" {
		t.Errorf("row under generated key has filename %q", got.Filename)
	}
}

func testInquiriesDBGeneratedID(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	in, err := messaging.NewInquiry(dbIDs, "Jane", "jane@example.com", "DB Gen", suiteBase)
	if err != nil {
		t.Fatalf("NewInquiry: %v", err)
	}
	created, err := repos.Inquiries.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	// Inquiries expose no Get; the row is read back through List by its key.
	list, err := repos.Inquiries.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, got := range list {
		if got.ID == created.ID {
			found = true
			if got.Message != "DB Gen" {
				t.Errorf("row under generated key has message %q", got.Message)
			}
		}
	}
	if !found {
		t.Errorf("inquiry with generated key %q not found in List", created.ID)
	}
}

// entryID and slugFor build stable, sortable identities for the pagination
// cases so the (created_at, id) tie-break is deterministic.
func entryID(prefix string, i int) string { return prefix + "-" + string(rune('a'+i)) }
func slugFor(prefix string, i int) string { return prefix + "-slug-" + string(rune('a'+i)) }

// --- the standard paginated-port case family (copied from P3, scoped to entries) ---
//
// Entries' two paginated ports (List, ListByTerm) each run the same six cases —
// Order, PrevPage, OffsetMode, WithCount, StaleCursorOrderChange,
// CursorOffsetExclusive — over a small seeded population. A port supplies a
// scoped list closure and a fresh-population seed returning the in-scope
// population plus its expected count (foreign rows a filter must exclude are not
// counted).

// familyMinutes is the created_at spread each seed uses: six rows with one
// same-created_at pair (indices 2 and 3) so the family exercises three pages of
// two plus the id tiebreak.
var familyMinutes = []int{0, 1, 2, 2, 3, 4}

// entryPagedScope runs a List against an already-scoped query for one paginated
// entry port.
type entryPagedScope func(repos cms.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[content.Entry], error)

// runEntriesPagedFamily wires the six standard cases for one paginated entry
// port. Each case obtains a clean, isolated Repositories from newRepos and seeds
// its own population, matching the suite's per-leaf isolation contract.
func runEntriesPagedFamily(
	t *testing.T,
	newRepos func(t *testing.T) cms.Repositories,
	scope entryPagedScope,
	seed func(t *testing.T, repos cms.Repositories) (created []content.Entry, wantTotal int),
) {
	t.Helper()

	list := func(repos cms.Repositories) func(context.Context, crud.ListRequest) (crud.Page[content.Entry], error) {
		return func(ctx context.Context, req crud.ListRequest) (crud.Page[content.Entry], error) {
			return scope(repos, ctx, req)
		}
	}

	t.Run("Order", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runEntriesOrderCase(t, list(repos), created)
	})
	t.Run("PrevPage", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runEntriesPrevPageCase(t, list(repos), created)
	})
	t.Run("OffsetMode", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runEntriesOffsetModeCase(t, list(repos), created)
	})
	t.Run("WithCount", func(t *testing.T) {
		repos := newRepos(t)
		_, wantTotal := seed(t, repos)
		runEntriesWithCountCase(t, list(repos), wantTotal)
	})
	t.Run("StaleCursorOrderChange", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runEntriesStaleCursorCase(t, list(repos), created)
	})
	t.Run("CursorOffsetExclusive", func(t *testing.T) {
		repos := newRepos(t)
		_, _ = seed(t, repos)
		runEntriesCursorOffsetExclusiveCase(t, list(repos))
	})
}

type entryList func(context.Context, crud.ListRequest) (crud.Page[content.Entry], error)

// runEntriesOrderCase asserts explicit asc + desc ordering on created_at pages
// through the full population in the correct total order (created_at then id
// tiebreak in the same direction), re-asserting the tiebreak under asc via the
// seeded same-created_at pair.
func runEntriesOrderCase(t *testing.T, list entryList, created []content.Entry) {
	t.Helper()
	wantAsc := entrySortedIDs(created, true)
	gotAsc := pageEntriesOrdered(t, list, crud.NewOrder("created_at", crud.ASC), 2)
	if !equalStrings(gotAsc, wantAsc) {
		t.Errorf("asc order = %v, want %v", gotAsc, wantAsc)
	}

	wantDesc := entrySortedIDs(created, false)
	gotDesc := pageEntriesOrdered(t, list, crud.NewOrder("created_at", crud.DESC), 2)
	if !equalStrings(gotDesc, wantDesc) {
		t.Errorf("desc order = %v, want %v", gotDesc, wantDesc)
	}
}

// runEntriesPrevPageCase asserts the reverse-probe semantics: the first page has
// no prev; page 2 has a prev whose (partial) window means "previous is the first
// page" and round-trips to page 1's IDs; page 3 has a full prior window whose
// PreviousCursor round-trips to page 2's IDs.
func runEntriesPrevPageCase(t *testing.T, list entryList, created []content.Entry) {
	t.Helper()
	ctx := context.Background()
	desc := entrySortedIDs(created, false)
	if len(desc) < 6 {
		t.Fatalf("prev-page case needs >= 6 seeded rows, got %d", len(desc))
	}

	page1, err := list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.HasPrev {
		t.Errorf("first page HasPrev = true, want false")
	}
	if got := entryIDsOf(page1.Items); !equalStrings(got, desc[0:2]) {
		t.Errorf("page1 = %v, want %v", got, desc[0:2])
	}

	page2, err := list(ctx, crud.ListRequest{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if !page2.HasPrev {
		t.Errorf("page2 HasPrev = false, want true")
	}
	if got := entryIDsOf(page2.Items); !equalStrings(got, desc[2:4]) {
		t.Errorf("page2 = %v, want %v", got, desc[2:4])
	}
	// Only one row precedes the page-1 cursor, so page 2's prior window is
	// partial ⇒ empty PreviousCursor meaning "the previous page is the first page".
	if page2.PreviousCursor != "" {
		t.Errorf("page2 PreviousCursor = %q, want empty (partial prior window)", page2.PreviousCursor)
	}
	back := backEntries(t, list, page2.PreviousCursor)
	if got := entryIDsOf(back.Items); !equalStrings(got, desc[0:2]) {
		t.Errorf("previous of page2 = %v, want page1 %v", got, desc[0:2])
	}

	page3, err := list(ctx, crud.ListRequest{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if !page3.HasPrev {
		t.Errorf("page3 HasPrev = false, want true")
	}
	if page3.PreviousCursor == "" {
		t.Errorf("page3 PreviousCursor empty, want a full prior window")
	}
	back3 := backEntries(t, list, page3.PreviousCursor)
	if got := entryIDsOf(back3.Items); !equalStrings(got, desc[2:4]) {
		t.Errorf("previous of page3 = %v, want page2 %v", got, desc[2:4])
	}
}

// runEntriesOffsetModeCase asserts offset traversal (explicit StrategyOffset)
// yields the identical ID sequence as cursor traversal, HasPrev iff offset > 0,
// that offset pages emit no cursors at any offset, and that Offset 0 under the
// offset strategy is the first page.
func runEntriesOffsetModeCase(t *testing.T, list entryList, created []content.Entry) {
	t.Helper()
	ctx := context.Background()

	cursorIDs := pageEntriesOrdered(t, list, crud.Order{}, 2)

	var offsetIDs []string
	for off := 0; off < 100; off += 2 {
		page, err := list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: off})
		if err != nil {
			t.Fatalf("offset page at %d: %v", off, err)
		}
		offsetIDs = append(offsetIDs, entryIDsOf(page.Items)...)
		// Offset strategy emits no cursors at any offset — the caller does the
		// offset arithmetic.
		if page.NextCursor != "" || page.PreviousCursor != "" {
			t.Errorf("offset page at %d carried a cursor (next=%q prev=%q)", off, page.NextCursor, page.PreviousCursor)
		}
		if want := off > 0; page.HasPrev != want {
			t.Errorf("offset page at %d HasPrev = %v, want %v", off, page.HasPrev, want)
		}
		if !page.HasMore {
			break
		}
	}
	if !equalStrings(offsetIDs, cursorIDs) {
		t.Errorf("offset traversal = %v, want cursor traversal %v", offsetIDs, cursorIDs)
	}

	// OffsetZero: the offset strategy with Offset 0 is the first page — HasPrev
	// false and no cursors. This is the wart the explicit strategy fixes: Offset 0
	// no longer silently means cursor mode.
	zero, err := list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("offset-zero page: %v", err)
	}
	if zero.HasPrev {
		t.Errorf("offset-zero HasPrev = true, want false")
	}
	if zero.NextCursor != "" || zero.PreviousCursor != "" {
		t.Errorf("offset-zero carried a cursor (next=%q prev=%q)", zero.NextCursor, zero.PreviousCursor)
	}
	if got, want := entryIDsOf(zero.Items), cursorIDs[:len(zero.Items)]; !equalStrings(got, want) {
		t.Errorf("offset-zero page = %v, want first page %v", got, want)
	}
}

// runEntriesWithCountCase asserts Total equals the filtered row count in both
// modes and is nil when unrequested.
func runEntriesWithCountCase(t *testing.T, list entryList, wantTotal int) {
	t.Helper()
	ctx := context.Background()

	cursorPage, err := list(ctx, crud.ListRequest{Limit: 2, WithCount: true})
	if err != nil {
		t.Fatalf("cursor+count: %v", err)
	}
	if cursorPage.Total == nil || *cursorPage.Total != int64(wantTotal) {
		t.Errorf("cursor-mode Total = %v, want %d", cursorPage.Total, wantTotal)
	}

	offsetPage, err := list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 2, WithCount: true})
	if err != nil {
		t.Fatalf("offset+count: %v", err)
	}
	if offsetPage.Total == nil || *offsetPage.Total != int64(wantTotal) {
		t.Errorf("offset-mode Total = %v, want %d", offsetPage.Total, wantTotal)
	}

	noCount, err := list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("no-count: %v", err)
	}
	if noCount.Total != nil {
		t.Errorf("Total = %v, want nil when count not requested", *noCount.Total)
	}
}

// runEntriesStaleCursorCase asserts a cursor minted under a different sort field
// is treated as the first page (no error, no skew). The order field is the only
// staleness key the cursor codec carries, so a token authored for a different
// column decodes to nil and the store returns the first page.
func runEntriesStaleCursorCase(t *testing.T, list entryList, created []content.Entry) {
	t.Helper()
	ctx := context.Background()

	first, err := list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	stale, err := crud.EncodeCursor("updated_at", created[0].CreatedAt, created[0].ID)
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	got, err := list(ctx, crud.ListRequest{Limit: 2, Cursor: stale})
	if err != nil {
		t.Fatalf("stale cursor: err=%v, want first page", err)
	}
	if g, w := entryIDsOf(got.Items), entryIDsOf(first.Items); !equalStrings(g, w) {
		t.Errorf("stale cursor = %v, want first page %v (treated as first page)", g, w)
	}
	if got.HasPrev {
		t.Errorf("stale-cursor first page HasPrev = true, want false")
	}
}

// runEntriesCursorOffsetExclusiveCase asserts each per-strategy conflict is
// rejected with the invalid-input kind: a cursor strategy carrying a non-zero
// offset, and an offset strategy carrying a cursor.
func runEntriesCursorOffsetExclusiveCase(t *testing.T, list entryList) {
	t.Helper()
	ctx := context.Background()
	if _, err := list(ctx, crud.ListRequest{Strategy: crud.StrategyCursor, Limit: 2, Offset: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("cursor strategy + offset: err=%v, want ErrInvalidInput", err)
	}
	if _, err := list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Cursor: "anything"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("offset strategy + cursor: err=%v, want ErrInvalidInput", err)
	}
}

// backEntries requests the previous page: an empty previousCursor means "the
// previous page is the first page", so a first-page request is issued.
func backEntries(t *testing.T, list entryList, previousCursor string) crud.Page[content.Entry] {
	t.Helper()
	page, err := list(context.Background(), crud.ListRequest{Limit: 2, Cursor: previousCursor})
	if err != nil {
		t.Fatalf("previous page: %v", err)
	}
	return page
}

// entrySortedIDs returns the ids of entries sorted by (created_at, id) in the
// given direction — the total order every paginated port must page in.
func entrySortedIDs(items []content.Entry, asc bool) []string {
	sorted := append([]content.Entry(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		ti, tj := sorted[i].CreatedAt, sorted[j].CreatedAt
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].ID > sorted[j].ID
	})
	return entryIDsOf(sorted)
}

// entryIDsOf projects each entry's id.
func entryIDsOf(items []content.Entry) []string {
	out := make([]string, len(items))
	for i, e := range items {
		out[i] = e.ID
	}
	return out
}

// pageEntriesOrdered pages forward through the whole population under order,
// threading order into every request, and returns the collected ids in traversal
// order.
func pageEntriesOrdered(t *testing.T, list entryList, order crud.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := list(ctx, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ids = append(ids, entryIDsOf(page.Items)...)
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageEntriesOrdered did not terminate")
	return nil
}

// equalStrings reports whether two id slices are equal element-wise.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// seedFamilyEntries creates six article entries on the familyMinutes spread for
// the List family. A page-typed entry is added to prove the type filter excludes
// (and does not count) foreign rows.
func seedFamilyEntries(t *testing.T, repos cms.Repositories) ([]content.Entry, int) {
	t.Helper()
	ctx := context.Background()
	repo := repos.Entries
	created := make([]content.Entry, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		e := newEntry(entryID("fam", i), "article", slugFor("fam", i), "Family Entry", content.StatusDraft, suiteBase.Add(time.Duration(m)*time.Minute))
		c, err := repo.Create(ctx, e)
		if err != nil {
			t.Fatalf("Create %s: %v", e.ID, err)
		}
		created = append(created, c)
	}
	// A foreign-type entry must not be listed or counted by an article-scoped list.
	other := newEntry("fam-other", "page", "fam-other-slug", "Other", content.StatusDraft, suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create(foreign): %v", err)
	}
	return created, len(created)
}

// seedFamilyEntriesByTerm creates six article entries associated with term-fam
// for the ListByTerm family. Foreign entries under a different term must not be
// listed or counted. entry_terms has no term FK, so a bare term id suffices.
func seedFamilyEntriesByTerm(t *testing.T, repos cms.Repositories) ([]content.Entry, int) {
	t.Helper()
	ctx := context.Background()
	repo := repos.Entries
	created := make([]content.Entry, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		e := newEntry(entryID("famt", i), "article", slugFor("famt", i), "Family Term Entry", content.StatusDraft, suiteBase.Add(time.Duration(m)*time.Minute))
		c, err := repo.Create(ctx, e)
		if err != nil {
			t.Fatalf("Create %s: %v", e.ID, err)
		}
		if err := repo.SetTerms(ctx, e.ID, []string{"term-fam"}); err != nil {
			t.Fatalf("SetTerms %s: %v", e.ID, err)
		}
		created = append(created, c)
	}
	// Foreign rows under a different term must not be listed or counted.
	for i := 0; i < 2; i++ {
		e := newEntry(entryID("famtf", i), "article", slugFor("famtf", i), "Foreign Term Entry", content.StatusDraft, suiteBase.Add(time.Duration(i)*time.Minute))
		if _, err := repo.Create(ctx, e); err != nil {
			t.Fatalf("Create(foreign): %v", err)
		}
		if err := repo.SetTerms(ctx, e.ID, []string{"term-other"}); err != nil {
			t.Fatalf("SetTerms(foreign): %v", err)
		}
	}
	return created, len(created)
}
