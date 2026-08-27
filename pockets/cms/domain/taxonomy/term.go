// Package taxonomy is the bounded context for classifying content: categories
// (hierarchical) and tags (flat). Other domains reference terms by ID only.
package taxonomy

import (
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/slug"
)

// Kind distinguishes hierarchical categories from flat tags.
type Kind string

const (
	KindCategory Kind = "category"
	KindTag      Kind = "tag"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindCategory || k == KindTag }

// Term is a classification term — a category or a tag. Categories may nest via
// ParentID; tags are always flat.
type Term struct {
	ID        string
	Kind      Kind
	Slug      string
	Name      string
	ParentID  string // categories only; empty otherwise
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewTerm validates inputs, generates a slug, mints its ID from ids (empty under
// cryptids.Database — the store then assigns the key), and returns a new Term.
// ParentID is ignored for tags. Validation failures wrap sdk.ErrInvalidInput.
func NewTerm(ids cryptids.IDGenerator, kind Kind, name, parentID string, now time.Time) (Term, error) {
	name = strings.TrimSpace(name)
	if err := validate(kind, name); err != nil {
		return Term{}, err
	}
	if kind != KindCategory {
		parentID = ""
	}

	now = now.UTC()
	return Term{
		ID:        ids.MustGenerate(),
		Kind:      kind,
		Slug:      slug.Make(name),
		Name:      name,
		ParentID:  strings.TrimSpace(parentID),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ApplyEdit updates the mutable fields in place, re-slugifies, and bumps
// UpdatedAt. A term may not be its own parent; tags stay flat.
func (t *Term) ApplyEdit(name, parentID string, now time.Time) error {
	name = strings.TrimSpace(name)
	if err := validate(t.Kind, name); err != nil {
		return err
	}
	if t.Kind != KindCategory {
		parentID = ""
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == t.ID {
		parentID = ""
	}

	t.Name = name
	t.Slug = slug.Make(name)
	t.ParentID = parentID
	t.UpdatedAt = now.UTC()
	return nil
}

func validate(kind Kind, name string) error {
	if !kind.Valid() {
		return fmt.Errorf("invalid kind %q: %w", kind, sdk.ErrInvalidInput)
	}
	if name == "" {
		return fmt.Errorf("name is required: %w", sdk.ErrInvalidInput)
	}
	if slug.Make(name) == "" {
		return fmt.Errorf("name must contain an alphanumeric character: %w", sdk.ErrInvalidInput)
	}
	return nil
}
