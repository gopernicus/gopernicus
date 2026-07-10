package content

import (
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/slug"
)

// Entry is the single dynamic content record on the frozen spine (plan §2/§3).
// Every piece of content — Articles, Pages, and host-registered custom types —
// is an Entry distinguished by Type. The spine columns (Title…MenuOrder) are
// universal and queryable; per-type custom data lives in Fields (EAV-backed).
// You query the spine; you render the fields by key.
type Entry struct {
	ID          string
	Type        string // content type slug
	Slug        string
	Title       string
	Status      Status // reuses the existing draft/published VO
	Body        string // raw markdown (universal long-form field)
	Excerpt     string
	Author      string
	Template    string // which registered template renders it
	ParentID    string // hierarchy (Hierarchical types); "" otherwise
	MenuOrder   int
	PublishedAt *time.Time // set the first time published
	Fields      Fields     // custom fields (EAV-backed)
	TermIDs     []string   // taxonomy associations; loaded on Get
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewEntry validates the spine inputs, generates a slug, mints its ID from ids
// (empty under cryptids.Database — the store then assigns the key), and returns a
// new Entry of type typeSlug stamped with now. An empty status defaults to
// draft; creating directly as published stamps PublishedAt. An empty template
// defaults to "default". Fields and hierarchy are applied by the caller (the
// service) after Registry field validation. Validation failures wrap
// sdk.ErrInvalidInput.
func NewEntry(ids cryptids.IDGenerator, typeSlug, title, excerpt, body, author string, status Status, template string, now time.Time) (Entry, error) {
	title = strings.TrimSpace(title)
	if status == "" {
		status = StatusDraft
	}
	if err := validate(title, status); err != nil {
		return Entry{}, err
	}
	if template == "" {
		template = "default"
	}

	now = now.UTC()
	e := Entry{
		ID:        ids.MustGenerate(),
		Type:      typeSlug,
		Slug:      slug.Make(title),
		Title:     title,
		Status:    status,
		Body:      body,
		Excerpt:   strings.TrimSpace(excerpt),
		Author:    strings.TrimSpace(author),
		Template:  template,
		Fields:    Fields{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if status == StatusPublished {
		e.PublishedAt = &now
	}
	return e, nil
}

// ApplyEdit updates the mutable spine fields in place, re-slugifies from the new
// title, and bumps UpdatedAt. Transitioning into published stamps PublishedAt
// the first time. Fields and hierarchy are applied separately by the caller.
// Validation failures leave the entry unchanged.
func (e *Entry) ApplyEdit(title, excerpt, body, author string, status Status, template string, now time.Time) error {
	title = strings.TrimSpace(title)
	if status == "" {
		status = e.Status
	}
	if err := validate(title, status); err != nil {
		return err
	}
	if template == "" {
		template = "default"
	}

	now = now.UTC()
	e.Title = title
	e.Slug = slug.Make(title)
	e.Excerpt = strings.TrimSpace(excerpt)
	e.Body = body
	e.Author = strings.TrimSpace(author)
	e.Template = template
	e.Status = status
	if status == StatusPublished && e.PublishedAt == nil {
		e.PublishedAt = &now
	}
	e.UpdatedAt = now
	return nil
}

// SetHierarchy sets the entry's parent and ordering (Hierarchical types only).
// A self-parent is dropped to top-level. The caller gates this on the type's
// Hierarchical capability.
func (e *Entry) SetHierarchy(parentID string, menuOrder int) {
	parentID = strings.TrimSpace(parentID)
	if parentID == e.ID {
		parentID = ""
	}
	e.ParentID = parentID
	e.MenuOrder = menuOrder
}

// Publish marks the entry published, stamping PublishedAt the first time.
func (e *Entry) Publish(now time.Time) {
	now = now.UTC()
	e.Status = StatusPublished
	if e.PublishedAt == nil {
		e.PublishedAt = &now
	}
	e.UpdatedAt = now
}

// Unpublish returns the entry to draft. PublishedAt is preserved as history.
func (e *Entry) Unpublish(now time.Time) {
	e.Status = StatusDraft
	e.UpdatedAt = now.UTC()
}
