package cms

import (
	"context"
	"io"
	"net/http"

	"github.com/gopernicus/gopernicus/features/cms/internal/logic/entrysvc"
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/logging"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// stringRenderer is a minimal web.Renderer that writes a fixed body, standing in
// for a host's own theme component / per-entry render.
type stringRenderer string

func (s stringRenderer) Render(_ context.Context, w io.Writer) error {
	_, err := io.WriteString(w, string(s))
	return err
}

// newTestRegistry returns a registry with the Article + Page seed types and
// their default renderers, mirroring the feature default so handler tests
// exercise real registry-driven routing.
func newTestRegistry() *content.Registry {
	r := content.NewRegistry()
	_ = r.Register(content.ContentType{Slug: "article", Singular: "Article", Plural: "Articles", Templates: []string{"default"}, Routable: true})
	_ = r.Register(content.ContentType{Slug: "page", Singular: "Page", Plural: "Pages", Templates: []string{"default"}, Hierarchical: true, Routable: true})
	_ = r.RegisterTemplate("article", "default", func(e content.Entry) web.Renderer { return stringRenderer(e.Title) })
	_ = r.RegisterTemplate("page", "default", func(e content.Entry) web.Renderer { return stringRenderer(e.Title) })
	return r
}

// --- router helpers: build a full router with fakes for every service except
// the one under test ---

func menuRouter(svc menuService) http.Handler {
	return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, svc, &fakeMediaSvc{}, &fakeContactSvc{}, nil, logging.NewNoop(), WithViews(stubViews{}))
}

func mediaRouter(svc mediaService) http.Handler {
	return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, svc, &fakeContactSvc{}, nil, logging.NewNoop(), WithViews(stubViews{}))
}

func contactRouter(svc messagingService) http.Handler {
	return BuildRouter(newTestRegistry(), &fakeEntrySvc{}, &fakeTaxo{}, &fakeMenuSvc{}, &fakeMediaSvc{}, svc, nil, logging.NewNoop(), WithViews(stubViews{}))
}

// --- fakeEntrySvc ---

type fakeEntrySvc struct {
	listFn      func(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error)
	byTermFn    func(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error)
	getFn       func(ctx context.Context, id string) (content.Entry, error)
	getSlugFn   func(ctx context.Context, typ, slug string) (content.Entry, error)
	createFn    func(ctx context.Context, typeSlug string, in entrysvc.Input) (content.Entry, error)
	editFn      func(ctx context.Context, id string, in entrysvc.Input) (content.Entry, error)
	publishFn   func(ctx context.Context, id string) (content.Entry, error)
	unpublishFn func(ctx context.Context, id string) (content.Entry, error)
	deleteFn    func(ctx context.Context, id string) error
	setTermsFn  func(ctx context.Context, entryID string, termIDs []string) error
}

func (f *fakeEntrySvc) Create(ctx context.Context, typeSlug string, in entrysvc.Input) (content.Entry, error) {
	if f.createFn != nil {
		return f.createFn(ctx, typeSlug, in)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) Edit(ctx context.Context, id string, in entrysvc.Input) (content.Entry, error) {
	if f.editFn != nil {
		return f.editFn(ctx, id, in)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) Get(ctx context.Context, id string) (content.Entry, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) GetBySlug(ctx context.Context, typ, slug string) (content.Entry, error) {
	if f.getSlugFn != nil {
		return f.getSlugFn(ctx, typ, slug)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) Delete(ctx context.Context, id string) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id)
	}
	return nil
}
func (f *fakeEntrySvc) List(ctx context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	if f.listFn != nil {
		return f.listFn(ctx, q)
	}
	return crud.Page[content.Entry]{}, nil
}
func (f *fakeEntrySvc) ListByTerm(ctx context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	if f.byTermFn != nil {
		return f.byTermFn(ctx, termID, q)
	}
	return crud.Page[content.Entry]{}, nil
}
func (f *fakeEntrySvc) Publish(ctx context.Context, id string) (content.Entry, error) {
	if f.publishFn != nil {
		return f.publishFn(ctx, id)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) Unpublish(ctx context.Context, id string) (content.Entry, error) {
	if f.unpublishFn != nil {
		return f.unpublishFn(ctx, id)
	}
	return content.Entry{}, nil
}
func (f *fakeEntrySvc) SetTerms(ctx context.Context, entryID string, termIDs []string) error {
	if f.setTermsFn != nil {
		return f.setTermsFn(ctx, entryID, termIDs)
	}
	return nil
}

// --- fakeTaxo ---

type fakeTaxo struct {
	listFn    func(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error)
	getSlugFn func(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error)
}

func (f *fakeTaxo) CreateTerm(context.Context, taxonomy.Kind, string, string) (taxonomy.Term, error) {
	return taxonomy.Term{}, nil
}
func (f *fakeTaxo) GetTerm(context.Context, string) (taxonomy.Term, error) {
	return taxonomy.Term{}, nil
}
func (f *fakeTaxo) GetTermBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	if f.getSlugFn != nil {
		return f.getSlugFn(ctx, kind, slug)
	}
	return taxonomy.Term{}, nil
}
func (f *fakeTaxo) ListTerms(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	if f.listFn != nil {
		return f.listFn(ctx, kind)
	}
	return nil, nil
}
func (f *fakeTaxo) EditTerm(context.Context, string, string, string) (taxonomy.Term, error) {
	return taxonomy.Term{}, nil
}
func (f *fakeTaxo) DeleteTerm(context.Context, string) error { return nil }

