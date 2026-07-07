package menussvc

import (
	"context"
	"errors"
	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	"sort"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// fakeMenus is an in-memory MenuRepository.
type fakeMenus struct {
	menus map[string]menus.Menu
	items map[string]menus.MenuItem
}

func newFakeMenus() *fakeMenus {
	return &fakeMenus{menus: map[string]menus.Menu{}, items: map[string]menus.MenuItem{}}
}

func (f *fakeMenus) CreateMenu(ctx context.Context, m menus.Menu) (menus.Menu, error) {
	for _, ex := range f.menus {
		if ex.Slug == m.Slug {
			return menus.Menu{}, errs.ErrAlreadyExists
		}
	}
	f.menus[m.ID] = m
	return m, nil
}
func (f *fakeMenus) GetMenu(ctx context.Context, id string) (menus.Menu, error) {
	m, ok := f.menus[id]
	if !ok {
		return menus.Menu{}, errs.ErrNotFound
	}
	return m, nil
}
func (f *fakeMenus) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	for _, m := range f.menus {
		if m.Slug == slug {
			return m, nil
		}
	}
	return menus.Menu{}, errs.ErrNotFound
}
func (f *fakeMenus) ListMenus(ctx context.Context) ([]menus.Menu, error) {
	var out []menus.Menu
	for _, m := range f.menus {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (f *fakeMenus) ItemsForMenu(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	var out []menus.MenuItem
	for _, it := range f.items {
		if it.MenuID == menuID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentID != out[j].ParentID {
			return out[i].ParentID < out[j].ParentID
		}
		return out[i].Position < out[j].Position
	})
	return out, nil
}
func (f *fakeMenus) AddItem(ctx context.Context, item menus.MenuItem) (menus.MenuItem, error) {
	f.items[item.ID] = item
	return item, nil
}
func (f *fakeMenus) GetItem(ctx context.Context, id string) (menus.MenuItem, error) {
	it, ok := f.items[id]
	if !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	return it, nil
}
func (f *fakeMenus) UpdateItem(ctx context.Context, id string, item menus.MenuItem) (menus.MenuItem, error) {
	if _, ok := f.items[id]; !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	f.items[id] = item
	return item, nil
}
func (f *fakeMenus) DeleteItem(ctx context.Context, id string) error {
	delete(f.items, id)
	return nil
}

func clock(start time.Time) Clock {
	n := 0
	return func() time.Time {
		t := start.Add(time.Duration(n) * time.Second)
		n++
		return t
	}
}

func TestService_MenuFlow(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeMenus(), clock(time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)))

	m, err := svc.CreateMenu(ctx, "Main Menu")
	if err != nil || m.Slug != "main-menu" {
		t.Fatalf("create menu: %v %+v", err, m)
	}

	// Add two items, second nested under the first.
	home, err := svc.AddMenuItem(ctx, m.ID, "Home", "/", "", 0)
	if err != nil {
		t.Fatalf("add home: %v", err)
	}
	_, err = svc.AddMenuItem(ctx, m.ID, "About", "/page/about", home.ID, 1)
	if err != nil {
		t.Fatalf("add about: %v", err)
	}

	items, err := svc.Items(ctx, m.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items: %v %+v", err, items)
	}

	// Edit an item.
	edited, err := svc.EditMenuItem(ctx, home.ID, "Home Page", "/", "", 5)
	if err != nil || edited.Label != "Home Page" || edited.Position != 5 {
		t.Fatalf("edit item: %v %+v", err, edited)
	}

	// Get by slug for the public nav.
	bySlug, err := svc.GetMenuBySlug(ctx, "main-menu")
	if err != nil || bySlug.ID != m.ID {
		t.Fatalf("get by slug: %v %+v", err, bySlug)
	}

	// Remove an item.
	if err := svc.RemoveMenuItem(ctx, home.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if items, _ := svc.Items(ctx, m.ID); len(items) != 1 {
		t.Errorf("expected 1 item after removal, got %d", len(items))
	}
}

func TestService_MenuErrors(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeMenus(), clock(time.Now()))

	if _, err := svc.CreateMenu(ctx, "  "); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("blank name: %v", err)
	}
	m, _ := svc.CreateMenu(ctx, "Footer")
	if _, err := svc.CreateMenu(ctx, "Footer"); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("dup slug: %v", err)
	}
	if _, err := svc.AddMenuItem(ctx, m.ID, "  ", "/x", "", 0); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("blank label: %v", err)
	}
	if _, err := svc.GetMenuItem(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("missing item: %v", err)
	}
}
