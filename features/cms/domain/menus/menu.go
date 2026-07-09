// Package menus is the bounded context for navigation: named menus (main,
// footer, …) each holding a tree of menu items. Items carry a label and a URL;
// URL resolution to pages/posts is a view-time/admin convenience, so this
// domain stays free of other domains.
package menus

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/slug"
)

// Menu is a named navigation menu (the aggregate root for its items).
type Menu struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MenuItem is one entry in a menu, nestable via ParentID and ordered by
// Position. URL may be empty for a non-link heading.
type MenuItem struct {
	ID        string
	MenuID    string
	Label     string
	URL       string
	ParentID  string
	Position  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewMenu validates the name, derives a slug, mints its ID from ids (empty under
// cryptids.Database — the store then assigns the key), and returns a new Menu.
func NewMenu(ids cryptids.IDGenerator, name string, now time.Time) (Menu, error) {
	name = strings.TrimSpace(name)
	if err := requireName(name); err != nil {
		return Menu{}, err
	}
	now = now.UTC()
	return Menu{
		ID:        ids.MustGenerate(),
		Name:      name,
		Slug:      slug.Make(name),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Rename updates the menu name and slug.
func (m *Menu) Rename(name string, now time.Time) error {
	name = strings.TrimSpace(name)
	if err := requireName(name); err != nil {
		return err
	}
	m.Name = name
	m.Slug = slug.Make(name)
	m.UpdatedAt = now.UTC()
	return nil
}

// NewMenuItem validates and returns a new item belonging to menuID, minting its
// ID from ids (empty under cryptids.Database — the store then assigns the key).
func NewMenuItem(ids cryptids.IDGenerator, menuID, label, url, parentID string, position int, now time.Time) (MenuItem, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return MenuItem{}, fmt.Errorf("label is required: %w", errs.ErrInvalidInput)
	}
	now = now.UTC()
	return MenuItem{
		ID:        ids.MustGenerate(),
		MenuID:    menuID,
		Label:     label,
		URL:       strings.TrimSpace(url),
		ParentID:  strings.TrimSpace(parentID),
		Position:  position,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ApplyEdit updates the mutable fields of an item in place.
func (i *MenuItem) ApplyEdit(label, url, parentID string, position int, now time.Time) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("label is required: %w", errs.ErrInvalidInput)
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == i.ID {
		parentID = ""
	}
	i.Label = label
	i.URL = strings.TrimSpace(url)
	i.ParentID = parentID
	i.Position = position
	i.UpdatedAt = now.UTC()
	return nil
}

func requireName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required: %w", errs.ErrInvalidInput)
	}
	if slug.Make(name) == "" {
		return fmt.Errorf("name must contain an alphanumeric character: %w", errs.ErrInvalidInput)
	}
	return nil
}
