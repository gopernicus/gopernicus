---
sidebar_position: 4
title: Adding an Infrastructure Adapter
---

# Adding an Infrastructure Adapter

Step-by-step guide for adding a new infrastructure adapter -- implementing an interface defined in the core or infrastructure layer and wiring it into the application.

Infrastructure adapters are the outermost layer in the hexagonal architecture. They provide concrete implementations for ports (interfaces) that the core layer depends on. Examples already in the codebase: `memorycache`, `rediscache`, `sendgridemailer`, `diskstorage`, `s3`, `gcs`, `memorybus`, `goredisbus`.

This walkthrough uses a fictional Twilio SMS adapter as a running example.

---

## Prerequisites

- An interface defined in the infrastructure or core layer that the adapter must satisfy
- Familiarity with the project's dependency injection pattern: construct in server setup, inject via constructor parameters

---

## Step 1: Identify or Define the Interface

Adapters implement interfaces. If the interface already exists (e.g., `cache.Cacher`, `storage.Client`, `events.Bus`), skip ahead. Otherwise, define it in the appropriate infrastructure package.

Gopernicus follows the Go idiom of defining interfaces where they are consumed, not where they are implemented.

For a new capability, create the interface package:

```
infrastructure/
  communications/
    sms/
      sms.go          <-- interface definition
      twiliosms/
        client.go     <-- Twilio implementation
        config.go     <-- configuration
```

Define the interface in `infrastructure/communications/sms/sms.go`:

```go
package sms

import "context"

// Sender defines the interface for sending SMS messages.
// Implementations: twiliosms, stdoutsms (for development).
type Sender interface {
    // Send sends an SMS message to the given phone number.
    Send(ctx context.Context, to, body string) error
}
```

Gopernicus convention favors many small interfaces over one large one — if you need multiple capabilities (send, receive, check status), separate interfaces make adapters easier to compose and test.

---

## Step 2: Implement the Adapter

Create the adapter package. The struct should hold configuration and any SDK clients. Follow the constructor pattern used throughout the codebase: `New()` returns a struct, functional options for optional configuration.

Create `infrastructure/communications/sms/twiliosms/client.go`:

```go
package twiliosms

import (
    "context"
    "fmt"
    "log/slog"
)

// Client implements sms.Sender using the Twilio API.
type Client struct {
    log       *slog.Logger
    accountID string
    authToken string
    fromPhone string
}

// Option configures a Client.
type Option func(*Client)

// New creates a new Twilio SMS client.
func New(log *slog.Logger, cfg Config, opts ...Option) *Client {
    c := &Client{
        log:       log,
        accountID: cfg.AccountID,
        authToken: cfg.AuthToken,
        fromPhone: cfg.FromPhone,
    }
    for _, opt := range opts {
        opt(c)
    }
    return c
}

// Send sends an SMS message via the Twilio API.
func (c *Client) Send(ctx context.Context, to, body string) error {
    c.log.DebugContext(ctx, "sending SMS", "to", to)

    // Twilio API call here...

    c.log.DebugContext(ctx, "SMS sent", "to", to)
    return nil
}
```

Existing adapters in the codebase demonstrate these patterns:

- **Structured logging** — `*slog.Logger` at Debug level for success, Error level for failures
- **Context propagation** — `context.Context` threaded through to external calls for tracing and cancellation
- **Error wrapping** — external errors wrapped with `fmt.Errorf("send sms: %w", err)` for clear error chains
- **No global state** — all state lives on the struct, injected via constructor

---

## Step 3: Add Configuration

Create `infrastructure/communications/sms/twiliosms/config.go`:

```go
package twiliosms

// Config holds Twilio SMS configuration.
// Populated from environment variables during server setup.
type Config struct {
    AccountID string `env:"TWILIO_ACCOUNT_ID"`
    AuthToken string `env:"TWILIO_AUTH_TOKEN"`
    FromPhone string `env:"TWILIO_FROM_PHONE"`
}
```

Add the environment variables to your `.env` file:

```
TWILIO_ACCOUNT_ID=AC...
TWILIO_AUTH_TOKEN=...
TWILIO_FROM_PHONE=+15551234567
```

The project loads `.env` automatically based on the `env_file` field in `gopernicus.yml`.

---

## Step 4: Create a Development Stub (recommended)

