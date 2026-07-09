package storetest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
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

// TestReference runs the conformance suite against the in-package reference
// implementation. This is what lets features/cms self-verify under guard G2
// (the core cannot import a driver or a host store, so without an in-package
// implementation the suite would compile but never execute). newRepos returns a
// fresh, empty Store per call — the memory harness's clean-isolation contract.
func TestReference(t *testing.T) {
	Run(t, func(t *testing.T) cms.Repositories {
		return newReference().repositories()
	})
}

// reference is a stdlib-only, test-scoped in-memory Repositories. It exists to
// give the feature module something to run the suite against; it deliberately
// hand-enforces the uniqueness, referential, and cascade semantics that SQL
// gives a dialect store for free, because those are exactly the invariants the
// suite is proving and the class of drift a naive memory store silently loses.
type reference struct {
	mu         sync.RWMutex
	entries    map[string]content.Entry
	entryTerms map[string][]string
	terms      map[string]taxonomy.Term
	menus      map[string]menus.Menu
	items      map[string]menus.MenuItem
	assets     map[string]media.Asset
	inquiries  map[string]messaging.Inquiry
}

func newReference() *reference {
	return &reference{
		entries:    map[string]content.Entry{},
		entryTerms: map[string][]string{},
		terms:      map[string]taxonomy.Term{},
		menus:      map[string]menus.Menu{},
		items:      map[string]menus.MenuItem{},
		assets:     map[string]media.Asset{},
		inquiries:  map[string]messaging.Inquiry{},
	}
}

func (r *reference) repositories() cms.Repositories {
	return cms.Repositories{
		Entries:   refEntries{r},
		Terms:     refTerms{r},
		Menus:     refMenus{r},
		Media:     refAssets{r},
		Inquiries: refInquiries{r},
	}
}

// --- content.EntryRepository ---

type refEntries struct{ *reference }

func (r refEntries) Create(_ context.Context, e content.Entry) (content.Entry, error) {
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

func (r refEntries) Update(_ context.Context, id string, e content.Entry) (content.Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	r.entries[id] = e
	return e, nil
}

func (r refEntries) Get(_ context.Context, id string) (content.Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	e.TermIDs = append([]string(nil), r.entryTerms[id]...)
	return e, nil
}

func (r refEntries) GetBySlug(_ context.Context, typ, slug string) (content.Entry, error) {
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

func (r refEntries) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.entries, id)
	delete(r.entryTerms, id) // cascade the associations, as SQL's ON DELETE CASCADE would
	return nil
}

func (r refEntries) List(_ context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var all []content.Entry
	for _, e := range r.entries {
		if e.Type == q.Type && (q.Status == "" || e.Status == q.Status) {
			all = append(all, e)
		}
	}
	return refPage(all, q.ListRequest)
}

func (r refEntries) ListByTerm(_ context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var matched []content.Entry
	for id, e := range r.entries {
		if e.Type != q.Type || (q.Status != "" && e.Status != q.Status) {
			continue
		}
		for _, tid := range r.entryTerms[id] {
			if tid == termID {
				matched = append(matched, e)
				break
			}
		}
	}
	return refPage(matched, q.ListRequest)
}

func (r refEntries) SetTerms(_ context.Context, entryID string, termIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entryTerms[entryID] = append([]string(nil), termIDs...)
	return nil
}

// refPage sorts by (created_at, id) in the resolved direction, then applies the
// full sdk/crud list matrix — cursor or offset mode, the reverse-probe prev
// page, and the optional count — the same keyset shape the dialect stores
// implement, hand-rolled here so the reference impl paginates identically. It
// encodes the cursor from the record's stored created_at (this store keeps
// nanoseconds, so no truncation happens), which is the property the precision
// case guards. created_at is the only sortable field (content.OrderFields).
func refPage(items []content.Entry, req crud.ListRequest) (crud.Page[content.Entry], error) {
	if err := req.Validate(); err != nil {
		return crud.Page[content.Entry]{}, err
	}
	if req.Order.Field != "" {
		if _, ok := content.OrderFields[req.Order.Field]; !ok {
			return crud.Page[content.Entry]{}, fmt.Errorf("unknown order field %q: %w", req.Order.Field, errs.ErrInvalidInput)
		}
	}
	asc := req.Order.Direction == crud.ASC

	sort.Slice(items, func(i, j int) bool {
		ti, tj := items[i].CreatedAt, items[j].CreatedAt
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return items[i].ID < items[j].ID
		}
		return items[i].ID > items[j].ID
	})

	total := int64(len(items))
	limit := req.NormalizedLimit(crud.Limits{})
	encode := func(e content.Entry) (string, error) {
		return crud.EncodeCursor(orderField, e.CreatedAt, e.ID)
	}

	if req.ResolvedStrategy() == crud.StrategyOffset {
		window := items
		if req.Offset < len(window) {
			window = window[req.Offset:]
		} else {
			window = window[:0]
		}
		if len(window) > limit+1 {
			window = window[:limit+1]
		}
		pg, err := crud.TrimPage(window, limit, encode)
		if err != nil {
			return crud.Page[content.Entry]{}, err
		}
		pg.NextCursor = ""
		pg.HasPrev = req.Offset > 0
		if req.WithCount {
			pg.Total = &total
		}
		return pg, nil
	}

	cur, err := crud.DecodeCursor(req.Cursor, orderField)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}

	forward := items
	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		forward = forward[:0:0]
		for _, e := range items {
			if refAfterCursor(e, cv, cur.PK, asc) {
				forward = append(forward, e)
			}
		}
	}
	window := forward
	if len(window) > limit+1 {
		window = window[:limit+1]
	}
	pg, err := crud.TrimPage(window, limit, encode)
	if err != nil {
		return crud.Page[content.Entry]{}, err
	}

	if cur != nil {
		cv, _ := cur.OrderValue.(time.Time)
		var before []content.Entry
		for _, e := range items {
			if refBeforeCursor(e, cv, cur.PK, asc) {
				before = append(before, e)
			}
		}
		if len(before) > limit {
			before = before[len(before)-limit:]
		}
		if err := crud.MarkPrevPage(&pg, before, limit, encode); err != nil {
			return crud.Page[content.Entry]{}, err
		}
	}

	if req.WithCount {
		pg.Total = &total
	}
	return pg, nil
}

