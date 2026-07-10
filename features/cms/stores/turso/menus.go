package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk/crud"
)

// MenuStore implements menus.MenuRepository over a libSQL database.
type MenuStore struct {
	db *tursodb.DB
}

var _ menus.MenuRepository = (*MenuStore)(nil)

// NewMenuStore returns a MenuStore backed by db.
func NewMenuStore(db *tursodb.DB) *MenuStore {
	return &MenuStore{db: db}
}

const menuColumns = "id, name, slug, created_at, updated_at"
const itemColumns = "id, menu_id, label, url, parent_id, position, created_at, updated_at"

// menuRow is the store-local, db-tagged projection of a menus row.
type menuRow struct {
	ID        string       `db:"id"`
	Name      string       `db:"name"`
	Slug      string       `db:"slug"`
	CreatedAt tursodb.Time `db:"created_at"`
	UpdatedAt tursodb.Time `db:"updated_at"`
}

func (r menuRow) toDomain() menus.Menu {
	return menus.Menu{
		ID:        r.ID,
		Name:      r.Name,
		Slug:      r.Slug,
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
}

// menuItemRow is the store-local, db-tagged projection of a menu_items row.
type menuItemRow struct {
	ID        string       `db:"id"`
	MenuID    string       `db:"menu_id"`
	Label     string       `db:"label"`
	URL       string       `db:"url"`
	ParentID  string       `db:"parent_id"`
	Position  int          `db:"position"`
	CreatedAt tursodb.Time `db:"created_at"`
	UpdatedAt tursodb.Time `db:"updated_at"`
}

func (r menuItemRow) toDomain() menus.MenuItem {
	return menus.MenuItem{
		ID:        r.ID,
		MenuID:    r.MenuID,
		Label:     r.Label,
		URL:       r.URL,
		ParentID:  r.ParentID,
		Position:  r.Position,
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
}

// CreateMenu persists a new menu.
func (s *MenuStore) CreateMenu(ctx context.Context, m menus.Menu) (menus.Menu, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if m.ID == "" {
		const q = `INSERT INTO menus (name, slug, created_at, updated_at) VALUES (?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q, m.Name, m.Slug,
			tursodb.FormatTime(m.CreatedAt), tursodb.FormatTime(m.UpdatedAt)).Scan(&m.ID); err != nil {
			return menus.Menu{}, tursodb.MapError(err)
		}
		return m, nil
	}
	const q = `INSERT INTO menus (` + menuColumns + `) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, m.ID, m.Name, m.Slug,
		tursodb.FormatTime(m.CreatedAt), tursodb.FormatTime(m.UpdatedAt))
	if err != nil {
		return menus.Menu{}, err
	}
	return m, nil
}

// GetMenu returns the menu with the given id.
func (s *MenuStore) GetMenu(ctx context.Context, id string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE id = ?`
	row, err := queryOne[menuRow](ctx, s.db, q, id)
	if err != nil {
		return menus.Menu{}, err
	}
	return row.toDomain(), nil
}

// GetMenuBySlug returns the menu with the given slug.
func (s *MenuStore) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE slug = ?`
	row, err := queryOne[menuRow](ctx, s.db, q, slug)
	if err != nil {
		return menus.Menu{}, err
	}
	return row.toDomain(), nil
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
		row, err := tursodb.ScanStruct[menuRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	return out, tursodb.MapError(rows.Err())
}

// ItemsForMenu returns a menu's items ordered by (parent_id, position).
func (s *MenuStore) ItemsForMenu(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE menu_id = ? ORDER BY parent_id, position`
	rows, err := s.db.Query(ctx, q, menuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []menus.MenuItem
	for rows.Next() {
		row, err := tursodb.ScanStruct[menuItemRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	return out, tursodb.MapError(rows.Err())
}

// AddItem persists a new menu item.
func (s *MenuStore) AddItem(ctx context.Context, it menus.MenuItem) (menus.MenuItem, error) {
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if it.ID == "" {
		const q = `INSERT INTO menu_items (menu_id, label, url, parent_id, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q, it.MenuID, it.Label, it.URL, it.ParentID, it.Position,
			tursodb.FormatTime(it.CreatedAt), tursodb.FormatTime(it.UpdatedAt)).Scan(&it.ID); err != nil {
			return menus.MenuItem{}, tursodb.MapError(err)
		}
		return it, nil
	}
	const q = `INSERT INTO menu_items (` + itemColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, it.ID, it.MenuID, it.Label, it.URL, it.ParentID, it.Position,
		tursodb.FormatTime(it.CreatedAt), tursodb.FormatTime(it.UpdatedAt))
	if err != nil {
		return menus.MenuItem{}, err
	}
	return it, nil
}

// GetItem returns the item with the given id.
func (s *MenuStore) GetItem(ctx context.Context, id string) (menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE id = ?`
	row, err := queryOne[menuItemRow](ctx, s.db, q, id)
	if err != nil {
		return menus.MenuItem{}, err
	}
	return row.toDomain(), nil
}

// UpdateItem persists changes to an item.
func (s *MenuStore) UpdateItem(ctx context.Context, id string, it menus.MenuItem) (menus.MenuItem, error) {
	const q = `UPDATE menu_items SET label=?, url=?, parent_id=?, position=?, updated_at=? WHERE id=?`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, it.Label, it.URL, it.ParentID, it.Position,
		tursodb.FormatTime(it.UpdatedAt), id)
	if err != nil {
		return menus.MenuItem{}, err
	}
	if n == 0 {
		return menus.MenuItem{}, crud.ErrNotFound
	}
	return it, nil
}

// DeleteItem removes an item by id.
func (s *MenuStore) DeleteItem(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM menu_items WHERE id = ?`, id)
	return err
}
