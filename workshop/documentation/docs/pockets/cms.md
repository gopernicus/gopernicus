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
}); err != nil {
    return err
}
```

`Repositories` contains typed entry, term, menu, asset, and inquiry ports. Store modules supply them as a bundle; a host may implement them independently.

## Configuration seams

| Field | Meaning |
|---|---|
| `Views` | nil disables HTML; the media byte endpoint remains |
| `Types` | host-defined content types added to Article and Page |
| `Templates` | host render bindings for registered types |
| `Cache` | nil disables public-page caching |
| `Blobs` | host-owned file storage for media bytes |
| `Mailer` | host-owned contact delivery |
| `IDs` | zero uses default nanoids; database/custom strategies supported |
| `AdminMiddleware` | wraps every admin route; nil leaves the surface ungated |

The views seam and registry-template seam are different: `Views` controls page chrome and forms, while template bindings render a particular registered content type.

## GOTH and custom themes

`pockets/cms/views/goth` supplies the bundled view implementation. A host can use it directly or embed it and override selected public pages while keeping admin/forms on the default components. `examples/cms/internal/theme` demonstrates that pattern.

The host constructs the GOTH bundle and serves its assets. CMS owns neither the UI module nor an asset route.

## Admin authorization

CMS declares no authentication or authorization dependency. The host injects middleware:

```go
cms.Config{
    AdminMiddleware: []web.Middleware{
        authSvc.RequireUser,
        requireCMSAdmin,
    },
}
```

Public pages, contact submission, and asset delivery are outside the admin middleware stack.

## Prefix limitation

`pocket.PrefixRegistrar` can relocate registered routes, but current views produce root-relative links and form actions. For now, mount CMS at the host root when using bundled views, or supply prefix-aware custom views.

## Persistence

The pgx and Turso sibling modules own CMS SQL and export canonical migrations. The memory-backed example proves no driver is required. Blob bytes are intentionally a separate capability: metadata can live in SQL while content lives on disk, GCS, S3, or a host adapter.
