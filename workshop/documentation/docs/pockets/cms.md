---
title: CMS
description: Registry-based content, taxonomy, menus, media, inquiries, and optional GOTH views.
---

# CMS

`pockets/cms` is a datastore-free content-management hexagon. It combines content entries, taxonomy, nested menus, media metadata/blob storage, and contact inquiries. A host may add an admin or public HTML surface through its view adapter, or use the pocket through an API.

## Registry content model

All content types share one `content.Entry` spine. A `Registry` stores type and field definitions as registered data, and custom values ride typed EAV fields.

Article and Page are built-in registrations, not dedicated Go structs or tables. Hosts can add a Product or Case Study with a content-type registration and renderer—no database migration for each type.

```go
product := content.ContentType{
    Slug:      "product",
    Singular:  "Product",
    Plural:    "Products",
    Routable:  true,
    Templates: []string{"default"},
}
```

Use dedicated aggregates when the business behavior needs a richer domain contract. The registry is a content model, not a replacement for every structured domain.

## Composition

CMS still uses the pre-`Service` registration shape:

```go
if err := cms.Register(mount, repos, cms.Config{
    Views:     views,
    Types:     []content.ContentType{product},
    Templates: []cms.TemplateBinding{productTemplate},
    Cache:     cacher.NewMemory(),
    Blobs:     blobStore,
    Mailer:    sender,
    MailFrom:  "cms@example.com",
    ContactTo: "ops@example.com",
    // An authorizing gate (see Admin authorization); without one the admin routes are not mounted.
    AdminMiddleware: adminGate,
    // AdminUngatedAcknowledged: true, // development only — instead of AdminMiddleware
}); err != nil {
    return err
}
```

`Repositories` contains typed entry, term, menu, asset, and inquiry ports. Store modules supply them as a bundle; a host may implement them independently.

## Inquiry retention and erasure

Contact inquiries hold third-party PII (name, email, message). Since v0.5.0 (stores pgx v0.5.0,
turso v0.4.0) `Repositories.InquiryMaintenance` is an OPTIONAL `messaging.InquiryEraser` over the
same rows as `Inquiries` — the bundled stores fill both from one store. `Register` logs
`inquiry_maintenance=wired|absent`, and production WARNs when it is nil. The pocket never calls
it; the host keeps the eraser it handed to `Register` and calls it:

- `PurgeBefore(ctx, before, limit)` from its retention schedule — at most `limit` inquiries with
  `created_at <= before`, oldest first, returning the count; `limit <= 0` is refused;
- `DeleteByEmail(ctx, email)` from its erasure runbook — every inquiry whose email equals `email`
  under ASCII case folding (letters outside ASCII compare as the store defines);
- `Delete(ctx, id)` for one inquiry (an absent id is `sdk.ErrNotFound`).

`0022_inquiries_created_at.sql` adds the `created_at` index the purge walks; it is admitted, not
required, and the pgx store WARNs without it. No admin route deletes an inquiry yet.

## Configuration seams

| Field | Meaning |
|---|---|
| `Views` | nil disables HTML; the media byte endpoint remains |
| `Types` | host-defined content types added to Article and Page |
| `Templates` | host render bindings for registered types |
| `Cache` | host-owned `cacher.Storer`; nil disables public-page caching |
| `PageCache` | `cacher.PageConfig`: host TTL, body limit and optional trusted scope |
| `Blobs` | host-owned file storage for media bytes |
| `MaxUploadBytes` | caps one media upload's request body; zero → 32 MiB; negative → `sdk.ErrInvalidInput` at `Register`; a larger upload answers 413 |
| `AllowedMediaTypes` | media upload allow-list matched against the sniffed type; nil or empty → `DefaultAllowedMediaTypes()`; an entry `mime.ParseMediaType` rejects → `sdk.ErrInvalidInput` at `Register` |
| `Mailer` | host-owned contact delivery |
| `IDs` | zero selects the record default, `sdk.UUIDv7()`, for every cms record (entries, terms, menus, menu items, assets, inquiries); a configured generator wins; `sdk.DatabaseID` defers to the store |
| `AdminMiddleware` | wraps every admin route; empty → the admin routes are not mounted (deny-by-absence, WARN) |
| `AdminUngatedAcknowledged` | development only: mounts the admin routes ungated with a WARN; refused in production, an error beside `AdminMiddleware` |
| `RuntimeMode` | the host's `environment.Mode`, no env tag; zero is mode-blind; production refuses the acknowledgement and a development-only `Mailer` |

The views seam and registry-template seam are different: `Views` controls page chrome and forms, while template bindings render a particular registered content type.

## Public-page cache policy

The host chooses the cache adapter and page policy independently:

```go
cms.Config{
    Cache: cacher.NewMemory(cacher.WithMaxEntries(1000)),
    PageCache: cacher.PageConfig{
        TTL:          30 * time.Second,
        MaxBodyBytes: 512 << 10,
    },
}
```

