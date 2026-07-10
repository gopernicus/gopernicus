// Tests for memstore's port-semantics: each repository's Create/Get/List/
// Delete happy path plus the sdk/errs sentinel contract each port's doc
// comment promises, including the uniqueness rules the turso store enforces
// in SQL (features/cms/stores/turso/migrations) — entry (type,slug), term
// (kind,slug), and menu slug collisions all return sdk.ErrAlreadyExists.
//
// Copied verbatim from examples/minimal/internal/memstore alongside the store
// it exercises (see memstore.go's copy note).
package memstore

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
	"github.com/gopernicus/gopernicus/features/cms/storetest"
	"github.com/gopernicus/gopernicus/sdk"
)

var now = time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)

// TestConformance runs the shared cms storetest suite against memstore, the drift
// net over the hand-written bespoke cases below: a fresh Store per call is the
// memory harness's clean-isolation contract.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) cms.Repositories {
		return New().Repositories()
	})
}

// --- content.EntryRepository ---

func TestEntryRepo_CreateGetListDelete(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	e, err := content.NewEntry(ids, "article", "First Post", "excerpt", "body", "demo", content.StatusPublished, "", now)
	if err != nil {
		t.Fatalf("NewEntry() error = %v", err)
	}
	created, err := repos.Entries.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repos.Entries.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "First Post" {
		t.Errorf("Get() Title = %q, want %q", got.Title, "First Post")
	}

	page, err := repos.Entries.List(ctx, content.EntryQuery{Type: "article"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Errorf("List() = %v, want one item with id %q", page.Items, created.ID)
	}

	if err := repos.Entries.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repos.Entries.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want sdk.ErrNotFound", err)
	}
}

func TestEntryRepo_GetUnknown(t *testing.T) {
	repos := New().Repositories()
	if _, err := repos.Entries.Get(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want sdk.ErrNotFound", err)
	}
}

func TestEntryRepo_DeleteUnknown(t *testing.T) {
	repos := New().Repositories()
	if err := repos.Entries.Delete(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Delete(unknown) error = %v, want sdk.ErrNotFound (documented in content.EntryRepository)", err)
	}
}

// TestEntryRepo_DuplicateTypeSlugCollision mirrors the turso store's
// UNIQUE(type, slug) constraint (features/cms/stores/turso/migrations/
// 0018_entries.sql) — memstore enforces the same rule in Create.
func TestEntryRepo_DuplicateTypeSlugCollision(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	e1, _ := content.NewEntry(ids, "article", "Same Title", "", "body", "", content.StatusDraft, "", now)
	if _, err := repos.Entries.Create(ctx, e1); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}

	e2, _ := content.NewEntry(ids, "article", "Same Title", "", "different body", "", content.StatusDraft, "", now)
	if _, err := repos.Entries.Create(ctx, e2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("second Create() with colliding (type,slug) error = %v, want sdk.ErrAlreadyExists", err)
	}

	// A different Type with the same slug does not collide (uniqueness is
	// scoped per (type, slug), not slug alone).
	e3, _ := content.NewEntry(ids, "page", "Same Title", "", "body", "", content.StatusDraft, "", now)
	if _, err := repos.Entries.Create(ctx, e3); err != nil {
		t.Errorf("Create() with same slug but different type error = %v, want nil", err)
	}
}

// --- taxonomy.TermRepository ---

func TestTermRepo_CreateGetListDelete(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	term, err := taxonomy.NewTerm(ids, taxonomy.KindCategory, "News", "", now)
	if err != nil {
		t.Fatalf("NewTerm() error = %v", err)
	}
	created, err := repos.Terms.Create(ctx, term)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repos.Terms.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "News" {
		t.Errorf("Get() Name = %q, want %q", got.Name, "News")
	}

	list, err := repos.Terms.ListByKind(ctx, taxonomy.KindCategory)
	if err != nil {
		t.Fatalf("ListByKind() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("ListByKind() = %v, want one item with id %q", list, created.ID)
	}

	if err := repos.Terms.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repos.Terms.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want sdk.ErrNotFound", err)
	}
}

func TestTermRepo_GetUnknown(t *testing.T) {
	repos := New().Repositories()
	if _, err := repos.Terms.Get(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want sdk.ErrNotFound", err)
	}
	if _, err := repos.Terms.GetBySlug(context.Background(), taxonomy.KindTag, "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetBySlug(unknown) error = %v, want sdk.ErrNotFound", err)
	}
}

// TestTermRepo_KindSlugCollision asserts the port contract turso enforces via
// migrations/0010_terms_kind_slug_idx.sql ("CREATE UNIQUE INDEX ... ON terms
// (kind, slug)"): a duplicate (kind,slug) is rejected with
// sdk.ErrAlreadyExists. (Historical note: memstore originally diverged here —
// silently succeeding — fixed 2026-07-02.)
func TestTermRepo_KindSlugCollision(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	t1, _ := taxonomy.NewTerm(ids, taxonomy.KindCategory, "Duplicate Name", "", now)
	if _, err := repos.Terms.Create(ctx, t1); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	t2, _ := taxonomy.NewTerm(ids, taxonomy.KindCategory, "Duplicate Name", "", now)
	if _, err := repos.Terms.Create(ctx, t2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("second Create() with colliding (kind,slug) error = %v, want sdk.ErrAlreadyExists", err)
	}
}

