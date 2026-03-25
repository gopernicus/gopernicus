# infrastructure/email -- Email Reference

Package `emailer` provides email rendering and sending infrastructure with pluggable email clients, layered template resolution, and branding support.

**Import:** `github.com/gopernicus/gopernicus/infrastructure/communications/emailer`

## Emailer

The central struct that combines a sending client, template registry, and default from address.

```go
e, err := emailer.New(log, sendgridClient, "noreply@example.com",
    emailer.WithContentTemplates("authentication", authTemplates, emailer.LayerCore),
    emailer.WithLayouts(appLayouts, "templates/layouts", emailer.LayerApp),
    emailer.WithBranding(&emailer.Branding{
        Name:    "MyApp",
        LogoURL: "https://example.com/logo.png",
    }),
)
```

`Emailer` implements both `Renderer` (templated emails) and `notify.Notifier` (simple notifications).

## Client Interface

The sending backend. Implementations live in subpackages.

```go
type Client interface {
    Send(ctx context.Context, email Email) error
}
```

### Available Clients

- `sendgridemailer` -- SendGrid API
- `stdoutemailer` -- Prints to stdout (development)
- `emailertest` -- Test double

## Email Type

```go
type Email struct {
    To      string // recipient
    From    string // sender (uses default if empty)
    Subject string
    HTML    string // HTML body
    Text    string // plain text fallback
}
```

## Renderer Interface

```go
type Renderer interface {
    RenderAndSend(ctx context.Context, req SendRequest, opts ...RenderOption) error
    Render(template string, data any, opts ...RenderOption) (html, text string, err error)
}
```

### SendRequest

```go
type SendRequest struct {
    To       string // recipient email
    Subject  string
    Template string // "namespace:templatename" format
    Data     any    // template data
}
```

### Usage

```go
err := e.RenderAndSend(ctx, emailer.SendRequest{
    To:       "user@example.com",
    Subject:  "Verify your email",
    Template: "authentication:verification",
    Data:     map[string]any{"Code": "123456", "Name": "Alice"},
})
```

## Layout Types

Layouts provide the HTML structure (header/footer) that wraps content templates.

| Layout | Use Case |
|---|---|
| `LayoutTransactional` | Verification emails, password resets, account notifications (default) |
| `LayoutMarketing` | Newsletters, announcements, promotional content |
| `LayoutMinimal` | System notifications, developer alerts |

```go
err := e.RenderAndSend(ctx, req, emailer.WithLayout(emailer.LayoutMarketing))
```

## Template Layers

Templates are resolved using a layered priority system. Higher layers override lower ones.

| Layer | Priority | Description |
|---|---|---|
| `LayerInfra` | Lowest | Generic fallback templates from infrastructure |
| `LayerCore` | Middle | Domain defaults (e.g., authentication templates) |
| `LayerApp` | Highest | App-level overrides and branded layouts |

Register templates at construction time:

```go
emailer.WithContentTemplates("authentication", authFS, emailer.LayerCore)
emailer.WithLayouts(appLayoutFS, "templates/layouts", emailer.LayerApp)
```

Templates are loaded from `embed.FS`. Content templates use `.html` and `.txt` extensions. Text templates are optional -- if missing, HTML is automatically stripped to plain text.

## Branding

Templates access branding via `{{.Brand.Name}}`, `{{.Brand.LogoURL}}`, etc.

```go
type Branding struct {
    Name           string
    Tagline        string
    LogoURL        string
    Address        string       // CAN-SPAM compliance
    SocialLinks    []SocialLink
    UnsubscribeURL string
    PreferencesURL string
}
```

## Direct Sending

Skip templates and send raw email:

```go
err := e.Send(ctx, emailer.Email{
    To:      "user@example.com",
    Subject: "Alert",
    Text:    "Something happened.",
})
```

Validates that To, Subject, and at least one of HTML/Text are present. Uses `defaultFrom` when `From` is empty.

## Notification Interface

`Emailer` also implements `notify.Notifier`:

```go
err := e.Notify(ctx, notify.Notification{
    Recipient: "admin@example.com",
    Subject:   "System Alert",
    Body:      "CPU usage at 95%",
})
```

## Related

- [infrastructure/events](../infrastructure/events.md) -- event handlers often trigger email sending
