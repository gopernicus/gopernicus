// Package goth is the ui/goth presentation bundle: it exposes the immutable
// Bundle, its Config/profiles, the parsed asset Manifest, the deterministic
// browser Requirements, and the document composition components. It registers no
// route, installs no middleware, accepts no feature.Mount, and writes no HTTP
// response header — the host composes assets, routes, and CSP. See README.md for
// the frozen public contract.
package goth

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/a-h/templ"

	"github.com/gopernicus/gopernicus/ui/goth/assets"
	"github.com/gopernicus/gopernicus/ui/goth/theme"
)

// DefaultAssetBasePath is the public URL prefix used when Config.AssetBasePath is
// empty.
const DefaultAssetBasePath = "/assets/goth"

// Profile selects which self-hosted asset classes a Bundle serves and requires.
type Profile uint8

const (
	// StylesOnly is the zero value: compiled CSS only. Chosen deliberately so a
	// zero Config yields the safest, smallest, no-JavaScript bundle rather than an
	// accidental full runtime.
	StylesOnly Profile = iota
	// Interactive adds the Alpine CSP build + GOTH controllers.
	Interactive
	// Full adds HTMX 2.0.10 + the Gopernicus HTMX response configuration.
	Full
)

func (p Profile) valid() bool { return p <= Full }

// Config is the value passed to New. Its zero value is valid and yields a
// StylesOnly bundle mounted at the default asset base path with the kit's embedded
// default theme injected.
type Config struct {
	// AssetBasePath is the public URL prefix the host will serve the embedded
	// asset FS under. Empty means DefaultAssetBasePath ("/assets/goth"). It is
	// normalized to a leading slash and no trailing slash; a value containing a
	// scheme, host, "..", control characters, or a query/fragment is a
	// construction error.
	AssetBasePath string

	// Profile selects the asset set. Zero value StylesOnly.
	Profile Profile

	// ThemeStylesheetPath is the root-relative public path of the HOST's theme
	// stylesheet. Head() emits it as a <link rel="stylesheet"> AFTER the kit
	// stylesheet (source-order cascade: the host wins). Empty selects the kit's
	// embedded compiled default theme (theme-default.css) — the WordPress model's
	// fallback. A non-empty value must begin with exactly one "/" and is emitted
	// verbatim with no integrity attribute (the kit cannot know host bytes). A
	// scheme, authority/host form ("//"), any additional leading slash, backslash,
	// "..", control character, query, or fragment is a construction error.
	ThemeStylesheetPath string
}

// Bundle is the constructed, immutable presentation bundle. It holds no request
// state and is safe to share across goroutines after New returns.
type Bundle struct {
	profile             Profile
	assetBasePath       string
	themeStylesheetPath string
	manifest            Manifest
	requirements        Requirements
}

// New validates cfg and returns a Bundle. It returns a non-nil error for an
// invalid AssetBasePath, an invalid ThemeStylesheetPath, an unknown Profile, or a
// malformed embedded manifest. It never returns a partially built Bundle alongside
// an error.
func New(cfg Config) (*Bundle, error) {
	if !cfg.Profile.valid() {
		return nil, fmt.Errorf("goth: unknown profile %d", cfg.Profile)
	}
	basePath, err := normalizeAssetBasePath(cfg.AssetBasePath)
	if err != nil {
		return nil, err
	}
	themePath, err := normalizeThemeStylesheetPath(cfg.ThemeStylesheetPath)
	if err != nil {
		return nil, err
	}
	manifest, err := parseManifest(assets.ManifestBytes)
	if err != nil {
		return nil, err
	}
	return &Bundle{
		profile:             cfg.Profile,
		assetBasePath:       basePath,
		themeStylesheetPath: themePath,
		manifest:            manifest,
		requirements:        requirementsForProfile(cfg.Profile),
	}, nil
}

// Profile returns the bundle's selected profile.
func (b *Bundle) Profile() Profile { return b.profile }

// AssetBasePath returns the normalized public asset base path.
func (b *Bundle) AssetBasePath() string { return b.assetBasePath }

// Manifest returns the parsed asset manifest.
func (b *Bundle) Manifest() Manifest { return b.manifest }

// Requirements returns the deterministic, minimal browser resource requirements
// for the bundle's profile.
func (b *Bundle) Requirements() Requirements { return b.requirements }

func normalizeAssetBasePath(s string) (string, error) {
	if s == "" {
		return DefaultAssetBasePath, nil
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("goth: AssetBasePath contains a control character")
		}
	}
	if strings.ContainsAny(s, "?#") {
		return "", fmt.Errorf("goth: AssetBasePath must not contain a query or fragment")
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("goth: AssetBasePath must not contain %q", "..")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("goth: invalid AssetBasePath: %w", err)
	}
	if u.Scheme != "" || u.Host != "" {
		return "", fmt.Errorf("goth: AssetBasePath must be a path, not an absolute URL")
	}
	return "/" + strings.Trim(s, "/"), nil
}

