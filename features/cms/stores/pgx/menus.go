package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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

// menuRow is the store-local, db-tagged projection of a menus row.
type menuRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Slug      string    `db:"slug"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r menuRow) toDomain() menus.Menu {
	return menus.Menu{
		ID:        r.ID,
		Name:      r.Name,
		Slug:      r.Slug,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// menuItemRow is the store-local, db-tagged projection of a menu_items row.
type menuItemRow struct {
	ID        string    `db:"id"`
	MenuID    string    `db:"menu_id"`
	Label     string    `db:"label"`
	URL       string    `db:"url"`
	ParentID  string    `db:"parent_id"`
	Position  int       `db:"position"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r menuItemRow) toDomain() menus.MenuItem {
	return menus.MenuItem{
		ID:        r.ID,
		MenuID:    r.MenuID,
		Label:     r.Label,
		URL:       r.URL,
		ParentID:  r.ParentID,
		Position:  r.Position,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// CreateMenu persists a new menu.
func (s *MenuStore) CreateMenu(ctx context.Context, m menus.Menu) (menus.Menu, error) {
	args := pgx.NamedArgs{
		"name":       m.Name,
		"slug":       m.Slug,
		"created_at": m.CreatedAt.UTC(),
		"updated_at": m.UpdatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if m.ID == "" {
		const q = `INSERT INTO menus (name, slug, created_at, updated_at)
			VALUES (@name, @slug, @created_at, @updated_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&m.ID); err != nil {
			return menus.Menu{}, pgxdb.MapError(err)
		}
		return m, nil
	}
	const q = `INSERT INTO menus (` + menuColumns + `)
		VALUES (@id, @name, @slug, @created_at, @updated_at)`
	args["id"] = m.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return menus.Menu{}, err
	}
	return m, nil
}

// GetMenu returns the menu with the given id.
func (s *MenuStore) GetMenu(ctx context.Context, id string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE id = @id`
	row, err := queryOne[menuRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return menus.Menu{}, err
	}
	return row.toDomain(), nil
}

// GetMenuBySlug returns the menu with the given slug.
func (s *MenuStore) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	const q = `SELECT ` + menuColumns + ` FROM menus WHERE slug = @slug`
	row, err := queryOne[menuRow](ctx, s.db, q, pgx.NamedArgs{"slug": slug})
	if err != nil {
		return menus.Menu{}, err
	}
	return row.toDomain(), nil
}

// ListMenus returns all menus ordered by name.
func (s *MenuStore) ListMenus(ctx context.Context) ([]menus.Menu, error) {
	rows, err := s.db.Query(ctx, `SELECT `+menuColumns+` FROM menus ORDER BY name`)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	mrows, err := pgx.CollectRows(rows, pgx.RowToStructByName[menuRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	var out []menus.Menu
	for _, m := range mrows {
		out = append(out, m.toDomain())
	}
	return out, nil
}

// ItemsForMenu returns a menu's items ordered by (parent_id, position).
func (s *MenuStore) ItemsForMenu(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE menu_id = @menu_id ORDER BY parent_id, position`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"menu_id": menuID})
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	irows, err := pgx.CollectRows(rows, pgx.RowToStructByName[menuItemRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	var out []menus.MenuItem
	for _, it := range irows {
		out = append(out, it.toDomain())
	}
	return out, nil
}

// AddItem persists a new menu item.
func (s *MenuStore) AddItem(ctx context.Context, it menus.MenuItem) (menus.MenuItem, error) {
	args := pgx.NamedArgs{
		"menu_id":    it.MenuID,
		"label":      it.Label,
		"url":        it.URL,
		"parent_id":  it.ParentID,
		"position":   it.Position,
		"created_at": it.CreatedAt.UTC(),
		"updated_at": it.UpdatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if it.ID == "" {
		const q = `INSERT INTO menu_items (menu_id, label, url, parent_id, position, created_at, updated_at)
			VALUES (@menu_id, @label, @url, @parent_id, @position, @created_at, @updated_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&it.ID); err != nil {
			return menus.MenuItem{}, pgxdb.MapError(err)
		}
		return it, nil
	}
	const q = `INSERT INTO menu_items (` + itemColumns + `)
		VALUES (@id, @menu_id, @label, @url, @parent_id, @position, @created_at, @updated_at)`
	args["id"] = it.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return menus.MenuItem{}, err
	}
	return it, nil
}

// GetItem returns the item with the given id.
func (s *MenuStore) GetItem(ctx context.Context, id string) (menus.MenuItem, error) {
	const q = `SELECT ` + itemColumns + ` FROM menu_items WHERE id = @id`
	row, err := queryOne[menuItemRow](ctx, s.db, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return menus.MenuItem{}, err
	}
	return row.toDomain(), nil
}

// UpdateItem persists changes to an item.
func (s *MenuStore) UpdateItem(ctx context.Context, id string, it menus.MenuItem) (menus.MenuItem, error) {
	const q = `UPDATE menu_items SET label = @label, url = @url, parent_id = @parent_id, position = @position, updated_at = @updated_at WHERE id = @id`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{
		"label":      it.Label,
		"url":        it.URL,
		"parent_id":  it.ParentID,
		"position":   it.Position,
		"updated_at": it.UpdatedAt.UTC(),
		"id":         id,
	})
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
	_, err := s.db.Exec(ctx, `DELETE FROM menu_items WHERE id = @id`, pgx.NamedArgs{"id": id})
	return err
}