For local development and testing, create a stdout adapter that logs instead of making real API calls. This follows the pattern established by `stdoutemailer` and `memorycache`:

```go
package stdoutsms

import (
    "context"
    "fmt"
    "log/slog"
)

// Client logs SMS messages to stdout instead of sending them.
// Use this for local development and testing.
type Client struct {
    log *slog.Logger
}

// New creates a new stdout SMS client.
func New(log *slog.Logger) *Client {
    return &Client{log: log}
}

// Send logs the SMS message instead of sending it.
func (c *Client) Send(ctx context.Context, to, body string) error {
    c.log.InfoContext(ctx, "SMS (stdout)",
        "to", to,
        "body", body,
    )
    fmt.Printf("\n--- SMS to %s ---\n%s\n---\n\n", to, body)
    return nil
}
```

---

## Step 5: Wire in server.go

Construct the adapter during server setup and inject it into cases or other components that need it.

```go
// Choose adapter based on environment
var smsSender sms.Sender
if cfg.Environment == "production" {
    smsSender = twiliosms.New(log, twiliosms.Config{
        AccountID: env.MustGet("TWILIO_ACCOUNT_ID"),
        AuthToken: env.MustGet("TWILIO_AUTH_TOKEN"),
        FromPhone: env.MustGet("TWILIO_FROM_PHONE"),
    })
} else {
    smsSender = stdoutsms.New(log)
}

// Inject into a case that needs SMS
notificationCase := notifications.New(
    log,
    eventBus,
    smsSender,  // satisfies sms.Sender interface
)
```

The adapter is constructed at the outermost layer (server setup) and passed inward through constructor injection. The core case only knows about the `sms.Sender` interface -- it never imports the concrete adapter package.

---

## Step 6: Write a Compliance Test Suite (recommended)

For adapters with multiple implementations, create a shared test suite that verifies any implementation satisfies the interface contract. This pattern is used by `cachetest`, `emailertest`, `storagetest`, and `eventstest`:

Create `infrastructure/communications/sms/smstest/suite.go`:

```go
package smstest

import (
    "context"
    "testing"

    "your-module/infrastructure/communications/sms"
)

// Suite runs compliance tests against any sms.Sender implementation.
func Suite(t *testing.T, sender sms.Sender) {
    t.Helper()

    t.Run("Send", func(t *testing.T) {
        err := sender.Send(context.Background(), "+15559876543", "test message")
        if err != nil {
            t.Fatalf("Send() error: %v", err)
        }
    })
}
```

Then in each implementation's test file:

```go
func TestCompliance(t *testing.T) {
    client := stdoutsms.New(logger.NewNoop())
    smstest.Suite(t, client)
}
```

---

## Directory Structure Summary

After adding the adapter, the directory structure looks like:

```
infrastructure/
  communications/
    sms/
      sms.go                   <-- Sender interface
      smstest/
        suite.go               <-- compliance test suite
      twiliosms/
        client.go              <-- production implementation
        config.go              <-- configuration struct
      stdoutsms/
        client.go              <-- development stub
        client_test.go         <-- unit tests
        compliance_test.go     <-- compliance suite runner
```

This follows the established pattern seen in:
- `cache/` with `memorycache/`, `rediscache/`, `noopcache/`
- `storage/` with `diskstorage/`, `s3/`, `gcs/`
- `events/` with `memorybus/`, `goredisbus/`, `outbox/`
- `emailer/` with `sendgridemailer/`, `stdoutemailer/`

---

## Checklist

- [ ] Interface defined (or existing interface identified)
- [ ] Adapter struct implemented with constructor, logging, and context propagation
- [ ] Configuration struct defined with `env` tags
- [ ] Environment variables added to `.env`
- [ ] Development stub created for local use
- [ ] Adapter wired in server.go with environment-based selection
- [ ] Compliance test suite created (if multiple implementations)
- [ ] Unit tests pass for all implementations

---

## Related

- [Database](../gopernicus/infrastructure/database/overview.md)
- [Cache](../gopernicus/infrastructure/cache.md)
- [Events](../gopernicus/infrastructure/events.md)
- [Storage](../gopernicus/infrastructure/storage.md)
- [Adding a New Entity](adding-new-entity.md)
- [Adding a Use Case](adding-use-case.md)