// normalizeThemeStylesheetPath validates the host theme-stylesheet path with
// BROWSER URL semantics, not merely net/url's Scheme/Host fields (amendment-1 D4).
// Empty selects the kit default (returns ""). A non-empty value must begin with
// exactly one "/" and is returned verbatim; anything a browser could resolve as a
// remote authority — a leading "//"/"///"/additional slash, a "\\", a scheme, an
// authority/host, "..", a control character, a query, or a fragment — is rejected.
// Root-relative-only is deliberately conservative: widening to absolute URLs later
// is compatible; narrowing is not.
func normalizeThemeStylesheetPath(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("goth: ThemeStylesheetPath contains a control character")
		}
	}
	if strings.Contains(s, "\\") {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must not contain a backslash")
	}
	if !strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must be root-relative (begin with a single %q)", "/")
	}
	// Reject "//", "///", and any additional leading slash: a browser resolves a
	// leading "//" as a protocol-relative authority, so it can never reach the host
	// origin as a path.
	if strings.HasPrefix(s, "//") {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must not begin with %q (browser authority form)", "//")
	}
	if strings.ContainsAny(s, "?#") {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must not contain a query or fragment")
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must not contain %q", "..")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("goth: invalid ThemeStylesheetPath: %w", err)
	}
	if u.Scheme != "" || u.Host != "" {
		return "", fmt.Errorf("goth: ThemeStylesheetPath must be a root-relative path, not an absolute URL")
	}
	// Exactly one leading slash is guaranteed (single "/" prefix, "//" rejected);
	// emit verbatim.
	return s, nil
}

// requirementsForProfile builds the deterministic, minimal CSP resource needs for
// a profile. The self-hosted default bundle uses fonts/images that need no bundled
// origin, so StylesOnly requires only style-src 'self' and Interactive/Full add
// script-src 'self'. The kit emits no server-rendered style attribute and no
// inline <style> element under any configuration, so style-src is exactly 'self'
// with no nonce (amendment-1 D6).
func requirementsForProfile(p Profile) Requirements {
	sources := map[Directive][]string{
		DirectiveStyle: {"'self'"},
	}
	if p >= Interactive {
		sources[DirectiveScript] = []string{"'self'"}
	}
	order := make([]Directive, 0, len(sources))
	for d := range sources {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	return Requirements{sources: sources, order: order}
}

// profileAssetOrder returns the logical asset names a profile serves, in head
// emission order (stylesheet before scripts). Profiles are additive supersets:
// StylesOnly ⊆ Interactive ⊆ Full, so a bundle serves exactly its profile's asset
// classes — never more.
func profileAssetOrder(p Profile) []string {
	names := []string{"theme.css"}
	if p >= Interactive {
		names = append(names, "runtime.js")
	}
	if p >= Full {
		names = append(names, "htmx.js")
	}
	return names
}

// headResource is one computed <head> asset link/script pointing at a
// fingerprinted manifest URL under AssetBasePath.
type headResource struct {
	href      string
	integrity string
	script    bool
}

// themeResource is the host theme-stylesheet link emitted AFTER the kit stylesheet
// (source-order cascade: the host wins). integrity is empty for a configured host
// path (the kit cannot know host bytes) and set for the kit's own default asset.
type themeResource struct {
	href      string
	integrity string
}

// headModel is the render input for the head component: the profile-selected kit
// stylesheet/script resources plus the theme-stylesheet link (the configured host
// path, or the kit's embedded default theme). No inline style is ever part of it.
type headModel struct {
	styles  []headResource
	theme   themeResource
	scripts []headResource
}

// headResources selects, for the bundle's profile, the external stylesheet and
// script resources from the manifest. An asset absent from the manifest is
// skipped rather than emitted as a broken link.
func (b *Bundle) headResources() (styles, scripts []headResource) {
	for _, name := range profileAssetOrder(b.profile) {
		a, ok := b.manifest.Lookup(name)
		if !ok {
			continue
		}
		r := headResource{
			href:      b.assetBasePath + "/" + a.Path,
			integrity: a.Integrity,
			script:    strings.HasSuffix(name, ".js"),
		}
		if r.script {
			scripts = append(scripts, r)
		} else {
			styles = append(styles, r)
		}
	}
	return styles, scripts
}

// themeResource computes the theme-stylesheet link. A configured host path is
// emitted verbatim with NO integrity (the kit cannot know host bytes). An empty
// path selects the kit's embedded compiled default theme (theme-default.css) under
// AssetBasePath, carrying integrity + crossorigin like every kit asset. If the
// default asset is somehow absent from the manifest, href is empty and the link is
// skipped rather than emitted broken.
func (b *Bundle) themeResource() themeResource {
	if b.themeStylesheetPath != "" {
		return themeResource{href: b.themeStylesheetPath}
	}
	a, ok := b.manifest.Lookup("theme-default.css")
	if !ok {
		return themeResource{}
	}
	return themeResource{href: b.assetBasePath + "/" + a.Path, integrity: a.Integrity}
}

// Head returns the templ component that emits, for the selected profile: the
// fingerprinted kit stylesheet link, then the theme-stylesheet link (the configured
// host path emitted verbatim with no integrity, or the kit's embedded default theme
// with integrity + crossorigin), then — for Interactive/Full — the deferred,
// SRI-guarded script tag(s). The kit emits no server-rendered style attribute and
// no inline <style> element. It writes no HTTP header.
func (b *Bundle) Head() templ.Component {
	styles, scripts := b.headResources()
	return headTags(headModel{
		styles:  styles,
		theme:   b.themeResource(),
		scripts: scripts,
	})
}

// DocumentOptions is the value type for Document. Zero value: light appearance,
// LTR, empty <title>, lang "en", no extra head content.
type DocumentOptions struct {
	Title      string
	Appearance theme.Appearance
	Dir        theme.Direction
	Lang       string
	HeadExtra  templ.Component
}

// Document composes <html>/<head>/<body> with the bundle's Head, an appearance,
// and a direction, wrapping caller body content. It is a convenience for whole
// pages; a host may instead call Bundle.Head() inside its own document. It writes
// no HTTP header and no route.
func (b *Bundle) Document(opts DocumentOptions, body templ.Component) templ.Component {
	return documentShell(opts, b.Head(), body)
}

func documentHTMLAttrs(opts DocumentOptions) templ.Attributes {
	attrs := theme.HTMLAttributes(opts.Appearance, opts.Dir)
	lang := opts.Lang
	if lang == "" {
		lang = "en"
	}
	attrs["lang"] = lang
	return attrs
}

// Asset is one fingerprinted output.
type Asset struct {
	// LogicalName is the stable key ("theme.css", "theme-default.css",
	// "runtime.js", "htmx.js").
	LogicalName string
	// Path is the fingerprinted path relative to the asset FS root, which keeps
	// the dist/ segment ("dist/theme.4f3a1c.css"). Join it to the bundle
	// AssetBasePath for a URL.
	Path string
	// Integrity is the "sha384-..." Subresource Integrity value for the bytes.
	Integrity string
	// Bytes is the uncompressed size, for preload hints and diagnostics.
	Bytes int64
}

// Manifest maps logical names to fingerprinted assets. Parsed once from the
// embedded manifest.json at New; immutable thereafter.
type Manifest struct {
	assets map[string]Asset
	order  []string
}

// Lookup returns the asset for a logical name. ok is false for an unknown name;
// it never panics and never returns a zero Asset as if it were real.
func (m Manifest) Lookup(logical string) (Asset, bool) {
	a, ok := m.assets[logical]
	return a, ok
}

// Assets returns all assets in a deterministic (logical-name-sorted) order.
func (m Manifest) Assets() []Asset {
	out := make([]Asset, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.assets[name])
	}
	return out
}

