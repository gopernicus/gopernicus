# integrations/email/sendgrid

SendGrid's Mail Send adapter implements
`sdk/capabilities/notify/email.Sender`. It uses SendGrid's typed mail builder
and a fresh standard-library HTTP request for each send; concurrent sends do
not share mutable request state.

## Wiring and selected delivery

```go
sender, err := sendgrid.New(sendgrid.Config{
    APIKey:   apiKey,
    FromName: "Example",
})
if err != nil {
    return err
}
err = notify.Send(ctx, email.NewDelivery(sender, email.Message{
    From:    "no-reply@example.test",
    To:      []string{"person@example.test"},
    Subject: "Reset your password",
    Text:    "Open " + resetURL,
    HTML:    renderedHTML,
}))
```

Import `email` from `sdk/capabilities/notify/email` and `notify` from
`sdk/capabilities/notify`. Passing one delivery sends only that email. Other
host-selected deliveries can be supplied in the same call. Direct
`sender.Send(ctx, message)` is also supported.

## Configuration

`New(Config) (*Sender, error)` validates configuration without I/O. The host supplies APIKey, optional
FromName, optional Host, and optional HTTPClient. Host must be an HTTP(S) origin,
without credentials, query, fragment or endpoint path; empty selects
`https://api.sendgrid.com`. A missing/blank API key, line breaks in the key or
FromName, or an invalid origin returns `sdk.ErrInvalidInput` from New. Provider
credential validity is checked only when sending.

HTTPClient is copied at construction. Its transport remains shared and host-owned;
configure it before use. Without a client, the adapter uses the standard transport
and a 30-second timeout. Every request carries the caller's context. Redirects are
disabled on the adapter's copy, including when the supplied client allows them,
so credentials and message bodies stay at the configured endpoint. The host's
original client is unchanged. HTTP endpoints are supported for local tests.

The email Message supplies bare From/To mailboxes; FromName adds the provider's
display name. Both text and HTML reach SendGrid. Every To recipient is visible
to the other recipients; use separate messages for private delivery.

## Posture and errors

Sender implements `notify.CapabilityReporter`. A valid HTTPS origin declares
TLS and production-capable posture. HTTP declares development-only posture. `notify.CheckTransport(mode, sender)` checks the
shared SDK policy; a custom HTTP transport remains the host's responsibility.

Message validation failures wrap `sdk.ErrInvalidInput`. A non-2xx response returns
`*ResponseError` with an inspectable StatusCode and a safe summary that omits
the provider body. `errors.As` reaches it through notify.Send errors too.
Mappings for `errors.Is` remain 400 → sdk.ErrInvalidInput, 401 →
sdk.ErrUnauthorized, 403 → sdk.ErrForbidden and 404 → sdk.ErrNotFound.
These auth errors concern provider credentials, not the application's user.
Other statuses retain their numeric code without inventing a domain mapping.

Unused response bodies are closed without reading. Transport errors preserve
their cause. A successful response means provider acceptance; retries and
recipient policy belong to the host. An error does not prove non-delivery.

## Verification and migration

`go test -race ./...` exercises owned HTTP listeners and fake transports:
request content, cancellation, concurrent request isolation, redirect refusal,
response closure, safe diagnostics and metadata. No real SendGrid delivery runs.

See [AUDIT-014](../../../AUDIT.md#audit-014-explicit-notification-deliveries-and-email-correctness)
for coordinated SDK/authentication migration. This module keeps its existing path;
its obsolete sendgrid/rest and x/net requirements were removed.
