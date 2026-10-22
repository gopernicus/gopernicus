// Package cms is the public surface of the CMS pocket module: the registration
// entry point (Register), the host-filled ports (Repositories), and the
// customization config (Config). Implementation lives in internal/; the domain
// type and repository-interface packages (content, menus, taxonomy, media,
// messaging) are public because hosts reference them, but the service concretes
// and handlers stay internal. The pocket is datastore-free: it depends on its
// repository ports, never on a concrete store.
//
// Content follows the Registry model (plan: cms-content-engine): all content is
// a content.Entry on a frozen spine + EAV fields, and content types are
// registered as data (Article and Page ship as seed registrations). Adding a
// type or field is a code change with zero DB migration.
package cms

import (
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/taxonomy"
	inbound "github.com/gopernicus/gopernicus/pockets/cms/internal/inbound/cms"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/entrysvc"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/mediasvc"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/menussvc"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/messagingsvc"
	"github.com/gopernicus/gopernicus/pockets/cms/internal/logic/taxonomysvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Admin and mail posture errors returned by Register. A refused configuration
// registers nothing.
var (
	// ErrAdminUngatedRefused is returned when production RuntimeMode meets
	// AdminUngatedAcknowledged: production never mounts the admin surface ungated.
	ErrAdminUngatedRefused = errors.New("cms: production RuntimeMode refuses Config.AdminUngatedAcknowledged — pass Config.AdminMiddleware with a gate that authorizes, not only authenticates")
	// ErrAdminAcknowledgedWithMiddleware is returned in every mode, zero
	// included, when AdminUngatedAcknowledged is set beside a non-empty
	// AdminMiddleware: acknowledging an ungated surface that is gated is
	// contradictory wiring.
	ErrAdminAcknowledgedWithMiddleware = errors.New("cms: Config.AdminUngatedAcknowledged set but Config.AdminMiddleware gates the admin surface")
	// ErrInsecureMailTransport is returned when production RuntimeMode meets a
	// Mailer that notify.CheckTransport rejects (a development-only or
	// metadata-less sender, e.g. the console sender). It wraps
	// notify.ErrInsecureTransport as well.
	ErrInsecureMailTransport = errors.New("cms: production RuntimeMode rejects Config.Mailer")
)

// Repositories is the set of outbound ports the pocket needs. A store adapter
// (e.g. pockets/cms/stores/turso) fills it; the pocket stays dialect-blind.
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
// host infrastructure the pocket cannot default.
type Config struct {
	Views     Views                 // HTML rendering port; nil → HTML surface not registered (FS3)
	Types     []content.ContentType // host-registered custom types
	Templates []TemplateBinding     // host (type,template) → render func
	Cache     cacher.Storer         // nil → no public-page caching
	// PageCache controls public-page TTL, body limit and optional host scope.
	// Zero config selects 60 seconds and 1 MiB; negative TTL disables caching.
	PageCache cacher.PageConfig
	Blobs     media.BlobStore // blob storage for media (disk/s3); host-owned
	Mailer    email.Sender    // contact-form delivery; host-owned
	// MailFrom is the From address for contact notifications. (env: CMS_MAIL_FROM)
	MailFrom string `env:"CMS_MAIL_FROM"`
	// ContactTo is the recipient for contact notifications. (env: CMS_CONTACT_TO)
	ContactTo string `env:"CMS_CONTACT_TO"`

	// IDs is the app's entity-ID strategy, decided once at wiring (amended D9):
	// it mints the keys of entries, assets, menus, menu items, inquiries, and
	// terms. The zero value generates default nanoids; sdk.DatabaseID delegates
	// key generation to the database (the bundled stores omit the id column and
	// read it back with RETURNING); an integration's GenerateFunc (e.g.
	// google-uuid) chooses another shape.
	IDs sdk.IDGenerator

	// AdminMiddleware wraps every admin route the pocket mounts (the CRUD/
	// management surface); public routes (site pages, asset serving, the contact
	// form) are never wrapped. Empty is deny-by-absence: the admin routes are not
	// mounted (404/405) and Register logs a WARN, unless AdminUngatedAcknowledged
	// is set. A gate must authorize, not only authenticate.
	// This is the cross-pocket wiring seam of pockets/README.md §5 (C2): a host
	// passes another pocket's middleware here — e.g. auth's RequireAccessToken() — so cms
	// gates its admin surface without importing auth. Structural typing means auth
	// need not know cms exists and cms never imports auth.
	AdminMiddleware []web.Middleware

	// AdminUngatedAcknowledged mounts the admin routes with no AdminMiddleware,
	// with a WARN — a development/demo posture. It is a wiring assertion written
	// in the composition root, never an env knob (no env tag). Production
	// RuntimeMode refuses it (ErrAdminUngatedRefused); beside a non-empty
	// AdminMiddleware it is ErrAdminAcknowledgedWithMiddleware in every mode.
	AdminUngatedAcknowledged bool

	// RuntimeMode is the host's deployment posture, resolved by the composition
	// root from the process environment (no env tag). Zero is mode-blind. A
	// non-zero mode is validated; production refuses AdminUngatedAcknowledged
	// and a Mailer that notify.CheckTransport rejects (ErrInsecureMailTransport).
	RuntimeMode environment.Mode
}

// Register wires the CMS pocket onto the host's mount: it builds the content
// Registry (seed types + host types + their templates), builds the domain
// services from the supplied repositories, and registers the pocket's routes.
// Migrations are registered by the store adapter (see pockets/cms/stores/turso),
// not here — the core is dialect-blind.
func Register(m pockets.Mount, repos Repositories, cfg Config) error {
	if err := checkPosture(cfg); err != nil {
		return err
	}
	gated := len(cfg.AdminMiddleware) > 0

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
	}, cfg.Views, cfg.Cache, cfg.PageCache, cfg.AdminMiddleware, gated || cfg.AdminUngatedAcknowledged)

	// With nil Views no admin surface exists, so there is nothing to warn about.
	if m.Logger != nil && cfg.Views != nil && !gated {
		if cfg.AdminUngatedAcknowledged {
			m.Logger.Warn("cms: admin routes mounted UNGATED by Config.AdminUngatedAcknowledged — development only; production refuses it", "posture_waiver", "AdminUngatedAcknowledged")
		} else {
			m.Logger.Warn("cms: admin routes NOT mounted (404/405): Config.AdminMiddleware is empty (deny-by-absence) — pass a gate that authorizes, or set Config.AdminUngatedAcknowledged for a development demo", "admin_mounted", false)
		}
	}

	return nil
}

// checkPosture validates the admin-gate and mail-transport wiring before
// anything is registered. Zero RuntimeMode is mode-blind: only the
// contradictory acknowledgement is refused.
func checkPosture(cfg Config) error {
	if cfg.RuntimeMode != "" {
		if err := environment.ValidateMode(cfg.RuntimeMode); err != nil {
			return fmt.Errorf("cms: Config.RuntimeMode: %w", err)
		}
	}
	if len(cfg.AdminMiddleware) > 0 && cfg.AdminUngatedAcknowledged {
		return ErrAdminAcknowledgedWithMiddleware
	}
	if cfg.RuntimeMode == "" {
		return nil
	}
	if cfg.RuntimeMode.IsProduction() && cfg.AdminUngatedAcknowledged {
		return ErrAdminUngatedRefused
	}
	if _, err := notify.CheckTransport(cfg.RuntimeMode, cfg.Mailer); err != nil {
		return fmt.Errorf("%w: %w", ErrInsecureMailTransport, err)
	}
	return nil
}
