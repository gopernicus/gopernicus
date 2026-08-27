# examples/cms

A hand-written server-side-rendered CMS host, built on the `pockets/cms`
pocket module over the `sdk` framework kernel, persisted in Turso/libSQL,
rendered through the [`ui/goth`](../../ui/goth/README.md) kit (templ + plain CSS)
via the `pockets/cms/views/goth` adapter.

The host builds a `ui/goth` bundle (`uigoth.New(uigoth.Config{AssetBasePath:
"/assets/goth"})`), serves its embedded fingerprinted assets under that path with
`web.NewStaticFileServer(uigothassets.FS, web.WithAssetPrefix("dist/"))`, and passes
the bundle to `cmsViews`. The admin CRUD pages render through the GOTH default; the
public site is this host's custom theme (`internal/theme`, the pocket's
view-override seam) composing the same bundle. See `ui/goth/README.md` §11 for the
full adopter guide.

**Domains (from `pockets/cms`):** `content` (posts, hierarchical pages, and
host-registered custom types via the Registry model), `taxonomy` (categories +
tags), `menus` (nested nav), `media` (uploads), `messaging` (contact
inquiries). **Surfaces:** an admin CRUD area and a themed public site
(markdown-rendered, menu-driven nav, SEO meta, render-cached).

See [ARCHITECTURE.md](../../ARCHITECTURE.md) for the layering rule and
[NOTES.md](../../NOTES.md) for the decision log.

## Layout

This app is a thin host: the CMS hexagon itself lives in the `pockets/cms`
module, not here.

```
cmd/server                 composition root — wires everything; the only place that names concrete adapters
internal/theme             this host's custom public-site theme (the pocket's view-override seam)
workshop/migrations        host-owned migration runner — applies pockets/cms/stores/turso's scaffolded SQL pre-boot
pockets/cms (module)      the hexagon — content, taxonomy, menus, media, messaging (services + ports); datastore-free
pockets/cms/stores/turso  the Turso/libSQL store adapter module for pockets/cms (SQL + migrations)
integrations/              reusable third-party connectors — datastores/turso today
sdk/                       stdlib-only kernel — config, logging, errs, web, repository, id, slug, filestorage, email, cacher, pocket
```

Dependencies point inward: `cmd` wires concrete adapters, the pocket module
never imports this app or `integrations/`, and everything ultimately stands on
`sdk`. See the root [README.md](../../README.md) for the full 6-module map.

## Health check

| route | probes |
|---|---|
| `GET /healthz` | pings Turso via the connector's `StatusCheck`: reachable ⇒ `200 {"status":"ok"}`, unreachable ⇒ `503 {"status":"unavailable"}`. Host-local, unauthenticated (mounted outside any gated group — a readiness probe can't log in). |

## Requirements

- Go 1.26+
- A Turso / libSQL database (remote). `templ` is pinned via the `tool`
  directive in `pockets/cms/views/goth/go.mod` (where the `.templ` sources live
  since the GOTH migration); `go tool templ` needs no global install.

## Environment

Copy `.env.example` (repo root) to `.env` and fill in:

| var | meaning |
|---|---|
| `TURSO_DATABASE_URL` | `libsql://<db>.turso.io` |
| `TURSO_AUTH_TOKEN` | Turso auth token |
| `HOST`, `PORT` | listen address (default `localhost:8080`) |
| `SHUTDOWN_TIMEOUT` | graceful-drain window (default `10s`) |
| `LOG_LEVEL`, `LOG_FORMAT`, `LOG_OUTPUT` | `slog` setup |
| `MEDIA_DIR` | disk path for uploaded assets (default `media-data`) |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | email delivery; unset ⇒ console sender (logs mail) |
| `MAIL_FROM`, `CONTACT_EMAIL` | From address + operator inbox for contact inquiries |

## Make targets

Run from the repo root (the `Makefile` covers all 6 modules):

| target | does |
|---|---|
| `make generate` | `templ generate` in the `views/goth` + `ui/goth` modules → `*_templ.go` |
| `make build` | generate + `go build ./...` per module |
| `make run` | generate + migrate + `go run ./cmd/server` (this app) |
| `make migrate` | applies `workshop/migrations` pre-boot (host-owned, separate from `run`'s server boot) |
| `make test` | `go test ./...` per module |
| `make check` | generate (fail on drift) + vet + build + test per module + the twenty layering guards |

Migrations are scaffolded into `workshop/migrations/primary` from
`pockets/cms/stores/turso` (D4: scaffold-and-own) and applied by this host's
own runner — never by the framework at server boot.

## Integration tests

The live-Turso integration test lives in the pocket's store adapter module
and is build-tagged, skipping without credentials:

```
cd ../../pockets/cms/stores/turso && go test -tags=integration ./...   # needs TURSO_* env / .env
```

## Layering guards

Run from the repo root (`make guard` runs all twenty; each must print nothing):

```
grep -rn --include='*.go' -E '"gopernicus/' .                                         # no legacy import path
grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(integrations|examples|pockets/cms/stores)' pockets/cms --exclude-dir=stores   # pocket core stays adapter-free
grep -rn --include='*.go' '"github.com/' sdk/ | grep -v '"github.com/gopernicus/gopernicus/sdk'          # sdk allows only internal sdk imports from github.com
grep -rnE '"(cloud\.google\.com|golang\.org/x|gopkg\.in)/' --include='*.go' sdk/                        # sdk is stdlib-only
grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(pockets|integrations|examples)' sdk/                        # sdk never imports outward
```

`sdk` is the adapter between the standard library and the application: it
imports **only** the standard library (and other `sdk` packages). Concrete
third-party drivers live in **`integrations/`** (reusable connectors, one
external lib each). The `pockets/cms` hexagon is datastore-free — its store
SQL lives in the separate `pockets/cms/stores/turso` module, which this host
depends on. The `templ` render seam in `sdk/foundation/web` takes a local `Renderer`
interface that `templ.Component` satisfies implicitly, so `sdk` never imports
`templ`.
