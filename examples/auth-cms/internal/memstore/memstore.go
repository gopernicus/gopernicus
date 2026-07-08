// Package memstore is an in-memory implementation of the CMS feature's
// repositories. It exists to prove the load-bearing property of the feature
// module split (plan §2): a host can run features/cms with a store of its own
// choosing, so this module's graph pulls NO libsql — only features/cms (+ its
// theme deps) and sdk. It is a reference "bring your own store", not a
// production datastore: data lives in maps and is lost on exit.
//
// NOTE: this package is a verbatim copy of examples/minimal/internal/memstore —
// a host never reaches into another example's internal/ packages, so the
// two-feature proof host owns its own copy of the cms store. Keep it in sync
// with the source if the cms ports change.
//
// The six repository ports reuse method names (Get/Create/List/Update/Delete)
// with different entity types, so one Go type cannot satisfy all of them. Each
// port is therefore a thin value over a shared *data holder.
package memstore

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/features/cms"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// orderField is the keyset order column entries paginate by; it must match the
// cursor's order field so a stale cursor from a different sort is ignored.
const orderField = "created_at"

// data holds every CMS entity in maps behind one mutex.
type data struct {
	mu         sync.RWMutex
	entries    map[string]content.Entry
	entryTerms map[string][]string
	terms      map[string]taxonomy.Term
	menus      map[string]menus.Menu
	items      map[string]menus.MenuItem
	assets     map[string]media.Asset
	inquiries  map[string]messaging.Inquiry
}

// Store is an in-memory CMS datastore. Its Repositories method yields the port
// set features/cms needs.
type Store struct{ d *data }

// New returns an empty Store.
func New() *Store {
	return &Store{d: &data{
		entries:    map[string]content.Entry{},
		entryTerms: map[string][]string{},
		terms:      map[string]taxonomy.Term{},
		menus:      map[string]menus.Menu{},
		items:      map[string]menus.MenuItem{},
		assets:     map[string]media.Asset{},
		inquiries:  map[string]messaging.Inquiry{},
	}}
}

// Repositories bundles the per-port views as the feature's repository set.
func (s *Store) Repositories() cms.Repositories {
	return cms.Repositories{
		Entries:   entryRepo{s.d},
		Terms:     termRepo{s.d},
		Menus:     menuRepo{s.d},
		Media:     assetRepo{s.d},
		Inquiries: inquiryRepo{s.d},
	}
}

// --- content.EntryRepository ---

type entryRepo struct{ *data }

func (r entryRepo) Create(_ context.Context, e content.Entry) (content.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.entries {
		if ex.Type == e.Type && ex.Slug == e.Slug {
			return content.Entry{}, errs.ErrAlreadyExists
		}
	}
	r.entries[e.ID] = e
	return e, nil
}

func (r entryRepo) Update(_ context.Context, id string, e content.Entry) (content.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	r.entries[id] = e
	return e, nil
}

func (r entryRepo) Get(_ context.Context, id string) (content.Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	e.TermIDs = append([]string(nil), r.entryTerms[id]...)
	return e, nil
}

func (r entryRepo) GetBySlug(_ context.Context, typ, slug string) (content.Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if e.Type == typ && e.Slug == slug {
			e.TermIDs = append([]string(nil), r.entryTerms[e.ID]...)
			return e, nil
		}
	}
	return content.Entry{}, errs.ErrNotFound
}

func (r entryRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.entries, id)
	delete(r.entryTerms, id)
	return nil
}

func (r entryRepo) List(_ context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []content.Entry
	for _, e := range r.entries {
		if e.Type == q.Type && (q.Status == "" || e.Status == q.Status) {
			all = append(all, e)
		}
	}
	return entryPageOf(all, q.ListRequest)
}

func (r entryRepo) ListByTerm(_ context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var matched []content.Entry
	for id, e := range r.entries {
		if e.Type == q.Type && (q.Status == "" || e.Status == q.Status) && slices.Contains(r.entryTerms[id], termID) {
			matched = append(matched, e)
		}
	}
	return entryPageOf(matched, q.ListRequest)
}

func (r entryRepo) SetTerms(_ context.Context, entryID string, termIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entryTerms[entryID] = append([]string(nil), termIDs...)
	return nil
}

// --- taxonomy.TermRepository ---

type termRepo struct{ *data }

