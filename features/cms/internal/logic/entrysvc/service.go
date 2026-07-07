// Package entrysvc holds the content use-case service for the Registry model:
// one generic service over content.EntryRepository, driven by a *content.Registry
// for field validation. It replaces the per-type contentsvc (post + page) — all
// content types share this one service. Kept internal so it is NOT part of the
// feature's public SemVer surface (plan §5/B3); hosts compose through
// cms.Register, not by importing the service.
package entrysvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/content"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// Clock returns the current time. Injected so tests can pin timestamps.
type Clock func() time.Time

// Input is the host-agnostic write payload for an entry: the spine fields plus
// the custom Fields bag and hierarchy hints. The service validates Fields
// against the type's FieldDefs and gates hierarchy on the type's Hierarchical
// capability.
type Input struct {
	Title     string
	Excerpt   string
	Body      string
	Author    string
	Status    content.Status
	Template  string
	ParentID  string
	MenuOrder int
	Fields    content.Fields
}

// Service implements the content use cases over a content.EntryRepository,
// driven by a Registry. It accepts the repository interface and returns concrete
// structs (accept interfaces, return structs).
type Service struct {
	entries  content.EntryRepository
	registry *content.Registry
	clock    Clock
}

// NewService constructs a Service. A nil clock defaults to time.Now.
func NewService(entries content.EntryRepository, registry *content.Registry, clock Clock) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{entries: entries, registry: registry, clock: clock}
}

// Create validates and persists a new entry of the given type.
func (s *Service) Create(ctx context.Context, typeSlug string, in Input) (content.Entry, error) {
	ct, ok := s.registry.Type(typeSlug)
	if !ok {
		return content.Entry{}, fmt.Errorf("content type %q not registered: %w", typeSlug, errs.ErrNotFound)
	}

	e, err := content.NewEntry(typeSlug, in.Title, in.Excerpt, in.Body, in.Author, in.Status, in.Template, s.clock())
	if err != nil {
		return content.Entry{}, err
	}
	if !ct.SupportsTemplate(e.Template) {
		return content.Entry{}, fmt.Errorf("content type %q does not support template %q: %w", typeSlug, e.Template, errs.ErrInvalidInput)
	}
	if ct.Hierarchical {
		e.SetHierarchy(in.ParentID, in.MenuOrder)
	}
	if e.Fields, err = s.validateFields(ctx, typeSlug, in.Fields); err != nil {
		return content.Entry{}, err
	}
	return s.entries.Create(ctx, e)
}

// Edit loads the entry, applies the spine edit and revalidated fields, and
// persists it.
func (s *Service) Edit(ctx context.Context, id string, in Input) (content.Entry, error) {
	e, err := s.entries.Get(ctx, id)
	if err != nil {
		return content.Entry{}, err
	}
	ct, ok := s.registry.Type(e.Type)
	if !ok {
		return content.Entry{}, fmt.Errorf("content type %q not registered: %w", e.Type, errs.ErrNotFound)
	}
	if err := e.ApplyEdit(in.Title, in.Excerpt, in.Body, in.Author, in.Status, in.Template, s.clock()); err != nil {
		return content.Entry{}, err
	}
	if !ct.SupportsTemplate(e.Template) {
		return content.Entry{}, fmt.Errorf("content type %q does not support template %q: %w", e.Type, e.Template, errs.ErrInvalidInput)
	}
	if ct.Hierarchical {
		e.SetHierarchy(in.ParentID, in.MenuOrder)
	}
	if e.Fields, err = s.validateFields(ctx, e.Type, in.Fields); err != nil {
		return content.Entry{}, err
	}
	return s.entries.Update(ctx, id, e)
}

// Get returns the entry with the given id, or errs.ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (content.Entry, error) {
	return s.entries.Get(ctx, id)
}

// GetBySlug returns the entry of type typ with the given slug, or
// errs.ErrNotFound.
func (s *Service) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	return s.entries.GetBySlug(ctx, typ, slug)
}

// List returns a cursor-paginated page of entries matching q.
func (s *Service) List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	return s.entries.List(ctx, q)
}

// ListByTerm returns a cursor-paginated page of entries matching q associated
// with termID.
func (s *Service) ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	return s.entries.ListByTerm(ctx, termID, q)
}

// Publish loads the entry, marks it published, and persists it.
func (s *Service) Publish(ctx context.Context, id string) (content.Entry, error) {
	e, err := s.entries.Get(ctx, id)
	if err != nil {
		return content.Entry{}, err
	}
	e.Publish(s.clock())
	return s.entries.Update(ctx, id, e)
}

// Unpublish loads the entry, returns it to draft, and persists it.
func (s *Service) Unpublish(ctx context.Context, id string) (content.Entry, error) {
	e, err := s.entries.Get(ctx, id)
	if err != nil {
		return content.Entry{}, err
	}
	e.Unpublish(s.clock())
	return s.entries.Update(ctx, id, e)
}

// Delete removes the entry.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.entries.Delete(ctx, id)
}

// SetTerms replaces the entry's taxonomy associations.
func (s *Service) SetTerms(ctx context.Context, entryID string, termIDs []string) error {
	return s.entries.SetTerms(ctx, entryID, termIDs)
}

// validateFields runs the Registry's schema validation (required + kind
// coercion) and then verifies relation/image targets resolve to existing
// entries of the declared RelTo type. Relation integrity that the pure Registry
// cannot check (existence) is enforced here, where the store is in reach.
func (s *Service) validateFields(ctx context.Context, typeSlug string, in content.Fields) (content.Fields, error) {
	out, err := s.registry.ValidateFields(typeSlug, in)
	if err != nil {
		return nil, err
	}
	ct, _ := s.registry.Type(typeSlug)
	for _, def := range ct.Fields {
		if def.Kind != content.KindRelation {
			continue
		}
		targetID := out[def.Key].Raw
		if targetID == "" {
			continue
		}
		target, err := s.entries.Get(ctx, targetID)
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return nil, fmt.Errorf("relation field %q points to missing entry %q: %w", def.Key, targetID, errs.ErrInvalidInput)
			}
			return nil, err
		}
		if target.Type != def.RelTo {
			return nil, fmt.Errorf("relation field %q must point to a %q, got %q: %w", def.Key, def.RelTo, target.Type, errs.ErrInvalidInput)
		}
	}
	return out, nil
}
