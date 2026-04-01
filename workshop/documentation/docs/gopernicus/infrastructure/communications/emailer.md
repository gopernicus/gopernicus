---
sidebar_position: 2
title: Emailer
---

# Emailer

The communications package provides email infrastructure: rendering HTML/text templates, applying branded layouts, and dispatching through a pluggable sending client.

## Structure

```
communications/emailer/
├── emailer.go          # Emailer service + Client interface
├── renderer.go         # Renderer interface, layout types, send request
├── templates.go        # TemplateRegistry, layered resolution, rendering
├── templates/layouts/  # Built-in layouts (transactional, marketing, minimal)
├── sendgridemailer/    # SendGrid client
└── stdoutemailer/      # Stdout client (development)
```

## Two Interfaces

**`Client`** — the sending contract, satisfied by backend implementations:

```go
type Client interface {
    Send(ctx context.Context, email Email) error
}
```

**`Renderer`** — the full render-and-send contract, satisfied by `Emailer`:

```go
type Renderer interface {
    RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error
    Render(template string, data any, opts ...RenderOption) (html, text string, err error)
}
```

Most code depends on `Renderer`, not `Emailer` directly.

## Emailer

`Emailer` wraps a `Client` and adds template rendering, a default sender address, and branding. It satisfies both `Renderer` and `notify.Notifier`. Structured logging of send failures is opt-in via `WithLogger`:

```go
e, err := emailer.New(
    sendgridemailer.New(cfg),
    "noreply@myapp.com",
    emailer.WithLogger(log),
    emailer.WithBranding(&emailer.Branding{
        Name:    "My App",
        LogoURL: "https://myapp.com/logo.png",
        Address: "123 Main St, Springfield",
    }),
    emailer.WithContentTemplates("authentication", authTemplates, emailer.LayerCore),
    emailer.WithLayouts(appLayouts, "templates/layouts", emailer.LayerApp),
)
```

When `WithLogger` is configured, the emailer logs send failures with recipient and subject context. Without it, errors are returned silently to the caller.

## Templates

Templates are namespaced and layered. The namespace scopes templates to a domain (`authentication`, `billing`, etc.). The layer controls override priority.

### Namespacing

Templates are referenced as `"namespace:templatename"`:

```go
err := e.RenderAndSend(ctx, emailer.SendRequest{
    To:       user.Email,
    Subject:  "Verify your email",
    Template: "authentication:verification",
    Data:     map[string]any{"VerifyURL": url},
})
```

### Layering

Three layers resolve in priority order — higher wins:

| Layer | Constant | Purpose |
|---|---|---|
| Infrastructure | `LayerInfra` | Built-in fallback templates |
| Core | `LayerCore` | Framework domain templates (e.g., auth emails) |
| App | `LayerApp` | App-level overrides and branded layouts |

App templates override Core, Core overrides Infrastructure. Register at the appropriate layer:

```go
// Framework domain templates (gopernicus registers these internally)
emailer.WithContentTemplates("authentication", authTemplates, emailer.LayerCore)

// App overrides — your branded versions replace the defaults
emailer.WithContentTemplates("authentication", myTemplates, emailer.LayerApp)
```

### Layouts

Layouts provide the HTML shell (header, footer, branding) that wraps content templates. Three built-in layouts ship with infrastructure:

| Layout | Constant | Use for |
|---|---|---|
| `transactional` | `LayoutTransactional` | Verification, password reset, account alerts — **default** |
| `marketing` | `LayoutMarketing` | Newsletters, announcements |
| `minimal` | `LayoutMinimal` | System notifications, plain-text preference |

Override a layout at the app layer by registering templates of the same name at `LayerApp`.

Specify a layout per send:

```go
err := e.RenderAndSend(ctx, req, emailer.WithLayout(emailer.LayoutMarketing))
```

### Plain Text

For each content template, provide a `.txt` counterpart alongside the `.html` file. If no text template exists, the renderer strips HTML tags as a fallback.

## Implementations

### sendgridemailer

Sends via the SendGrid API. Configured with an API key and default sender:

```go
client := sendgridemailer.New(sendgridemailer.Config{
    APIKey:    cfg.SendGridAPIKey,
    FromEmail: "noreply@myapp.com",
    FromName:  "My App",
})
```

### stdoutemailer

Logs emails to stdout via `slog`. Use in development and CI — no external dependency, emails are visible in logs without being sent:

```go
client := stdoutemailer.New(log)
```

## Custom Clients

To scaffold a new `Client` implementation:

```
gopernicus new adapter emailer mysmtpclient
```

Generates a new sub-package with a `Send` stub and a compliance test.