Zero PageCache uses 60 seconds and a 1 MiB body limit. Negative page TTL disables
caching; this differs from raw storage's zero TTL meaning no expiration. Public
GET HTML is eligible; credentials, cookies, principals, conditional requests,
`no-store`, `Vary`, encoded responses and nonce CSP bypass it. Keep personalized
content outside these routes. A trusted tenant can be added through
`PageCache.Scope`; Pages does not decide which proxy headers to trust.

Since sdk v0.17.0 `cacher.Pages` stores only a response whose `Cache-Control`
carries `public` or `s-maxage`, unless `PageConfig.AllowImplicitPublic` is set.
CMS sets `AllowImplicitPublic` on its own public mount (those routes are public
by construction, whatever `PageCache` says) and its handlers emit no
`Cache-Control`, so pages keep being cached in-process (`X-Cache: MISS`, then
`HIT`) while no CDN starts storing pages the host's invalidation cannot purge.

Page keys are versioned beneath `page:`. A host using a prefix-capable adapter
can call `DeletePrefix(ctx, "page:")` after content changes. CMS does not
synchronously invalidate every write: an in-flight render can repopulate an
invalidated entry, so TTL remains part of the freshness policy. See the
[cache contract](../sdk/capabilities.md#cacher)
for supported headers and middleware ordering.

## Media uploads and serving

`POST /media` (admin) and `GET /media/{id}/file` (public) apply these rules since v0.6.0:

- **Size cap.** The request body is read through `http.MaxBytesReader` at
  `Config.MaxUploadBytes` (zero → 32 MiB). Past it the library re-renders with
  "upload too large" and status **413**. `ParseMultipartForm`'s in-memory limit
  stays a fixed 32 MiB whatever the cap — larger files spill to temporary files,
  so a 1 GiB cap never buffers 1 GiB.
- **Type by sniffing.** The handler reads the file's first 512 bytes, takes
  `http.DetectContentType`'s answer, parses it with `mime.ParseMediaType`, and
  hands that type to the media service; the client's part `Content-Type` is never
  consulted. The service compares it with `Config.AllowedMediaTypes` (both sides
  parsed, so case and parameters never decide). Outside the list the library
  re-renders with 400 and "unsupported file type; allowed: image/png, image/jpeg,
  image/gif, image/webp, application/pdf" (the configured list), and nothing is
  stored.
- **Default list.** `DefaultAllowedMediaTypes()` returns a fresh copy of
  `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `application/pdf`. SVG is
  absent by design (a script-capable document). A host widens or narrows the
  list with `AllowedMediaTypes`; an entry only ever matches a type
  `http.DetectContentType` can return (CSV, for example, sniffs as `text/plain`).
- **Serving.** Every asset response carries `X-Content-Type-Options: nosniff`
  and a `Content-Disposition` built with `mime.FormatMediaType` from the stored
  filename (last path element, control characters dropped; quoted or RFC
  2231-encoded when needed): `inline` for png/jpeg/gif/webp, `attachment` for every other
  type — PDFs download rather than open on the site's origin, and an asset stored
  before v0.6.0 under any other type is still served, as a download.
  `Cache-Control: public, max-age=3600` is unchanged.

```go
cms.Config{
    MaxUploadBytes:    100 << 20, // 100 MiB
    AllowedMediaTypes: append(cms.DefaultAllowedMediaTypes(), "video/mp4"),
}
```

## GOTH and custom themes

`pockets/cms/views/goth` supplies the bundled view implementation. A host can use it directly or embed it and override selected public pages while keeping admin/forms on the default components. `examples/cms/pockets/cms/inbound/theme` demonstrates that pattern.

The host constructs the GOTH bundle and serves its assets. CMS owns neither the UI module nor an asset route.

## Admin authorization

CMS declares no authentication or authorization dependency. The admin surface is
deny-by-absence: with no `AdminMiddleware` its routes are not mounted (admin paths
answer 404 or 405) and `Register` logs a WARN. The host injects middleware. A gate
must authorize, not only authenticate — compose `Require(Can(...))`:

```go
cms.Config{
    AdminMiddleware: []web.Middleware{
        authSvc.HTTP.RequireAccessToken(),
        authorizationComponents.HTTP.Require(authorizationhttp.Can("admin", authorizationhttp.Fixed("platform", "global"))),
    },
}
```

A demo host with no identity pocket acknowledges the ungated posture instead:

```go
cms.Config{
    AdminUngatedAcknowledged: true, // development only — refused in production
}
```

Public pages, contact submission, and asset delivery are outside the admin middleware stack.

## Prefix limitation

`pockets.PrefixRegistrar` can relocate registered routes, but current views produce root-relative links and form actions. For now, mount CMS at the host root when using bundled views, or supply prefix-aware custom views.

## Persistence

The pgx and Turso sibling modules own CMS SQL and export canonical migrations. The memory-backed example proves no driver is required. Blob bytes are intentionally a separate capability: metadata can live in SQL while content lives on disk, GCS, S3, or a host adapter.
