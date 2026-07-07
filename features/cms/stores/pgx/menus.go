package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/menus"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// MenuStore implements menus.MenuRepository over a PostgreSQL database.
type MenuStore struct {
	db *pgxdb.DB
}

var _ menus.MenuRepository = (*MenuStore)(nil)

// NewMenuStore returns a MenuStore backed by db.
func NewMenuStore(db *pgxdb.DB) *MenuStore {
	return &MenuStore{db: db}
}

const menuColumns = "id, name, slug, created_at, updated_at"
const itemColumns = "id, menu_id, label, url, parent_id, position, created_at, updated_at"

// CreateMenu persists a new menu.
func (s *MenuStore) CreateMenu(ctx context.Context, m menus.Menu) (menus.Menu, error) {
	const q = `INSERT INTO menus (` + menuColumns + `) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(ctx, q, m.ID, m.Name, m.Slug,
		m.CreatedAt.UTC(), m.UpdatedAt.UTC())
	if err != nil {
		return menus.Menu{}, err
	}
	return m, nil
}

// GetMenu returns the menu with the given id.
func (s *MenuStore) GetMenu(ctx context.Context, id string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE id = $1`
	return scanMenu(s.db.QueryRow(ctx, q, id))
}

// GetMenuBySlug returns the menu with the given slug.
func (s *MenuStore) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE slug = $1`
	return scanMenu(s.db.QueryRow(ctx, q, slug))
}

// ListMenus returns all menus ordered by name.
func (s *MenuStore) ListMenus(ctx context.Context) ([]menus.Menu, error) {
	rows, err := s.db.Query(ctx, `SELECT `+menuColumns+` FROM menus ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []menus.Menu
	for rows.Next() {
		m, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, pgxdb.MapError(rows.Err())
}

// ItemsForMenu returns a menu's items ordered by (parent_id, position).
func (s *MenuStore) ItemsForMenu(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE menu_id = $1 ORDER BY parent_id, position`
	rows, err := s.db.Query(ctx, q, menuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []menus.MenuItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, pgxdb.MapError(rows.Err())
}

// AddItem persists a new menu item.
func (s *MenuStore) AddItem(ctx context.Context, it menus.MenuItem) (menus.MenuItem, error) {
	const q = `INSERT INTO menu_items (` + itemColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := s.db.Exec(ctx, q, it.ID, it.MenuID, it.Label, it.URL, it.ParentID, it.Position,
		it.CreatedAt.UTC(), it.UpdatedAt.UTC())
	if err != nil {
		return menus.MenuItem{}, err
	}
	return it, nil
}

// GetItem returns the item with the given id.
func (s *MenuStore) GetItem(ctx context.Context, id string) (menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE id = $1`
	return scanItem(s.db.QueryRow(ctx, q, id))
}

// UpdateItem persists changes to an item.
func (s *MenuStore) UpdateItem(ctx context.Context, id string, it menus.MenuItem) (menus.MenuItem, error) {
	const q = `UPDATE menu_items SET label=$1, url=$2, parent_id=$3, position=$4, updated_at=$5 WHERE id=$6`
	res, err := s.db.Exec(ctx, q, it.Label, it.URL, it.ParentID, it.Position,
		it.UpdatedAt.UTC(), id)
	if err != nil {
		return menus.MenuItem{}, err
	}
	if res.RowsAffected() == 0 {
		return menus.MenuItem{}, crud.ErrNotFound
	}
	return it, nil
}

// DeleteItem removes an item by id.
func (s *MenuStore) DeleteItem(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM menu_items WHERE id = $1`, id)
	return err
}

func scanMenu(sc scanner) (menus.Menu, error) {
	var (
		m                    menus.Menu
		createdAt, updatedAt time.Time
	)
	if err := sc.Scan(&m.ID, &m.Name, &m.Slug, &createdAt, &updatedAt); err != nil {
		return menus.Menu{}, pgxdb.MapError(err)
	}
	m.CreatedAt = createdAt.UTC()
	m.UpdatedAt = updatedAt.UTC()
	return m, nil
}

func scanItem(sc scanner) (menus.MenuItem, error) {
	var (
		it                   menus.MenuItem
		createdAt, updatedAt time.Time
	)
	if err := sc.Scan(&it.ID, &it.MenuID, &it.Label, &it.URL, &it.ParentID, &it.Position, &createdAt, &updatedAt); err != nil {
		return menus.MenuItem{}, pgxdb.MapError(err)
	}
	it.CreatedAt = createdAt.UTC()
	it.UpdatedAt = updatedAt.UTC()
	return it, nil
}
