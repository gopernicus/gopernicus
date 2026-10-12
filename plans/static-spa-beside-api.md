# StaticFileServer for an SPA mounted beside an API (sdk/pkg/web) — issue #68

Status: RATIFIED 2026-10-12 (owner: "ratified"; rev 2 with lead-backend, platform-sre, product reviews folded).
RELEASED 2026-10-12 — sdk/v0.12.0 @ 93c38a35 (PR #69, merge commit), cold-verified from the public proxy
(empty caches, GOWORK=off, consumer build/vet/verify + public module tests; see RELEASING.md). Task 4
(three-sixty) and the Segovia v2 7.2 adoption are downstream/owner.
Build correction 2026-10-12 (Task 2): `If-None-Match: *` gets 304 from `http.ServeContent` even with
no ETag — RFC 9110 says `*` matches any current representation — so the test pins 304 for `*` and 200
for `If-Modified-Since` and a quoted/weak tag; the D1 doc comment says so. D5: the existing
`WithSPAMode` sentence was kept (it carries the ServeContent/range/MIME facts) and the new block
follows it. Panic messages chosen at build: `web.WithIndex: empty index`,
`web.WithReservedPrefixes: empty prefix` / `prefix "api" is not rooted` / `prefix "/" reserves the whole mount`.
Direction agreed in-session 2026-10-12 (owner: "accept, keep it, plan it" to:
`WithIndex(bytes)` for the rewritten index, hard-404 under the asset prefix as the SPA-mode
default, `WithReservedPrefixes` kept).

## Problem

`web.StaticFileServer` with `WithSPAMode()` serves `index.html` for every missing path. A host
that mounts an SPA at `/` beside its own API has to wrap it for three things the server could do
itself. three-sixty (gps-360-go, plans/79) does all three in `ui/spa/`: an `fs.FS` overlay that
swaps `index.html` for a `bytes.Reader`-backed file so `http.ServeContent` keeps Content-Length,
HEAD and ranges (`index.go`); a `Reserved(p)` prefix check so `/api/…` never answers a page; and
an `fs.Stat` under the Vite asset dir so a stale hashed chunk is a hard 404 the browser reloads
from, not HTML parsed as a module (`spa.go`).

Inspected: `sdk v0.11.0`, `sdk/pkg/web/static.go` (same on `main`). No in-repo SPA-mode user
outside the package's own tests; examples and `ui/goth` use `WithAssetPrefix("dist/")` without
SPA mode and are unaffected. Known external consumers: three-sixty (`HandleRaw("/", …)` mount,
sdk pinned at v0.9.0) and Segovia v2 go-live 7.2 (unbuilt; `AddRoutes(handler, "")` over
`os.DirFS`, needs JSON 404s under `/api/`).

## Rulings

- **R1 Bytes, not a transform func.** `WithIndex(index []byte)`. The host reads and rewrites
  `index.html` at boot and passes the result. `NewStaticFileServer` returns no error, so a
  `func([]byte) ([]byte, error)` option would have nowhere to put a failed transform (panic or a
  deferred 500); the host already reads `index.html` to detect an absent bundle, so the func saves
  no line. Injection is host policy; the server's job is to serve the result well.
- **R2 WithIndex replaces content only.** It does not enable fallback. Root and missing-path
  policy stay with `WithSPAMode`. Without SPA mode, `/index.html` serves the bytes and `/` is
  still 404, as today.
- **R3 Hard-404 under the asset prefix is the SPA-mode default, not an option.** A hashed asset
  never legitimately falls back to the page. Behavior change for SPA-mode users; MINOR with a
  release note that names the deep-link consequence (a client route under the asset prefix is no
  longer reachable by refresh). `WithAssetPrefix("")` disables the zone along with the caching,
  documented as a trade-off, not a free toggle.
- **R4 Reserved prefixes win over everything.** A reserved path answers 404 before the filesystem
  is consulted, in every mode, even when a file exists there and even when `Open` would fail: the
  prefix belongs to another mount and the file server must never speak for it.
- **R5 Method policy stays out of the server.** `AddRoutes` registers `GET` patterns, which the
  mux also matches for `HEAD`, and answers other methods 405 itself. A host that mounts the server
  as a method-less root catch-all (`HandleRaw("/", …)`) owns method policy and is told so in the
  docs (D5). No `Allow` header logic in `static.go`.
- **R6 Misconfiguration panics at the option call**, with a `web.WithIndex:` /
  `web.WithReservedPrefixes:` message prefix, matching the existing nil-option rule: empty
  `WithIndex` bytes, a reserved entry that is empty, unrooted, or cleans to `/`.
- **R7 The two new 404s are not cacheable.** The asset-zone and reserved-prefix 404s carry
  `Cache-Control: no-cache, no-store, must-revalidate` (the index's triple). During a rolling
  deploy a request for `index-NEW.js` can land on an old instance; a bare 404 there is cached by
  CDNs on static extensions (Cloudflare ~3 min, CloudFront 10 s default) and outlives the deploy.
- **R8 Last-call-wins for every option.** `WithReservedPrefixes` replaces like `WithAssetPrefix`
  does. No consumer composes lists; one semantic across the option set.

## Design

### D0. Shared header helper (`static.go`)

Lift `static.go:132-138` (Cache-Control by SPA-index / asset-prefix rule, then Content-Type) into
`func (s *StaticFileServer) setStaticHeaders(w http.ResponseWriter, name string)`, called by both
the fs path and the D1 bytes path so the two cannot drift. Add
`func notFoundNoStore(w http.ResponseWriter, r *http.Request)` for R7: set the triple, then
`http.NotFound` (which sets `text/plain`).

### D1. `WithIndex(index []byte) StaticOption`

```go
// WithIndex serves index for index.html — and, in SPA mode, for every fallback —
// instead of the file in the fs.FS, which need not contain one. Hosts use it to
// serve an index.html rewritten once at boot (runtime config injected into a
// data block); those bytes are public. The slice is copied. Responses carry no
// ETag or Last-Modified, so conditional requests always get 200. Empty index
// panics. Detecting an absent bundle stays with the host.
func WithIndex(index []byte) StaticOption
```

- The option stores `bytes.Clone(index)`; the constructor clones again from `staticConfig` into
  `StaticFileServer` (the snapshot `TestStaticOptionsDoNotRetainConstructionConfig` enforces).
- In `serveFile`, before `fileFS.Open`: when `name == "index.html"` and `s.index != nil`,
  `s.setStaticHeaders(w, name)` then
  `http.ServeContent(staticResponseWriter{w}, r, "index.html", time.Time{}, bytes.NewReader(s.index))`.
  A `bytes.Reader` cannot fail, so no `staticReadSeeker` wrapper. Zero ModTime: no
  `Last-Modified`, `If-Modified-Since` ignored; `Content-Length`, `HEAD`, `Range` from
  `ServeContent`.
- `serveMissing`'s fallback already calls `serveFile(w, r, "index.html")`, so root and fallback
  pick the bytes up with no further change.

### D2. `WithReservedPrefixes(prefixes ...string) StaticOption`

```go
// WithReservedPrefixes names rooted paths that belong to other mounts ("/api/",
// "/healthz"). A request at or under one answers a plain-text 404 before the
// filesystem is consulted — never index.html, even in SPA mode, and never a
// file that happens to exist there. A trailing slash is optional: "/api/" and
// "/api" both cover /api and everything under /api/. Matching is byte-exact.
// Paths are compared after AddRoutes strips its basePath, so prefixes are
// relative to the mount. The last call wins. An entry that is empty, unrooted,
// or cleans to "/" panics.
func WithReservedPrefixes(prefixes ...string) StaticOption
```

- Each entry is normalized with `path.Clean` at the option call; panic when the input is empty,
  does not start with `/`, or cleans to `/` (`"//"` and `"/api//"` are therefore handled, not
  silently accepted). The option copies into its own slice; the constructor clones again.
- `ServeHTTP` computes `rooted := path.Clean("/" + r.URL.Path)` once; reserved when
  `rooted == entry || strings.HasPrefix(rooted, entry+"/")`; then `cleanPath` derives from
  `rooted` by `TrimPrefix("/")`. The check runs first, before the root/empty-path branch (R4).
  Answer: `notFoundNoStore` (R7). This is three-sixty's `Reserved` with its two branches unified.

### D3. Hard-404 under the asset prefix (`serveMissing`)

```go
if s.spaMode && name != "index.html" {
	if dir := strings.TrimSuffix(s.assetPrefix, "/"); dir != "" && (name == dir || strings.HasPrefix(name, dir+"/")) {
		notFoundNoStore(w, r) // a missing or directory asset is never the page
		return
	}
	s.serveFile(w, r, "index.html")
	return
}
```

- Directory semantics on purpose: `WithAssetPrefix("static")` must not hard-404 `/statistics` or
  `/static-info`. The *caching* match at `static.go:134` stays a raw `HasPrefix` as today; the doc
  says the two differ and that the prefix should be a directory ending in `/` (Vite
  `build.assetsDir`, default `assets/`), relative, no leading slash.
- `serveMissing` is reached for both missing paths and directories, before any header is set, so
  `/assets`, `/assets/`, `/assets/sub/` and `/assets/index-OLD.js` are all covered and carry no
  stray `immutable`.
- **Flips one existing test**: `TestStaticFileServer_SPA_DirectoryFallback` (`static_test.go:138`)
  requests `GET /assets` and expects the page; under R3 that is 404. Task 2 re-covers directory
  fallback at a path outside the prefix. `TestStaticFileServer_SPA_IndexCachePolicy`
  (`WithAssetPrefix("index")`, probes `/missing`, `/dir`) stays green.

### D4. Doc comments

- `WithSPAMode`: "A missing path under the asset prefix is a 404, not index.html: a browser
  holding a stale hashed chunk must get a hard failure the app reloads from, not HTML parsed as a
  module. The 404 is `no-store`. A client route under the asset prefix is therefore unreachable
  by deep link; name the bundler's asset dir so they do not collide."
- `WithAssetPrefix`: "Relative to the fs root, no leading slash; a directory, ending in `/`. The
  cache rule is a raw prefix match; the SPA-mode 404 zone is the directory."
- `ServeHTTP`: mentions the reserved check order and the index bytes.

### D5. Docs (`workshop/documentation/docs/sdk/web.md`, "Static and SPA files")

Replace the current `WithSPAMode` sentence with a short block:

- **`WithIndex`** — serve an index.html rewritten at boot (runtime config in a data block). The
  bytes are public and go through `ServeContent`. The host still detects an absent bundle itself.
- **Stale chunks.** In SPA mode a missing path under the asset prefix is a `no-store` 404, never
  the page. The SPA owns recovery: listen for Vite's `vite:preloadError` (or the bundler's
  equivalent) and `location.reload()`. Keep `WithAssetPrefix` equal to the bundler's asset dir,
  ending in `/`; a client route under it is unreachable by deep link. `WithAssetPrefix("")`
  turns the zone off and the immutable caching with it.
- **Other mounts beside the SPA — pick one:**
  - Mounted with `AddRoutes` on the same `WebHandler` as the API, and the API owns its 404 shape
    (JSON): register `GET /api/{path...}` *with its method* answering the API's 404. Mux
    precedence ranks it above `GET /{path...}`, and because it carries a method a wrong method on a
    real API route still gets 405. `WithReservedPrefixes` is not needed. (Segovia's case.)
  - Mounted as a method-less root catch-all (`HandleRaw("/", …)`), or the reserved list is wanted
    as one testable value: `WithReservedPrefixes`. The answer is always a plain-text `no-store`
    404. Keep the list in step with the root mounts; a route-census test is the pattern.
    (three-sixty's case.)
- **The catch-all caveat.** A method-less `/` catch-all turns a wrong method on any registered
  route from 405 into whatever the catch-all answers, because ServeMux reports 405 only when no
  pattern matches at all. `AddRoutes` avoids this by registering `GET`; a host that mounts with
  `HandleRaw("/", …)` must answer non-GET/HEAD itself. Do not "fix" it with a method-less
  `/api/` 404 handler: that recreates the same 405→404 loss one prefix down.

The one-line table at the top of `web.md` ("serve embedded or SPA assets") gains the two option
names.

## Tasks

1. **`static.go`** — D0–D4. New fields copied from `staticConfig` in the constructor, never the
   config pointer. Imports gain `bytes` and `time`.
2. **Tests (`static_test.go`, table tests beside the existing SPA block; `static_regression_test.go`
   where noted)**
   - *WithIndex:* SPA mode serves the bytes at `/index.html`, `/`, `/client-route` with exact
     `Content-Length`; `HEAD /` has the length and an empty body; `Range: bytes=0-3` on `/` and
     on `/client-route` → 206 with the slice; `If-Modified-Since` and `If-None-Match: *` → 200,
     never 304; fs with a different `index.html` → ignored; fs with no `index.html` → fallback
     still works; mutating the caller's slice after the option call and after construction leaves
     the response unchanged; `Cache-Control` is the no-store triple in SPA mode; non-SPA mode:
     `/index.html` serves the bytes, `/` is 404; empty bytes panic.
   - *WithReservedPrefixes:* with `"/api/"`, `"/healthz"` and SPA mode: `/api`, `/api/`, `/api/x`,
     `/healthz`, `/healthz/live` → 404, `text/plain`, no-store, body never contains the index or
     the `WithIndex` bytes; `/healthzz`, `/apix` → page; `api/real.txt` exists in the fs → still
     404; an fs whose `Open` calls `t.Fatal` under `api/` proves "before the filesystem"; non-SPA
     mode `/api/real.txt` → 404 (R4 every mode); direct `ServeHTTP` with `//api/x` and
     `/x/../api/y` → 404; mounted under `AddRoutes(h, "/app")`: `/app/api/x` → 404,
     `/app/other` → page; a reserved entry nested in the asset zone (`"/assets/goth/"` with
     default `assets/`): `/assets/goth/x.css` → 404 even when the file exists, `/assets/app.js`
     still `immutable` (three-sixty's exact configuration); last call wins; panics for `""`,
     `"api"`, `"/"`, `"//"`.
   - *Asset hard-404:* SPA mode `/assets/index-OLD.js` → 404, `text/plain`, no-store, body does
     not contain the index; `/assets`, `/assets/`, `/assets/sub/` → 404; `/elsewhere` → page;
     `WithAssetPrefix("static")`: `/static/x.js` missing → 404, `/static-info` → page;
     `WithAssetPrefix("")` → `/assets/x.js` falls back to the page (R3 trade-off); mounted under
     `AddRoutes(h, "/app")`: `/app/assets/old.js` → 404; non-SPA mode unchanged.
   - Flip `TestStaticFileServer_SPA_DirectoryFallback`: `/assets` → 404 is the new contract; add a
     `docs/x.txt` fixture and assert `/docs` → page to keep directory fallback covered.
   - New `TestStaticOptionsPanicOnMisconfig` table with each message pinned (the existing
     `TestWebConstructorsRejectNilOptions` table is for constructors and asserts the nil-option
     message; leave it). Extend `TestStaticOptionsDoNotRetainConstructionConfig` to mutate
     `settings.index` and `settings.reserved`.
3. **Docs** — D5. RELEASING.md entry at release (see Release).
4. **Downstream proof (owner, three-sixty)** — see Downstream.

## Verify

```
cd sdk && go build ./... && go vet ./... && go test ./pkg/web/...
make check    # examples build StaticFileServer and carry proof tests; keep the #64 discipline
```

Real-interaction check before reporting done: a throwaway main in the scratchpad embeds a
four-file fs (`index.html` with a `<script id="cfg">` marker, `assets/app.js`, `api/real.txt`,
`docs/x.txt`), rewrites the marker, builds the server with `WithSPAMode(), WithIndex(b),
WithReservedPrefixes("/api/")`, and mounts it **both ways** in turn:

| mount | request | expected |
|---|---|---|
| both | `GET /`, `GET /client/route` | 200, injected config present, `Content-Length` set |
| both | `HEAD /` | 200, empty body, same `Content-Length` |
| both | `GET -r 0-3 /` | 206 |
| both | `GET /assets/app.js` | 200, `immutable` |
| both | `GET /assets/index-OLD.js` | 404, `text/plain`, `no-store`, body has no `<script` |
| both | `GET /api/real.txt`, `GET /api`, `GET /api/` | 404, `no-store` |
| both | `GET /apix`, `GET /docs` | 200 page |
| `AddRoutes(h, "")` | `POST /anything` | 405 from the mux |
| `HandleRaw("/", …)` | `POST /anything` | 200 page — the D5 caveat, shown on purpose |

## Release

One PR, tests included: `sdk/v0.12.0` — MINOR (two new exported options; SPA-mode behavior
change under the asset prefix). The RELEASING.md entry must say in plain words: a missing path
under the asset prefix is now a `no-store` 404 instead of the page; a client route under that
prefix (default `assets/`, applied even when `WithAssetPrefix` was never called) loses deep-link
refresh, remedy = rename the bundler's asset dir and pass a matching `WithAssetPrefix`;
`WithAssetPrefix("")` opts out but also drops immutable caching; reserved-prefix 404s are
`no-store` too. Tag from the main merge commit; merge / tag / push as separate steps; poll the
proxy `.info` before `go mod tidy` against the tag; cold-verify with `GOWORK=off`. Not bumped:
pockets, integrations, `ui/goth`, examples, workshop scaffold pins.

## Downstream (owner)

- **three-sixty (gps-360-go):** repin sdk v0.9.0 → v0.12.0, crossing v0.10.0 (`OriginPolicy`) and
  v0.11.0 (`email.Unsubscribe`), both additive per RELEASING.md. Then: pass the rewritten bytes to
  `WithIndex`; pass `spa.ReservedPrefixes...` to `WithReservedPrefixes`; delete `indexOverlay`,
  `memFile`, `memInfo` and their methods from `ui/spa/index.go` — **keep `injectConfig`,
  `runtimeConfig`, `ErrConfigElementMissing`** (R1); delete the asset `fs.Stat` branch of
  `spa.Handler.ServeHTTP`; **keep the method gate and the `h.static == nil` absent-bundle 404**
  (R5). Census: `workshop/clicontract/sparoutes_test.go:52` calls `spa.Reserved(path)` as a
  function, so it is *not* unaffected. Recommended: rewrite the census to probe the handler with
  each mounted root path and assert the answer is not the page, which tests the real server and
  lets `spa.Reserved` go; alternative: keep `spa.Reserved` as a census-only helper that
  duplicates D2's rule by convention. Keep `TestWhatIsNotAPageIsAPlain404` unchanged as the
  parity proof (`/assets/goth/x`, `/assets/auth/x`, `/assets`, POST, DELETE). Behavior parity
  holds for GET/HEAD; the gate covers the rest. The SPA source has no `vite:preloadError`
  handler, so a stale chunk is a clean 404 but not yet a self-healing one — add the reload
  listener (tracked by three-sixty, not here).
- **Segovia v2 (go-live 7.2, unbuilt):** `AddRoutes(handler, "")` over `os.DirFS`; use the D5
  stdlib recipe (`GET /api/{path...}` JSON 404) for 7.2(d) rather than `WithReservedPrefixes`,
  which answers plain text. R3's hard-404 applies to its Vite output under `assets/`.
- **Before any SPA-mode repin:** grep the host for `WithSPAMode`, and grep its client router for
  routes under the asset prefix. Segovia and coordination-hub were not grepped in this session.
- Issue #68 close note: Segovia's "navigation-only fallback" flag is answered by reserved
  prefixes plus the stdlib recipe, not by a new option.

## Out of scope

Method gating or an `Allow` header in the server (R5); an ETag for the index bytes; a transform
option (R1); an exported fs overlay; per-prefix custom responses for reserved paths (JSON 404s
are the stdlib recipe); `AddRoutes` registering a method-less pattern; an SPA example app in this
repo; the three-sixty `preloadError` listener.