func (r termRepo) Get(_ context.Context, id string) (taxonomy.Term, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.terms[id]
	if !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	return t, nil
}

func (r termRepo) GetBySlug(_ context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.terms {
		if t.Kind == kind && t.Slug == slug {
			return t, nil
		}
	}
	return taxonomy.Term{}, errs.ErrNotFound
}

func (r termRepo) ListByKind(_ context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []taxonomy.Term
	for _, t := range r.terms {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r termRepo) Create(_ context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.terms {
		if ex.Kind == t.Kind && ex.Slug == t.Slug {
			return taxonomy.Term{}, errs.ErrAlreadyExists
		}
	}
	r.terms[t.ID] = t
	return t, nil
}

func (r termRepo) Update(_ context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.terms[id]; !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	r.terms[id] = t
	return t, nil
}

func (r termRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.terms, id)
	return nil
}

// --- menus.MenuRepository ---

type menuRepo struct{ *data }

func (r menuRepo) CreateMenu(_ context.Context, m menus.Menu) (menus.Menu, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.menus {
		if ex.Slug == m.Slug {
			return menus.Menu{}, errs.ErrAlreadyExists
		}
	}
	r.menus[m.ID] = m
	return m, nil
}

func (r menuRepo) GetMenu(_ context.Context, id string) (menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.menus[id]
	if !ok {
		return menus.Menu{}, errs.ErrNotFound
	}
	return m, nil
}

func (r menuRepo) GetMenuBySlug(_ context.Context, slug string) (menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.menus {
		if m.Slug == slug {
			return m, nil
		}
	}
	return menus.Menu{}, errs.ErrNotFound
}

func (r menuRepo) ListMenus(_ context.Context) ([]menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]menus.Menu, 0, len(r.menus))
	for _, m := range r.menus {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r menuRepo) ItemsForMenu(_ context.Context, menuID string) ([]menus.MenuItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []menus.MenuItem
	for _, it := range r.items {
		if it.MenuID == menuID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, nil
}

func (r menuRepo) AddItem(_ context.Context, item menus.MenuItem) (menus.MenuItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = item
	return item, nil
}

func (r menuRepo) GetItem(_ context.Context, id string) (menus.MenuItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.items[id]
	if !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	return it, nil
}

func (r menuRepo) UpdateItem(_ context.Context, id string, item menus.MenuItem) (menus.MenuItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	r.items[id] = item
	return item, nil
}

func (r menuRepo) DeleteItem(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
	return nil
}

// --- media.AssetRepository ---

type assetRepo struct{ *data }

func (r assetRepo) Create(_ context.Context, a media.Asset) (media.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assets[a.ID] = a
	return a, nil
}

func (r assetRepo) Get(_ context.Context, id string) (media.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.assets[id]
	if !ok {
		return media.Asset{}, errs.ErrNotFound
	}
	return a, nil
}

func (r assetRepo) List(_ context.Context) ([]media.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]media.Asset, 0, len(r.assets))
	for _, a := range r.assets {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r assetRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assets, id)
	return nil
}

// --- messaging.InquiryRepository ---

type inquiryRepo struct{ *data }

func (r inquiryRepo) Create(_ context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inquiries[in.ID] = in
	return in, nil
}

func (r inquiryRepo) List(_ context.Context) ([]messaging.Inquiry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]messaging.Inquiry, 0, len(r.inquiries))
	for _, in := range r.inquiries {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// entryPageOf sorts items by (created_at, id) descending, applies the keyset
// cursor, and trims to a page via the shared codec — the same keyset shape the
// dialect stores implement, so this demo store honors the EntryRepository
// pagination contract rather than truncating to a single page.
func entryPageOf(items []content.Entry, req crud.ListRequest) (crud.Page[content.Entry], error) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		var after []content.Entry
		for _, e := range items {
			if e.CreatedAt.Before(cv) || (e.CreatedAt.Equal(cv) && e.ID < cur.PK) {
				after = append(after, e)
			}
		}
		items = after
	}

	limit := req.NormalizedLimit()
	if len(items) > limit+1 {
		items = items[:limit+1]
	}
	return crud.TrimPage(items, limit, func(e content.Entry) (string, error) {
		return crud.EncodeCursor(orderField, e.CreatedAt, e.ID)
	})
}
