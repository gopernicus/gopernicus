package cms

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/content"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/media"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/menus"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/taxonomy"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// publicRouteSet is every route the pocket mounts with its HTML surface and no
// admin surface (Article + Page seeds). A new route fails the exact-set test
// until it is classified public here or registered through handleAdmin.
var publicRouteSet = []string{
	"GET /{$}",
	"GET /articles/{slug}",
	"GET /{slug}",
	"GET /category/{slug}",
	"GET /tag/{slug}",
	"GET /menu/{slug}",
	"GET /media/{id}/file",
	"GET /contact",
	"POST /contact",
}

// representativeRoutes is a slice across the pocket's surfaces: registry-driven
// content routes (Article + Page seed types), public site, admin, and the
// folded-in contact form (decision 1 — messaging is part of CMS).
var representativeRoutes = []string{
	"GET /{$}",                // public home
	"GET /articles",           // admin article list (registry-driven)
	"POST /articles",          // admin article create
	"GET /articles/{id}/edit", // admin article editor
	"GET /articles/{slug}",    // public article (plural-prefixed)
	"GET /pages",              // admin page list
	"GET /{slug}",             // public page (hierarchical, flat at root)
	"GET /menu/{slug}",        // public nav
	"GET /category/{slug}",    // public taxonomy archive
	"GET /contact",            // contact form (folded-in messaging)
	"POST /contact",           // contact submit
	"GET /inquiries",          // inquiry admin
}

// recordingRegistrar captures the routes a pocket mounts and how many
// middleware each carries, standing in for the host's real router. It
// implements pockets.RouteRegistrar.
type recordingRegistrar struct{ routes map[string]int }

func (r *recordingRegistrar) Handle(method, path string, _ http.HandlerFunc, mw ...web.Middleware) {
	r.routes[method+" "+path] = len(mw)
}

func newRecordingRegistrar() *recordingRegistrar {
	return &recordingRegistrar{routes: map[string]int{}}
}

func (r *recordingRegistrar) sorted() []string {
	out := make([]string, 0, len(r.routes))
	for route := range r.routes {
		out = append(out, route)
	}
	slices.Sort(out)
	return out
}

// passthrough stands in for a host's admin gate.
func passthrough(next http.Handler) http.Handler { return next }

// tlsSender declares a production-capable mail transport.
type tlsSender struct{}

func (tlsSender) Send(context.Context, email.Message) error { return nil }
func (tlsSender) Capabilities() notify.Capabilities {
	return notify.Capabilities{TransportSecurity: notify.TransportSecurityTLS}
}

func captureLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// stubViews is a nil-rendering Views used to exercise Register's full HTML route
// set without importing the bundled default (which would cycle: views/templ
// imports cms). Handlers are never invoked here, so the renderers can be nil.
type stubViews struct{}

func (stubViews) Home([]menus.MenuItem, []ListItem) web.Renderer { return nil }
func (stubViews) Archive(string, []menus.MenuItem, []ListItem, Pager) web.Renderer {
	return nil
}
func (stubViews) Single(string, string, []menus.MenuItem, web.Renderer) web.Renderer { return nil }
func (stubViews) Error(int, string) web.Renderer                                     { return nil }
func (stubViews) ContactForm(ContactModel) web.Renderer                              { return nil }
func (stubViews) ContactThanks() web.Renderer                                        { return nil }
func (stubViews) MenuNav(menus.Menu, []menus.MenuItem) web.Renderer                  { return nil }
func (stubViews) EntriesList(string, string, string, []EntryListItem, Pager) web.Renderer {
	return nil
}
func (stubViews) EntriesListContent(string, string, string, []EntryListItem, Pager) web.Renderer {
	return nil
}
func (stubViews) EntryForm(EntryFormModel) web.Renderer                   { return nil }
func (stubViews) TermsList([]taxonomy.Term, []taxonomy.Term) web.Renderer { return nil }
func (stubViews) TermForm(TermFormModel) web.Renderer                     { return nil }
func (stubViews) MenusList([]menus.Menu) web.Renderer                     { return nil }
func (stubViews) MenuNew(string) web.Renderer                             { return nil }
func (stubViews) MenuDetail(menus.Menu, []menus.MenuItem) web.Renderer    { return nil }
func (stubViews) MenuItemForm(menus.MenuItem) web.Renderer                { return nil }
func (stubViews) MediaLibrary([]media.Asset, string) web.Renderer         { return nil }
func (stubViews) InquiriesList([]messaging.Inquiry) web.Renderer          { return nil }
func (stubViews) AdminError(int, string) web.Renderer                     { return nil }
func (stubViews) SeedTemplates() []content.TemplateBinding                { return nil }

