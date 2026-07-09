// Package cms is the public surface of the CMS feature module: the registration
// entry point (Register), the host-filled ports (Repositories), and the
// customization config (Config). Implementation lives in internal/; the domain
// type and repository-interface packages (content, menus, taxonomy, media,
// messaging) are public because hosts reference them, but the service concretes
// and handlers stay internal. The feature is datastore-free: it depends on its
// repository ports, never on a concrete store.
//
// Content follows the Registry model (plan: cms-content-engine): all content is
// a content.Entry on a frozen spine + EAV fields, and content types are
// registered as data (Article and Page ship as seed registrations). Adding a
// type or field is a code change with zero DB migration.
package cms

import (
	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/features/cms/domain/media"
	"github.com/gopernicus/gopernicus/features/cms/domain/menus"
	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/features/cms/domain/taxonomy"
	inbound "github.com/gopernicus/gopernicus/features/cms/internal/inbound/cms"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/entrysvc"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/mediasvc"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/menussvc"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/messagingsvc"
	"github.com/gopernicus/gopernicus/features/cms/internal/logic/taxonomysvc"
	"github.com/gopernicus/gopernicus/sdk/cacher"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// Repositories is the set of outbound ports the feature needs. A store adapter
// (e.g. features/cms/stores/turso) fills it; the feature stays dialect-blind.
// All content rides the single Entries port (the Registry model); Terms/Menus/
// Media/Inquiries stay typed.
type Repositories struct {
	Entries   content.EntryRepository
	Terms     taxonomy.TermRepository
	Menus     menus.MenuRepository
	Media     media.AssetRepository
	Inquiries messaging.InquiryRepository
}

// TemplateBinding binds a dev-authored render func to a registered type's
// template. Hosts supply these via Config.Templates to render their custom
// content types (the per-type seam, complementing the chrome seam Config.Views).
type TemplateBinding = content.TemplateBinding

// Config carries host-provided collaborators and overrides. Zero values fall
// back to sensible defaults where possible: a nil Views leaves the HTML surface
// unregistered (FS3 — only GET /media/{id}/file mounts), a nil Cache disables
// public-page caching. Types/Templates let a host register custom content types
// and their renderers on top of the Article/Page seeds. Blobs and Mailer are
// host infrastructure the feature cannot default.
type Config struct {
	Views     Views                 // HTML rendering port; nil → HTML surface not registered (FS3)
	Types     []content.ContentType // host-registered custom types
	Templates []TemplateBinding     // host (type,template) → render func
	Cache     cacher.Storer         // nil → no public-page caching
	Blobs     media.BlobStore       // blob storage for media (disk/s3); host-owned
	Mailer    email.Sender          // contact-form delivery; host-owned
	MailFrom  string                // From address for contact notifications
	ContactTo string                // recipient for contact notifications

	// IDs is the app's entity-ID strategy, decided once at wiring (amended D9):
	// it mints the keys of entries, assets, menus, menu items, inquiries, and
	// terms. The zero value generates default nanoids; cryptids.Database delegates
	// key generation to the database (the bundled stores omit the id column and
	// read it back with RETURNING); an integration's GenerateFunc (e.g.
	// google-uuid) chooses another shape.
	IDs cryptids.IDGenerator

	// AdminMiddleware wraps every admin route the feature mounts (the CRUD/
	// management surface); public routes (site pages, asset serving, the contact
	// form) are never wrapped. Nil disables gating, preserving current behavior.
	// This is the cross-feature wiring seam of features/README.md §5 (C2): a host
	// passes another feature's middleware here — e.g. auth's RequireUser — so cms
	// gates its admin surface without importing auth. Structural typing means auth
	// need not know cms exists and cms never imports auth.
	AdminMiddleware []web.Middleware
}

// Register wires the CMS feature onto the host's mount: it builds the content
// Registry (seed types + host types + their templates), builds the domain
// services from the supplied repositories, and registers the feature's routes.
// Migrations are registered by the store adapter (see features/cms/stores/turso),
// not here — the core is dialect-blind.
func Register(m feature.Mount, repos Repositories, cfg Config) error {
	registry := content.NewRegistry()
	if err := registerSeedTypes(registry); err != nil {
		return err
	}
	for _, ct := range cfg.Types {
		if err := registry.Register(ct); err != nil {
			return err
		}
	}
	if cfg.Views != nil {
		for _, b := range cfg.Views.SeedTemplates() {
			if err := registry.RegisterTemplate(b.Type, b.Template, b.Fn); err != nil {
				return err
			}
		}
	}
	for _, b := range cfg.Templates {
		if err := registry.RegisterTemplate(b.Type, b.Template, b.Fn); err != nil {
			return err
		}
	}

	entrySvc := entrysvc.NewService(repos.Entries, registry, cfg.IDs, nil, m.Events)
	termSvc := taxonomysvc.NewService(repos.Terms, cfg.IDs, nil)
	menuSvc := menussvc.NewService(repos.Menus, cfg.IDs, nil)
	mediaSvc := mediasvc.NewService(repos.Media, cfg.Blobs, cfg.IDs, nil)
	contactSvc := messagingsvc.NewService(repos.Inquiries, cfg.Mailer, cfg.MailFrom, cfg.ContactTo, cfg.IDs, nil)

	inbound.Mount(m.Router, inbound.Deps{
		Registry: registry,
		Entries:  entrySvc,
		Taxo:     termSvc,
		Menus:    menuSvc,
		Media:    mediaSvc,
		Contact:  contactSvc,
	}, cfg.Views, cfg.Cache, cfg.AdminMiddleware)

	return nil
}
