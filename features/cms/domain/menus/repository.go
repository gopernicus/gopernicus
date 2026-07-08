package menus

import "context"

// MenuRepository is the app port for persisting menus and their items.
// Implemented by feature store adapters (features/cms/stores/turso) or any
// host-provided implementation (see examples/minimal's memstore).
type MenuRepository interface {
	// CreateMenu persists a new menu; slug collision → errs.ErrAlreadyExists.
	CreateMenu(ctx context.Context, m Menu) (Menu, error)
	// GetMenu returns the menu with the given id, or errs.ErrNotFound.
	GetMenu(ctx context.Context, id string) (Menu, error)
	// GetMenuBySlug returns the menu with the given slug, or errs.ErrNotFound.
	GetMenuBySlug(ctx context.Context, slug string) (Menu, error)
	// ListMenus returns all menus ordered by name.
	ListMenus(ctx context.Context) ([]Menu, error)

	// ItemsForMenu returns a menu's items ordered by (parent, position).
	ItemsForMenu(ctx context.Context, menuID string) ([]MenuItem, error)
	// AddItem persists a new item.
	AddItem(ctx context.Context, item MenuItem) (MenuItem, error)
	// GetItem returns the item with the given id, or errs.ErrNotFound.
	GetItem(ctx context.Context, id string) (MenuItem, error)
	// UpdateItem persists changes; missing id → errs.ErrNotFound.
	UpdateItem(ctx context.Context, id string, item MenuItem) (MenuItem, error)
	// DeleteItem removes an item by id.
	DeleteItem(ctx context.Context, id string) error
}