// --- fakeMenuSvc ---

type fakeMenuSvc struct {
	listFn  func(ctx context.Context) ([]menus.Menu, error)
	getSlug func(ctx context.Context, slug string) (menus.Menu, error)
	itemsFn func(ctx context.Context, menuID string) ([]menus.MenuItem, error)
}

func (f *fakeMenuSvc) CreateMenu(context.Context, string) (menus.Menu, error) {
	return menus.Menu{}, nil
}
func (f *fakeMenuSvc) GetMenu(context.Context, string) (menus.Menu, error) { return menus.Menu{}, nil }
func (f *fakeMenuSvc) GetMenuBySlug(ctx context.Context, slug string) (menus.Menu, error) {
	if f.getSlug != nil {
		return f.getSlug(ctx, slug)
	}
	return menus.Menu{}, nil
}
func (f *fakeMenuSvc) ListMenus(ctx context.Context) ([]menus.Menu, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return nil, nil
}
func (f *fakeMenuSvc) Items(ctx context.Context, menuID string) ([]menus.MenuItem, error) {
	if f.itemsFn != nil {
		return f.itemsFn(ctx, menuID)
	}
	return nil, nil
}
func (f *fakeMenuSvc) AddMenuItem(context.Context, string, string, string, string, int) (menus.MenuItem, error) {
	return menus.MenuItem{}, nil
}
func (f *fakeMenuSvc) GetMenuItem(context.Context, string) (menus.MenuItem, error) {
	return menus.MenuItem{}, nil
}
func (f *fakeMenuSvc) EditMenuItem(context.Context, string, string, string, string, int) (menus.MenuItem, error) {
	return menus.MenuItem{}, nil
}
func (f *fakeMenuSvc) RemoveMenuItem(context.Context, string) error { return nil }

// --- fakeMediaSvc ---

type fakeMediaSvc struct {
	listFn func(ctx context.Context) ([]media.Asset, error)
	openFn func(ctx context.Context, id string) (media.Asset, io.ReadCloser, error)
}

func (f *fakeMediaSvc) Upload(context.Context, string, string, int64, io.Reader) (media.Asset, error) {
	return media.Asset{}, nil
}
func (f *fakeMediaSvc) GetAsset(context.Context, string) (media.Asset, error) {
	return media.Asset{}, nil
}
func (f *fakeMediaSvc) ListAssets(ctx context.Context) ([]media.Asset, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return nil, nil
}
func (f *fakeMediaSvc) OpenAsset(ctx context.Context, id string) (media.Asset, io.ReadCloser, error) {
	if f.openFn != nil {
		return f.openFn(ctx, id)
	}
	return media.Asset{}, nil, nil
}
func (f *fakeMediaSvc) DeleteAsset(context.Context, string) error { return nil }

// --- fakeContactSvc ---

type fakeContactSvc struct {
	submitFn func(ctx context.Context, name, email, message string) (messaging.Inquiry, error)
	listFn   func(ctx context.Context) ([]messaging.Inquiry, error)
}

func (f *fakeContactSvc) Submit(ctx context.Context, name, email, message string) (messaging.Inquiry, error) {
	if f.submitFn != nil {
		return f.submitFn(ctx, name, email, message)
	}
	return messaging.Inquiry{}, nil
}
func (f *fakeContactSvc) ListInquiries(ctx context.Context) ([]messaging.Inquiry, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return nil, nil
}