// --- menus.MenuRepository ---

func TestMenuRepo_CreateGetListAndItems(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	menu, err := menus.NewMenu(ids, "Main", now)
	if err != nil {
		t.Fatalf("NewMenu() error = %v", err)
	}
	createdMenu, err := repos.Menus.CreateMenu(ctx, menu)
	if err != nil {
		t.Fatalf("CreateMenu() error = %v", err)
	}

	got, err := repos.Menus.GetMenu(ctx, createdMenu.ID)
	if err != nil {
		t.Fatalf("GetMenu() error = %v", err)
	}
	if got.Name != "Main" {
		t.Errorf("GetMenu() Name = %q, want %q", got.Name, "Main")
	}

	list, err := repos.Menus.ListMenus(ctx)
	if err != nil {
		t.Fatalf("ListMenus() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != createdMenu.ID {
		t.Errorf("ListMenus() = %v, want one item with id %q", list, createdMenu.ID)
	}

	item, err := menus.NewMenuItem(ids, createdMenu.ID, "Home", "/", "", 0, now)
	if err != nil {
		t.Fatalf("NewMenuItem() error = %v", err)
	}
	createdItem, err := repos.Menus.AddItem(ctx, item)
	if err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}

	items, err := repos.Menus.ItemsForMenu(ctx, createdMenu.ID)
	if err != nil {
		t.Fatalf("ItemsForMenu() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != createdItem.ID {
		t.Errorf("ItemsForMenu() = %v, want one item with id %q", items, createdItem.ID)
	}

	if err := repos.Menus.DeleteItem(ctx, createdItem.ID); err != nil {
		t.Fatalf("DeleteItem() error = %v", err)
	}
	if _, err := repos.Menus.GetItem(ctx, createdItem.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetItem() after DeleteItem() error = %v, want sdk.ErrNotFound", err)
	}
}

func TestMenuRepo_GetUnknown(t *testing.T) {
	repos := New().Repositories()
	if _, err := repos.Menus.GetMenu(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetMenu(unknown) error = %v, want sdk.ErrNotFound", err)
	}
	if _, err := repos.Menus.GetMenuBySlug(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetMenuBySlug(unknown) error = %v, want sdk.ErrNotFound", err)
	}
	if _, err := repos.Menus.GetItem(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetItem(unknown) error = %v, want sdk.ErrNotFound", err)
	}
}

func TestMenuRepo_UpdateItemUnknown(t *testing.T) {
	repos := New().Repositories()
	item, _ := menus.NewMenuItem(ids, "menu-1", "Home", "/", "", 0, now)
	if _, err := repos.Menus.UpdateItem(context.Background(), "does-not-exist", item); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("UpdateItem(unknown) error = %v, want sdk.ErrNotFound (documented in menus.MenuRepository)", err)
	}
}

// TestMenuRepo_SlugCollision asserts the port contract turso enforces via
// migrations/0013_menus.sql ("slug TEXT NOT NULL UNIQUE") and MenuRepository's
// doc comment ("slug collision → sdk.ErrAlreadyExists"). (Historical note:
// memstore originally diverged here — silently succeeding — fixed 2026-07-02.)
func TestMenuRepo_SlugCollision(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	m1, _ := menus.NewMenu(ids, "Footer", now)
	if _, err := repos.Menus.CreateMenu(ctx, m1); err != nil {
		t.Fatalf("first CreateMenu() error = %v", err)
	}
	m2, _ := menus.NewMenu(ids, "Footer", now)
	if _, err := repos.Menus.CreateMenu(ctx, m2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("second CreateMenu() with colliding slug error = %v, want sdk.ErrAlreadyExists", err)
	}
}

// --- media.AssetRepository ---

func TestAssetRepo_CreateGetListDelete(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	asset, err := media.NewAsset(ids, "photo.jpg", "image/jpeg", 1024, now)
	if err != nil {
		t.Fatalf("NewAsset() error = %v", err)
	}
	created, err := repos.Media.Create(ctx, asset)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repos.Media.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Filename != "photo.jpg" {
		t.Errorf("Get() Filename = %q, want %q", got.Filename, "photo.jpg")
	}

	list, err := repos.Media.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("List() = %v, want one item with id %q", list, created.ID)
	}

	if err := repos.Media.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repos.Media.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want sdk.ErrNotFound", err)
	}
}

func TestAssetRepo_GetUnknown(t *testing.T) {
	repos := New().Repositories()
	if _, err := repos.Media.Get(context.Background(), "does-not-exist"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want sdk.ErrNotFound", err)
	}
}

// --- messaging.InquiryRepository ---
//
// InquiryRepository (messaging/repository.go) exposes only Create and List —
// no Get or Delete — so this is Create/List only, not the full
// Create/Get/List/Delete set the other four repositories offer.

func TestInquiryRepo_CreateAndList(t *testing.T) {
	ctx := context.Background()
	repos := New().Repositories()

	in, err := messaging.NewInquiry(ids, "Jane Doe", "jane@example.com", "Hello", now)
	if err != nil {
		t.Fatalf("NewInquiry() error = %v", err)
	}
	created, err := repos.Inquiries.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	list, err := repos.Inquiries.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("List() = %v, want one item with id %q", list, created.ID)
	}
}
