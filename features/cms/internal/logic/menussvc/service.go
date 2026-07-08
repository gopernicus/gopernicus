// Package menussvc holds the menus use-case service, kept internal so it is not
// part of the feature's public SemVer surface (plan §5/B3). The public domain
// types and MenuRepository interface stay in package menus.
package menussvc

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
)

// Clock returns the current time. Injected so tests can pin timestamps.
type Clock func() time.Time

// Service implements the menu use cases over a menus.MenuRepository.
type Service struct {
	repo  menus.MenuRepository
	clock Clock
}

// NewService constructs a Service. A nil clock defaults to time.Now.
func NewService(repo menus.MenuRepository, clock Clock) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{repo: repo, clock: clock}
}

// CreateMenu validates and persists a new menu.
func (s *Service) CreateMenu(ctx context.Context, name string) (menus.Menu, error) {
	m, err := menus.NewMenu(name, s.clock())
	if err != nil {
		return menus.Menu{}, err
	}
	return s.repo.CreateMenu(ctx, m)
}

// GetMenu returns the menu with the given id, or errs.ErrNotFound.
func (s *Service) GetMenu(ctx context.Context, id string) (menus.Menu, error) {
	return s.repo.GetMenu(ctx, id)
}

// GetMenuBySlug returns the menu with the given slug, or errs.ErrNotFound.
func (s *Service) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	return s.repo.GetMenuBySlug(ctx, slug)
}

// ListMenus returns all menus ordered by name.
func (s *Service) ListMenus(ctx context.Context) ([]menus.Menu, error) {
	return s.repo.ListMenus(ctx)
}

// Items returns a menu's items ordered for tree/nav display.
func (s *Service) Items(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	return s.repo.ItemsForMenu(ctx, menuID)
}

// AddMenuItem validates and appends an item to a menu.
func (s *Service) AddMenuItem(ctx context.Context, menuID, label, url, parentID string, position int) (menus.MenuItem, error) {
	item, err := menus.NewMenuItem(menuID, label, url, parentID, position, s.clock())
	if err != nil {
		return menus.MenuItem{}, err
	}
	return s.repo.AddItem(ctx, item)
}

// GetMenuItem returns the item with the given id, or errs.ErrNotFound.
func (s *Service) GetMenuItem(ctx context.Context, id string) (menus.MenuItem, error) {
	return s.repo.GetItem(ctx, id)
}

// EditMenuItem loads the item, applies the edit, and persists it.
func (s *Service) EditMenuItem(ctx context.Context, id, label, url, parentID string, position int) (menus.MenuItem, error) {
	item, err := s.repo.GetItem(ctx, id)
	if err != nil {
		return menus.MenuItem{}, err
	}
	if err := item.ApplyEdit(label, url, parentID, position, s.clock()); err != nil {
		return menus.MenuItem{}, err
	}
	return s.repo.UpdateItem(ctx, id, item)
}

// RemoveMenuItem removes an item by id.
func (s *Service) RemoveMenuItem(ctx context.Context, id string) error {
	return s.repo.DeleteItem(ctx, id)
}