type manifestFile struct {
	Assets []Asset `json:"assets"`
}

func parseManifest(raw []byte) (Manifest, error) {
	var file manifestFile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Manifest{}, fmt.Errorf("goth: malformed asset manifest: %w", err)
	}
	assets := make(map[string]Asset, len(file.Assets))
	order := make([]string, 0, len(file.Assets))
	for _, a := range file.Assets {
		if a.LogicalName == "" {
			return Manifest{}, fmt.Errorf("goth: asset manifest entry missing logicalName")
		}
		if _, dup := assets[a.LogicalName]; dup {
			return Manifest{}, fmt.Errorf("goth: duplicate asset logicalName %q", a.LogicalName)
		}
		assets[a.LogicalName] = a
		order = append(order, a.LogicalName)
	}
	sort.Strings(order)
	return Manifest{assets: assets, order: order}, nil
}

// Directive is a CSP resource directive key the kit can require.
type Directive string

const (
	DirectiveScript  Directive = "script-src"
	DirectiveStyle   Directive = "style-src"
	DirectiveImg     Directive = "img-src"
	DirectiveFont    Directive = "font-src"
	DirectiveConnect Directive = "connect-src"
	DirectiveMedia   Directive = "media-src"
	DirectiveWorker  Directive = "worker-src"
)

// Requirements is the deterministic, minimal set of browser resource needs for a
// bundle's selected profile. A host/feature maps it into its own CSP policy; the
// kit never writes a header. Ordering is stable and duplicate-free.
type Requirements struct {
	sources map[Directive][]string
	order   []Directive
}

// Sources returns the required sources for a directive in deterministic order. ok
// is false for a directive the bundle does not require.
func (r Requirements) Sources(d Directive) ([]string, bool) {
	s, ok := r.sources[d]
	if !ok {
		return nil, false
	}
	out := make([]string, len(s))
	copy(out, s)
	return out, true
}

// Directives returns every required directive in a stable order.
func (r Requirements) Directives() []Directive {
	out := make([]Directive, len(r.order))
	copy(out, r.order)
	return out
}
