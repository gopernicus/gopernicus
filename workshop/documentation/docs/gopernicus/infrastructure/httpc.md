---
sidebar_position: 7
title: HTTP Client
---

# Infrastructure — HTTP Client

`github.com/gopernicus/gopernicus/infrastructure/httpc`

`httpc` is a JSON HTTP client with sensible defaults and OpenTelemetry tracing built in. It handles content negotiation, connection pooling, and request/response marshaling so you don't have to repeat that boilerplate for every external API your app talks to.

## Creating a client

```go
client := httpc.NewClient(
    httpc.WithBaseURL("https://api.example.com"),
    httpc.WithBearerToken("my-token"),
)
```

`NewClient` ships with a 30-second timeout, a pooled transport (`MaxIdleConns: 100`), and `Content-Type: application/json` / `Accept: application/json` headers by default.

## Making requests

Each HTTP method accepts a `context.Context`, a path, and optional body/result pointers.

```go
// Unmarshal into a pointer you already have
var user User
err := client.Get(ctx, "/users/123", &user)

// POST with a body, unmarshal the response
var created User
err := client.Post(ctx, "/users", CreateUserRequest{Name: "alice"}, &created)

// DELETE with no response body
err := client.Delete(ctx, "/users/123", nil)
```

Pass `nil` as the result when you don't need the response body.

### Generic helpers

If you prefer to receive the value directly rather than passing a pointer, use the generic variants:

```go
user, err := httpc.GetValue[User](client, ctx, "/users/123")

created, err := httpc.PostValue[User](client, ctx, "/users", CreateUserRequest{Name: "alice"})
```

`GetValue`, `PostValue`, `PutValue`, `PatchValue`, and `DeleteValue` all follow the same pattern.

### Absolute URLs

If a path starts with `http://` or `https://`, the base URL is bypassed:

```go
// Uses the full URL as-is, ignoring WithBaseURL
err := client.Get(ctx, "https://other.example.com/data", &result)
```

## Authentication

```go
httpc.WithBearerToken("token")           // Authorization: Bearer <token>
httpc.WithBasicAuth("user", "pass")      // Authorization: Basic <base64>
httpc.WithAPIKey("X-API-Key", "secret")  // any custom header
```

## Custom headers

```go
httpc.WithHeader("X-Request-Source", "my-app")
httpc.WithUserAgent("my-app/1.0")

// Set multiple at once
httpc.WithHeaders(map[string]string{
    "X-Tenant-ID": tenantID,
    "X-Version":   "2",
})
```

## Error handling

Any non-2xx response is returned as `*httpc.Error`. Use `IsError` to check and inspect it:

```go
err := client.Get(ctx, "/users/999", &user)
if e, ok := httpc.IsError(err); ok {
    switch {
    case e.IsNotFound():
        // 404
    case e.IsUnauthorized():
        // 401
    case e.IsForbidden():
        // 403
    case e.IsServerError():
        // 5xx
    }
    // e.StatusCode, e.Status, e.Body are all available
}
```

## Tracing

The main reason to reach for `httpc` over rolling your own client is the tracing transport. `NewTracingTransport` wraps any `http.RoundTripper` and produces an OpenTelemetry client span for each outbound request — including method, host, status code, and W3C trace context propagation via headers.

```go
tracer := provider.Tracer()

client := httpc.NewClient(
    httpc.WithBaseURL("https://api.example.com"),
    httpc.WithTransport(
        httpc.NewTracingTransport(tracer, http.DefaultTransport),
    ),
)
```

Query parameters are stripped from span attributes to avoid leaking sensitive data.

## Transport and timeout

```go
// Override timeout
httpc.WithTimeout(10 * time.Second)

// Bring your own transport (proxies, TLS, retries, etc.)
httpc.WithTransport(myTransport)

// Or replace the entire underlying http.Client
httpc.WithHTTPClient(myHTTPClient)
```

`WithTransport` and `WithHTTPClient` are escape hatches — use them when the default transport configuration doesn't fit your needs.

## Wiring example

`httpc.Client` is a concrete type, similar to how you'd wire a database connection. Wrap it in your own interface if your use case requires a mock.

```go
// In your App or DI container
githubClient := httpc.NewClient(
    httpc.WithBaseURL(cfg.GithubAPIURL),
    httpc.WithBearerToken(cfg.GithubToken),
    httpc.WithTransport(
        httpc.NewTracingTransport(tracer, http.DefaultTransport),
    ),
)
```

## See also

- [Infrastructure / Tracing](/docs/gopernicus/telemetry/overview) — `telemetry.Tracer` used by `NewTracingTransport`
