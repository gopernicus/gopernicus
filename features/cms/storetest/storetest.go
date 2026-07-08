// Package storetest is the conformance suite for the CMS feature's repository
// ports: every store that fills a cms.Repositories — the in-package reference
// implementation, a host memstore (examples/minimal), and each dialect adapter
// (features/cms/stores/turso, .../postgres) — should pass Run against a freshly
// wired, isolated Repositories. Modeled on sdk/cacher/cachertest's
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
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

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
		t.Run("TermAssociationAndCascade", func(t *testing.T) { testEntriesTerms(t, newRepos(t)) })
		t.Run("CursorPagination", func(t *testing.T) { testEntriesPagination(t, newRepos(t)) })
		t.Run("StaleAndEmptyCursor", func(t *testing.T) { testEntriesCursorEdges(t, newRepos(t)) })
		t.Run("TimestampPrecision", func(t *testing.T) { testEntriesPrecision(t, newRepos(t)) })
	})

	t.Run("Terms", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testTermsCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testTermsAbsent(t, newRepos(t)) })
		t.Run("KindSlugUniqueness", func(t *testing.T) { testTermsUniqueness(t, newRepos(t)) })
		t.Run("ListByKindOrdered", func(t *testing.T) { testTermsListByKind(t, newRepos(t)) })
	})

	t.Run("Menus", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testMenusCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testMenusAbsent(t, newRepos(t)) })
		t.Run("SlugUniqueness", func(t *testing.T) { testMenusUniqueness(t, newRepos(t)) })
		t.Run("Items", func(t *testing.T) { testMenuItems(t, newRepos(t)) })
	})

	t.Run("Media", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testMediaCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testMediaAbsent(t, newRepos(t)) })
		t.Run("ListNewestFirst", func(t *testing.T) { testMediaListOrder(t, newRepos(t)) })
	})

	t.Run("Inquiries", func(t *testing.T) {
		t.Run("CreateAndListNewestFirst", func(t *testing.T) { testInquiries(t, newRepos(t)) })
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
	if _, err := repo.Get(ctx, "e-crud"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testEntriesAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Entries

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetBySlug(ctx, "article", "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetBySlug(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.Update(ctx, "nope", newEntry("nope", "article", "nope", "Nope", content.StatusDraft, suiteBase)); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
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
	if _, err := repo.Create(ctx, dup); !errors.Is(err, errs.ErrAlreadyExists) {
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

	term, err := taxonomy.NewTerm(taxonomy.KindTag, "News", "", suiteBase)
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

	term, err := taxonomy.NewTerm(taxonomy.KindCategory, "News", "", suiteBase)
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
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testTermsAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetBySlug(ctx, taxonomy.KindTag, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetBySlug(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := taxonomy.NewTerm(taxonomy.KindTag, "Ghost", "", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
}

func testTermsUniqueness(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	t1, _ := taxonomy.NewTerm(taxonomy.KindCategory, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t1); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	t2, _ := taxonomy.NewTerm(taxonomy.KindCategory, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t2); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding (kind,slug): err=%v, want ErrAlreadyExists", err)
	}
	// Same slug under a different kind does not collide — uniqueness is (kind,slug).
	t3, _ := taxonomy.NewTerm(taxonomy.KindTag, "Duplicate", "", suiteBase)
	if _, err := repo.Create(ctx, t3); err != nil {
		t.Errorf("Create same slug different kind: err=%v, want nil", err)
	}
}

func testTermsListByKind(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Terms

	for _, name := range []string{"Zebra", "Apple", "Mango"} {
		term, _ := taxonomy.NewTerm(taxonomy.KindCategory, name, "", suiteBase)
		if _, err := repo.Create(ctx, term); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	// A tag should not appear in the category list.
	tag, _ := taxonomy.NewTerm(taxonomy.KindTag, "Sidebar", "", suiteBase)
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

	menu, err := menus.NewMenu("Main", suiteBase)
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

	if _, err := repo.GetMenu(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetMenu(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetMenuBySlug(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetMenuBySlug(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetItem(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetItem(absent): err=%v, want ErrNotFound", err)
	}
	item, _ := menus.NewMenuItem("m", "Home", "/", "", 0, suiteBase)
	if _, err := repo.UpdateItem(ctx, "nope", item); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("UpdateItem(absent): err=%v, want ErrNotFound", err)
	}
}

func testMenusUniqueness(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	m1, _ := menus.NewMenu("Footer", suiteBase)
	if _, err := repo.CreateMenu(ctx, m1); err != nil {
		t.Fatalf("first CreateMenu: %v", err)
	}
	m2, _ := menus.NewMenu("Footer", suiteBase)
	if _, err := repo.CreateMenu(ctx, m2); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("CreateMenu colliding slug: err=%v, want ErrAlreadyExists", err)
	}
}

func testMenuItems(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Menus

	menu, _ := menus.NewMenu("Nav", suiteBase)
	menu, err := repo.CreateMenu(ctx, menu)
	if err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}

	// Two top-level items in reverse position order to verify ordering.
	second, _ := menus.NewMenuItem(menu.ID, "About", "/about", "", 1, suiteBase)
	first, _ := menus.NewMenuItem(menu.ID, "Home", "/", "", 0, suiteBase)
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
	if _, err := repo.GetItem(ctx, first.ID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetItem after Delete: err=%v, want ErrNotFound", err)
	}
}

// --- Media ---

func testMediaCRUD(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Media

	asset, err := media.NewAsset("photo.jpg", "image/jpeg", 1024, suiteBase)
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
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testMediaAbsent(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	if _, err := repos.Media.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

func testMediaListOrder(t *testing.T, repos cms.Repositories) {
	ctx := context.Background()
	repo := repos.Media

	older, _ := media.NewAsset("old.jpg", "image/jpeg", 10, suiteBase)
	newer, _ := media.NewAsset("new.jpg", "image/jpeg", 20, suiteBase.Add(time.Hour))
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

	older, err := messaging.NewInquiry("Jane", "jane@example.com", "First", suiteBase)
	if err != nil {
		t.Fatalf("NewInquiry older: %v", err)
	}
	newer, err := messaging.NewInquiry("John", "john@example.com", "Second", suiteBase.Add(time.Hour))
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

// entryID and slugFor build stable, sortable identities for the pagination
// cases so the (created_at, id) tie-break is deterministic.
func entryID(prefix string, i int) string { return prefix + "-" + string(rune('a'+i)) }
func slugFor(prefix string, i int) string { return prefix + "-slug-" + string(rune('a'+i)) }
