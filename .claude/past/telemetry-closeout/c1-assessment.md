# C1 assessment — cms non-root-prefix link limitation (forward-plan shape)

Produced by telemetry-closeout task-6 (ASSESS-ONLY, 2026-07-07). This is the
plan a future milestone cuts from — **nothing here is fixed in this
milestone** (Risk 3: the fix stays future-milestone scope regardless of size;
prior conclusion `features/README.md` §4). The trigger that reopens it is
ledgered: *a host needs cms mounted under a non-root prefix, or a
multi-feature mount forces non-root prefixes.*

## The limitation, restated precisely

`feature.PrefixRegistrar` (`sdk/feature/prefix.go`) rewrites only the path a
handler is **registered** under. Everything the feature **renders or emits** —
hrefs, form actions, image `src`, redirect `Location` values — is built as a
host-relative absolute path (`/terms`, `/menus/{id}`, …) with no knowledge of
the mount prefix. Under `PrefixRegistrar{Prefix: "/blog"}`, `GET
/blog/articles` serves 200 but every link on the page points at the
un-prefixed root and 404s (verified 2026-07-02, real-interaction check;
`restructure/00-overview.md` C1 row).

Two distinct layers produce these paths:

1. **Rendering** — `.templ` sources under
   `features/cms/internal/inbound/http/views/`, in three sub-classes:
   static literals in markup, paths string-built inside templ expressions,
   and paths pre-computed in Go handlers and passed in as model fields.
2. **Handlers** — `web.RespondRedirect(w, r, "<absolute path>", 303)` calls
   (which delegate straight to `http.Redirect`, `sdk/web/response.go:129`)
   and the Go-side link-construction sites that feed the view models.

## 1. Inventory (the future milestone's work-list)

Reproducible from repo root with:

```sh
# every link-shaped site in templ sources (hx- returns nothing today)
grep -rn 'href=\|action=\|src=\|hx-' features/cms --include="*.templ"
# sub-class A: static absolute literals
grep -rn 'href="/\|action="/\|src="/' features/cms --include="*.templ"
# sub-class B: absolute paths built inside templ expressions
grep -rn 'templ.SafeURL("/' features/cms --include="*.templ"
# handler redirects (RespondRedirect is the only redirect mechanism;
# no direct http.Redirect or manual Location headers exist)
grep -rn 'RespondRedirect\|http.Redirect\|"Location"' features/cms --include="*.go" \
  | grep -v _templ.go | grep -v _test.go
# handler-side absolute-path construction feeding views
grep -rn '"/' features/cms/internal/inbound/http/*.go | grep -v _test
# menu-item seed data (host-owned)
grep -rn '"/about"' examples
```

### Totals

