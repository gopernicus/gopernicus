# integrations/email/sendgrid

An email connector wrapping exactly one third-party library —
`github.com/sendgrid/sendgrid-go` (with its transport dependency
`github.com/sendgrid/rest`). Its `Sender` implements the `sdk/capabilities/email.Sender`
port over Twilio SendGrid's v3 Mail Send API.

It is an integration rather than an `sdk` default because it speaks one
vendor's live API contract, which churns on SendGrid's schedule, not `sdk`'s.
The stdlib defaults (`email.Console`, `email.SMTP`) stay vendor-neutral; a
SaaS mailer lives in its own module and is swapped in at the composition root.

## Surface

| member | shape |
|---|---|
| `New(cfg Config) *Sender` | builds a POST client for the Mail Send endpoint; no network I/O |
| `Config{APIKey, FromName, Host}` | API key, optional display name, optional host override |
| `Sender.Send(ctx, email.Message) error` | validates then delivers via SendGrid |

`Config.Host` overrides the scheme+host requests are sent to (empty defaults to
`https://api.sendgrid.com`; set `https://api.eu.sendgrid.com` for the EU
region). The sdk `email.Message` carries only a bare `From` address, so
`FromName` supplies the optional display name paired with it.

## Error contract

`Send` calls `email.Message.Validate()` first (invalid input wraps
`sdk/errs.ErrInvalidInput`). A non-2xx SendGrid response maps to a stable
`sdk/errs` kind where one fits — 400 → `ErrInvalidInput`, 401 →
`ErrUnauthorized`, 403 → `ErrForbidden`, 404 → `ErrNotFound`; other statuses
return a plain error carrying the status code and response body. Transport
failures wrap the underlying error.

## Testing

Tests are hermetic and run with a plain `go test ./...`. They point the client
at an `httptest.Server` via `Config.Host` — SendGrid's overridable request
host — capture the constructed request in the handler, and assert the auth
header, recipients, subject, from address, and content types, plus the non-2xx
error mappings. There is deliberately no live SendGrid leg: a real call would
send real email.