// refAfterCursor reports whether e sorts strictly after the cursor under the
// resolved direction — the next-page predicate.
func refAfterCursor(e content.Entry, cv time.Time, cpk string, asc bool) bool {
	if !e.CreatedAt.Equal(cv) {
		if asc {
			return e.CreatedAt.After(cv)
		}
		return e.CreatedAt.Before(cv)
	}
	if asc {
		return e.ID > cpk
	}
	return e.ID < cpk
}

// refBeforeCursor reports whether e sorts strictly before the cursor under the
// resolved direction — the reverse-probe predicate.
func refBeforeCursor(e content.Entry, cv time.Time, cpk string, asc bool) bool {
	if !e.CreatedAt.Equal(cv) {
		if asc {
			return e.CreatedAt.Before(cv)
		}
		return e.CreatedAt.After(cv)
	}
	if asc {
		return e.ID < cpk
	}
	return e.ID > cpk
}

// --- taxonomy.TermRepository ---

type refTerms struct{ *reference }

func (r refTerms) Get(_ context.Context, id string) (taxonomy.Term, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.terms[id]
	if !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	return t, nil
}

func (r refTerms) GetBySlug(_ context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.terms {
		if t.Kind == kind && t.Slug == slug {
			return t, nil
		}
	}
	return taxonomy.Term{}, errs.ErrNotFound
}

func (r refTerms) ListByKind(_ context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
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

func (r refTerms) Create(_ context.Context, t taxonomy.Term) (taxonomy.Term, error) {
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

func (r refTerms) Update(_ context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.terms[id]; !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	r.terms[id] = t
	return t, nil
}

func (r refTerms) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.terms, id)
	// Drop the term from every entry's associations, mirroring the store's
	// "removes the term and any associations" contract.
	for entryID, ids := range r.entryTerms {
		kept := ids[:0:0]
		for _, tid := range ids {
			if tid != id {
				kept = append(kept, tid)
			}
		}
		r.entryTerms[entryID] = kept
	}
	return nil
}

// --- menus.MenuRepository ---

type refMenus struct{ *reference }

func (r refMenus) CreateMenu(_ context.Context, m menus.Menu) (menus.Menu, error) {
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

func (r refMenus) GetMenu(_ context.Context, id string) (menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.menus[id]
	if !ok {
		return menus.Menu{}, errs.ErrNotFound
	}
	return m, nil
}

func (r refMenus) GetMenuBySlug(_ context.Context, slug string) (menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.menus {
		if m.Slug == slug {
			return m, nil
		}
	}
	return menus.Menu{}, errs.ErrNotFound
}

func (r refMenus) ListMenus(_ context.Context) ([]menus.Menu, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]menus.Menu, 0, len(r.menus))
	for _, m := range r.menus {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r refMenus) ItemsForMenu(_ context.Context, menuID string) ([]menus.MenuItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []menus.MenuItem
	for _, it := range r.items {
		if it.MenuID == menuID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentID == out[j].ParentID {
			return out[i].Position < out[j].Position
		}
		return out[i].ParentID < out[j].ParentID
	})
	return out, nil
}

func (r refMenus) AddItem(_ context.Context, item menus.MenuItem) (menus.MenuItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = item
	return item, nil
}

func (r refMenus) GetItem(_ context.Context, id string) (menus.MenuItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.items[id]
	if !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	return it, nil
}

func (r refMenus) UpdateItem(_ context.Context, id string, item menus.MenuItem) (menus.MenuItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return menus.MenuItem{}, errs.ErrNotFound
	}
	r.items[id] = item
	return item, nil
}

func (r refMenus) DeleteItem(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
	return nil
}

// --- media.AssetRepository ---

type refAssets struct{ *reference }

func (r refAssets) Create(_ context.Context, a media.Asset) (media.Asset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assets[a.ID] = a
	return a, nil
}

func (r refAssets) Get(_ context.Context, id string) (media.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.assets[id]
	if !ok {
		return media.Asset{}, errs.ErrNotFound
	}
	return a, nil
}

func (r refAssets) List(_ context.Context) ([]media.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]media.Asset, 0, len(r.assets))
	for _, a := range r.assets {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r refAssets) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.assets, id)
	return nil
}

// --- messaging.InquiryRepository ---

type refInquiries struct{ *reference }

func (r refInquiries) Create(_ context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inquiries[in.ID] = in
	return in, nil
}

func (r refInquiries) List(_ context.Context) ([]messaging.Inquiry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]messaging.Inquiry, 0, len(r.inquiries))
	for _, in := range r.inquiries {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