- **36 path-valued link sites across 10 of 11 `.templ` files**
  (`content_seeds.templ` has none; `contact.templ:46`'s `mailto:` excluded):
  **12 static** literals, **14 built inside templ expressions**, **10 fed by
  Go-computed model values or stored data** (2 of those 10 are menu-item
  *data* URLs, see "Data-level sites" below).
- **14 handler redirects** (all admin) + **11 Go-side link-construction
  sites** feeding view models = **25 Go-side sites** across 5 handler files.
- **Admin/public split (templ sites):** 27 admin-surface, 8 public-surface
  (`public.templ` ×5, `contact.templ` ×2, `menus.templ:118` MenuNav — the
  `GET /menu/{slug}` route is ungated/public), 1 shared chrome
  (`layout.templ:14` renders on every admin page AND the public MenuNav
  page). Go-side: all 14 redirects admin; construction sites 9 admin
  (entries 5, terms 4) + 2 public (`public.go`).

### Per-file: templ sources (`features/cms/internal/inbound/http/views/`)

| file | sites | class | representative examples |
|---|---|---|---|
| `layout.templ` | 1 | static | `:14 <a href="/">Home</a>` — the admin chrome nav; renders on every admin page and the public MenuNav page |
| `public.templ` | 5 | 1 static, 3 model-fed, 1 data-fed | `:41 href={ templ.SafeURL(it.URL) }` (nav — menu-item DATA); `:65/:86 href={ templ.SafeURL(it.Href) }` (model-fed from `publicHref`); `:91 baseHref + "?cursor=" + nextCursor` (model-fed pagination); `:108 <a href="/">Back home</a>` (static, error body) |
| `contact.templ` | 2 | static | `:19 action="/contact"`; `:32 href="/contact"` (public routes) |
| `menus.templ` | 12 | 3 static, 8 expression-built, 1 data-fed | `:12 href="/menus/new"`, `:35 action="/menus"`, `:47 href="/menus"` (static); `:19 SafeURL("/menus/" + m.ID)`, `:21/:47 SafeURL("/menu/" + m.Slug)`, `:61 SafeURL("/menu-items/" + it.ID + "/edit")`, `:62 action=SafeURL("/menu-items/" + it.ID + "/delete")`, `:70 action=SafeURL("/menus/" + m.ID + "/items")`, `:92 action=SafeURL("/menu-items/" + it.ID)`, `:98 SafeURL("/menus/" + it.MenuID)` (expression-built); `:118 SafeURL(it.URL)` (nav item DATA in public MenuNav) |
| `media.templ` | 4 | 1 static, 3 expression-built | `:16 action="/media"`; `:27 src=SafeURL("/media/" + a.ID + "/file")`; `:29 href` same; `:32 action=SafeURL("/media/" + a.ID + "/delete")` |
| `error.templ` | 1 | static | `:9 <a href="/articles">Back to articles</a>` (admin error page; also hardcodes the seed type's plural — pre-existing wrinkle worth fixing in passing) |
| `terms_list.templ` | 5 | 2 static, 3 expression-built | `:8 href="/terms/new?kind=category"`, `:10 ...kind=tag`; `:22 SafeURL("/" + string(t.Kind) + "/" + t.Slug)` (public archive link on an admin page), `:24 .../edit`, `:25 action .../delete` |
| `entries_list.templ` | 3 | model-fed | `:9 SafeURL(newHref)`, `:16 SafeURL(editPrefix + "/" + it.ID + "/edit")`, `:24 SafeURL(newHref + "?cursor=" + nextCursor)` — all from `entries.go:65` `base := "/" + ct.AdminBase()` |
| `term_form.templ` | 2 | 1 static, 1 model-fed | `:9 action=SafeURL(m.Action)` (from `terms.go`); `:28 href="/terms">Cancel` |
| `entry_form.templ` | 1 | model-fed | `:12 action=SafeURL(m.Action)` (from `entries.go`) |
| `content_seeds.templ` | 0 | — | per-entry bodies; markdown-rendered `e.Body` may CONTAIN absolute links (data, see below) |

Check: 12 + 14 + 10 = 36.

### Per-file: handlers (`features/cms/internal/inbound/http/`)

| file | redirects (`web.RespondRedirect`) | link-construction sites feeding views |
|---|---|---|
| `entries.go` | 5 — `:91` `"/"+ct.AdminBase()`, `:127/:137/:147` `.../{id}/edit`, `:156` list | 5 — `:65 base := "/" + ct.AdminBase()` (list newHref/editPrefix), `:71`/`:84` New form `Action`, `:101`/`:120` Edit form `Action` |
| `terms.go` | 3 — `:78/:117/:126` `"/terms"` | 4 — `:57`/`:74` `Action: "/terms"`, `:90`/`:113` `Action: "/terms/" + id` |
| `menus.go` | 4 — `:67/:99/:126/:140` `"/menus/" + id` | 0 (menus links are all built in the template) |
| `media.go` | 2 — `:63/:88` `"/media"` | 0 |
| `public.go` | 0 | 2 — `:129 base := "/" + string(kind) + "/" + term.Slug` (archive pagination baseHref), `:148–152 publicHref()` (`"/" + base + "/" + slug` or `"/" + slug`) |
| `contact.go`, `router.go` | 0 | 0 (router.go's absolute paths are route REGISTRATIONS — PrefixRegistrar already handles those) |

Totals: 14 redirects, 11 construction sites.

### Data-level sites (code fix cannot reach these)

- **Menu-item URLs are stored content data** (`menus.MenuItem.URL`). Both
  seed hosts write host-relative absolute URLs:
  `examples/minimal/cmd/server/main.go:85` and
  `examples/auth-cms/cmd/server/main.go:118` — `{"Home", "/"}, {"About",
  "/about"}`. Rendered raw at `public.templ:41` and `menus.templ:118`.
- **Markdown entry bodies** (`content_seeds.templ` via `RenderMarkdown`) may
  contain author-written absolute links, including `/media/{id}/file` image
  URLs.

A base-path seam cannot rewrite stored data. The future plan must pick a
policy: (i) hosts mounting under a prefix seed/author prefixed URLs (document
it, do nothing), or (ii) the nav renderer base-joins stored URLs that are
path-relative to the mount (recommended for menus — one join point in
`public.go`'s `nav()` — while markdown bodies stay a documented data
limitation).

## 2. Seam recommendation

### Options considered

**(a) Base path discovered from the Mount/PrefixRegistrar surface, threaded
as data.** The feature learns its mount prefix at `Mount` time and
pre-computes full paths, exactly as it already pre-computes `Action`/`Href`
model fields. Discovery shape: a named optional interface in `sdk/feature`
(e.g. `interface{ BasePath() string }`), satisfied by `PrefixRegistrar`
(composing when `Next` also satisfies it — nested prefixes join). cms's
`router.go Mount` type-asserts it on the registrar it was handed; absent →
`""` (root, today's behavior, zero host churn).
- For: single source of truth — the host sets the prefix ONCE on
  `PrefixRegistrar` and both registration and rendering agree by
  construction; matches the codebase's explicit-data doctrine
  (features/README §1 "explicit dependencies as data", no context magic) and
  its existing view-model style (`public.templ`'s doc: "The handler
  pre-computes Href … so the view stays type-blind" — 10 of 36 sites are
  already model-fed); the TC5 precedent favors a NAMED optional interface in
  the defining package when re-derivers are foreseeable, and every future
  feature with views re-derives this; `Mount` itself is untouched (no §6/C3
  evolution question); double-mounting one feature under two prefixes works
  (handlers are per-Mount closures).
- Against: real threading work — handler constructors gain a base/URL-builder
  param, and every static/expression-built template site must become
  base-aware (a `base` param on templ components or more model fields).
  This churn is the actual size of the fix under ANY explicit design; (a)
  just refuses to hide it.

**(b) A view-context value.** Stash the base path in `context.Context`
(middleware installed at cms Mount time); templ components read the implicit
`ctx` via a views-package helper (`href(ctx, "/terms")`); redirect helpers
read it from `r.Context()`.
- For: lowest signature churn — no handler-constructor or templ-signature
  threading; the same 36 + 25 sites get edited but call a helper instead of
  receiving a param.
- Against: an invisible dependency, against the repo's explicit-wiring
  doctrine (the same reasoning that rejected integration-side trace-ID
  stashing in the telemetry plan: "an invisible global side effect");
  failure mode is silent (missing middleware → links quietly un-prefixed —
  the exact bug class C1 already is); the context key needs a home and every
  render path must be guaranteed to pass through the middleware (including
  error renderers); harder to unit-test views in isolation.

**(c) Relative links.** Emit links relative to the current URL
(`../../terms`), no seam at all.
- For: zero new API.
- Against: correctness depends on the DEPTH of the current route
  (`/articles/{id}/edit` vs `/articles` need different `..` counts), so
  every link site becomes per-route arithmetic; redirects and form actions
  share the fragility; interacts with trailing-slash normalization; menus/nav
  stored data can't be made relative. Highest ongoing bug surface. Reject.

### Recommendation: **(a)** — base path via the PrefixRegistrar surface,
discovered through a named optional interface in `sdk/feature`, threaded
explicitly (handler-held URL builder → model fields / template `base`
params).

Reasoning: the host already states the prefix exactly once, on
`PrefixRegistrar`; letting the feature read it back from the registrar it was
handed keeps one source of truth with no new Mount field to keep in sync, and
degrades to today's behavior when absent. Everything downstream then follows
the codebase's own established pattern — handlers pre-compute paths, views
stay dumb — rather than introducing a second, implicit data channel (b) or a
per-route arithmetic scheme (c). The threading cost is the honest cost of the
feature: 61 code-level sites exist regardless of seam choice; (a) is the only
option where a missed site fails visibly in review (a handler building
`"/terms"` without the builder stands out) instead of silently at runtime.

Design details the future plan must carry:

- `theme.PublicViews` (host-implemented chrome seam) can stay UNCHANGED:
  `ListItem.Href` and `Archive`'s `baseHref` are already handler-pre-computed
  (now base-aware for free), nav menu URLs get base-joined in `public.go`'s
  `nav()` (the data policy above), and only the BUNDLED default chrome's own
  static links (`public.templ:108`) need the base — an additive
  `theme.DefaultWithBase(base string)` alongside `Default()` covers it. A
  host overriding chrome owns its own links (document in §4).
- `layout.templ`'s `Layout(title)` gains a base (param or a small
  `LayoutModel`) — this ripples to all 8 admin view files that call
  `@Layout`, and is most of the templ churn.
- `error.templ:9`'s hardcoded `/articles` should become base-aware AND stop
  assuming the article seed type exists (pre-existing wrinkle; link to base
  root instead).

## 3. Size estimate

| area | files | rough hand-written delta |
|---|---|---|
| `sdk/feature` (named optional interface + `PrefixRegistrar.BasePath()` with composition + tests) | `prefix.go`, `feature.go` (doc), `prefix_test.go` | ~25 src + ~40 test |
| cms Go handlers (URL builder file; thread into 5 handler constructors; 14 redirects + 11 construction sites; nav base-join) | `router.go`, new `urls.go`, `entries.go`, `terms.go`, `menus.go`, `media.go`, `public.go` | ~120–150 |
| cms templ sources (26 static/expression sites; `Layout` base threading into 8 caller files; model plumbing) | 11 `.templ` files | ~70–100 |
| theme (additive `DefaultWithBase`) | `theme/theme.go` | ~15 |
| tests (prefixed-mount rendering test: register under `/demo-prefix`, assert every rendered href/action/Location is prefixed; handler redirect tests) | `cms_test.go` or new `internal/inbound/http` tests | ~100–150 |
| docs | `features/README.md` §4 rewrite (limitation → supported, with the data-policy caveat) | ~30 |

Total: **~360–510 hand-written lines across ~24 files**, plus 11 regenerated
`*_templ.go` (machine-generated diff, likely larger than the hand-written
one).

**Test/verify implications:**

- Every `.templ` edit requires `make generate`; `*_templ.go` is NEVER
  hand-edited; `make check`'s templ-drift gate will catch any skip.
- `make guard` must stay green — the seam adds no new import edges (cms
  already imports `sdk/feature`; the interface lives there).
- Hermetic green does NOT close this: §4's limitation was found by a
  real-interaction check, so the milestone close requires the same — boot a
  host with cms under a non-root prefix, crawl the rendered pages, follow
  every link/form/redirect to 200. A link-crawl assertion in tests is the
  cheap standing guard; the manual drive is the close evidence.
- Zero behavior change for root-mounted hosts (base `""` short-circuits to
  today's strings) — `examples/cms`, `examples/minimal`, `examples/auth-cms`
  need no edits, which is itself a verify point (their page snapshots are
  byte-identical before/after).

**Suggested phase split:**

1. **Phase 1 — seam + Go side.** `sdk/feature` interface +
   `PrefixRegistrar.BasePath()`; cms discovers the base in `router.go`,
   builds the URL helper, threads it through the 5 handler constructors;
   fix all 25 Go-side sites. After this phase: form posts, redirects, and
   public list/pagination links are prefix-correct; template-static links
   still break (documented mid-state).
2. **Phase 2 — views.** Thread base through the templ components (`Layout`
   first, then the 26 static/expression sites), `make generate`, snapshot
   tests proving root-mount output unchanged.
3. **Phase 3 — data policy + proof + docs.** Menu-nav base-joining in
   `nav()` + host seeding guidance; markdown-body limitation documented as
   accepted; `features/README.md` §4 rewrite; the prefixed-mount
   real-interaction drive recorded as the close evidence; full `make check`.

Phases 1–2 could collapse into one for a single executor, but the mid-state
boundary is a clean review point and keeps the templ-regeneration diff
isolated from the seam diff.