// TestRegister_GatedMountsRouteSet verifies the public composition path:
// cms.Register wires services from repositories and mounts the pocket's full
// route set, every non-public route behind the admin gate and no public route
// behind it. Repositories are nil because no handler is invoked here — services
// are constructed (passthrough) but never called, so this exercises the
// Register→Mount wiring without a datastore.
func TestRegister_GatedMountsRouteSet(t *testing.T) {
	rec := newRecordingRegistrar()
	log, buf := captureLog()

	err := Register(pockets.Mount{Router: rec, Logger: log}, Repositories{}, Config{Views: stubViews{}, AdminMiddleware: []web.Middleware{passthrough}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, route := range representativeRoutes {
		if _, ok := rec.routes[route]; !ok {
			t.Errorf("Register did not mount route %q", route)
		}
	}
	for route, mw := range rec.routes {
		want := 1
		if slices.Contains(publicRouteSet, route) {
			want = 0
		}
		if mw != want {
			t.Errorf("route %q carries %d middleware, want %d", route, mw, want)
		}
	}
	if strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("gated Register logged a WARN: %s", buf.String())
	}
}

// TestRegister_AcknowledgedMountsUngated: AdminUngatedAcknowledged outside
// production mounts the admin routes with no middleware and one WARN naming the
// waiver.
func TestRegister_AcknowledgedMountsUngated(t *testing.T) {
	for _, mode := range []environment.Mode{"", environment.ModeDevelopment} {
		rec := newRecordingRegistrar()
		log, buf := captureLog()

		err := Register(pockets.Mount{Router: rec, Logger: log}, Repositories{}, Config{Views: stubViews{}, AdminUngatedAcknowledged: true, RuntimeMode: mode})
		if err != nil {
			t.Fatalf("mode %q: Register: %v", mode, err)
		}
		for _, route := range representativeRoutes {
			if mw, ok := rec.routes[route]; !ok || mw != 0 {
				t.Errorf("mode %q: route %q mounted=%t middleware=%d, want mounted ungated", mode, route, ok, mw)
			}
		}
		if got := strings.Count(buf.String(), "level=WARN"); got != 1 || !strings.Contains(buf.String(), "posture_waiver=AdminUngatedAcknowledged") {
			t.Errorf("mode %q: want one WARN with posture_waiver=AdminUngatedAcknowledged, got %d: %s", mode, got, buf.String())
		}
	}
}

// TestRegister_NeitherMountsExactlyThePublicSet pins deny-by-absence: with no
// gate and no acknowledgement the registered set EQUALS the public list, and a
// nil Mount.Logger is allowed.
func TestRegister_NeitherMountsExactlyThePublicSet(t *testing.T) {
	rec := newRecordingRegistrar()
	if err := Register(pockets.Mount{Router: rec}, Repositories{}, Config{Views: stubViews{}}); err != nil {
		t.Fatalf("Register with nil Logger: %v", err)
	}
	want := slices.Clone(publicRouteSet)
	slices.Sort(want)
	if got := rec.sorted(); !slices.Equal(got, want) {
		t.Fatalf("registered routes = %q, want exactly the public set %q", got, want)
	}

	log, buf := captureLog()
	if err := Register(pockets.Mount{Router: newRecordingRegistrar(), Logger: log}, Repositories{}, Config{Views: stubViews{}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 || !strings.Contains(buf.String(), "admin routes NOT mounted") {
		t.Errorf("want one admin-unmounted WARN, got %d: %s", got, buf.String())
	}
}

// TestRegister_NilViewsWarnsNothing: with nil Views no admin surface exists, so
// deny-by-absence has nothing to announce.
func TestRegister_NilViewsWarnsNothing(t *testing.T) {
	rec := newRecordingRegistrar()
	log, buf := captureLog()
	if err := Register(pockets.Mount{Router: rec, Logger: log}, Repositories{}, Config{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := rec.sorted(); !slices.Equal(got, []string{"GET /media/{id}/file"}) {
		t.Errorf("registered routes = %q, want only the media byte endpoint", got)
	}
	if buf.Len() != 0 {
		t.Errorf("nil Views logged: %s", buf.String())
	}
}

// TestRegister_PostureRefusals covers the three sentinels and mode validation.
// A refused configuration registers nothing.
func TestRegister_PostureRefusals(t *testing.T) {
	gate := []web.Middleware{passthrough}
	console := email.NewConsole(slog.New(slog.DiscardHandler))
	cases := []struct {
		name string
		cfg  Config
		want []error
	}{
		{"production acknowledged", Config{Views: stubViews{}, RuntimeMode: environment.ModeProduction, AdminUngatedAcknowledged: true, Mailer: tlsSender{}}, []error{ErrAdminUngatedRefused}},
		{"zero mode acknowledged beside a gate", Config{Views: stubViews{}, AdminMiddleware: gate, AdminUngatedAcknowledged: true}, []error{ErrAdminAcknowledgedWithMiddleware}},
		{"development acknowledged beside a gate", Config{Views: stubViews{}, RuntimeMode: environment.ModeDevelopment, AdminMiddleware: gate, AdminUngatedAcknowledged: true}, []error{ErrAdminAcknowledgedWithMiddleware}},
		{"production acknowledged beside a gate", Config{Views: stubViews{}, RuntimeMode: environment.ModeProduction, AdminMiddleware: gate, AdminUngatedAcknowledged: true, Mailer: tlsSender{}}, []error{ErrAdminAcknowledgedWithMiddleware}},
		{"production console mailer", Config{Views: stubViews{}, RuntimeMode: environment.ModeProduction, AdminMiddleware: gate, Mailer: console}, []error{ErrInsecureMailTransport, notify.ErrInsecureTransport}},
		{"invalid mode", Config{Views: stubViews{}, RuntimeMode: "prod", AdminMiddleware: gate}, []error{environment.ErrModeInvalid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecordingRegistrar()
			err := Register(pockets.Mount{Router: rec}, Repositories{}, tc.cfg)
			for _, want := range tc.want {
				if !errors.Is(err, want) {
					t.Errorf("Register error = %v, want errors.Is %v", err, want)
				}
			}
			if len(rec.routes) != 0 {
				t.Errorf("refused Register mounted %d routes", len(rec.routes))
			}
		})
	}
}

// TestRegister_PostureAccepted: production with a gate and a production-capable
// Mailer constructs; zero mode is mode-blind (no Mailer check), and development
// accepts the console sender.
func TestRegister_PostureAccepted(t *testing.T) {
	console := email.NewConsole(slog.New(slog.DiscardHandler))
	cases := map[string]Config{
		"production gated":          {Views: stubViews{}, RuntimeMode: environment.ModeProduction, AdminMiddleware: []web.Middleware{passthrough}, Mailer: tlsSender{}},
		"zero mode console":         {Views: stubViews{}, AdminUngatedAcknowledged: true, Mailer: console},
		"development console":       {Views: stubViews{}, RuntimeMode: environment.ModeDevelopment, AdminUngatedAcknowledged: true, Mailer: console},
		"zero mode nil mailer gate": {Views: stubViews{}, AdminMiddleware: []web.Middleware{passthrough}},
	}
	for name, cfg := range cases {
		if err := Register(pockets.Mount{Router: newRecordingRegistrar()}, Repositories{}, cfg); err != nil {
			t.Errorf("%s: Register: %v", name, err)
		}
	}
}
