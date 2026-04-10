---
sidebar_position: 2
title: Sensitive operations
---

# Implementing sensitive operations

A "sensitive operation" is a destructive change that an authenticated user is
allowed to perform on their own account, but which we want gated behind a
fresh proof of inbox ownership. The framework ships two of these out of the
box:

- **Remove password** — `POST /auth/password/remove` (paired send endpoint at
  `POST /auth/password/remove/send-code`)
- **Unlink OAuth provider** — `DELETE /auth/oauth/unlink/{provider}` (paired
  send endpoint at `POST /auth/oauth/unlink/{provider}/send-code`)

Both follow the same shape: the user requests a 6-digit verification code
delivered by email, then confirms the destructive action by re-presenting
that code on the actual mutation endpoint. The code expires, has a wrong-guess
lockout, is one-shot, and is bound to a single operation purpose so a code
issued to remove a password can never be reused to unlink an OAuth account.

This page describes the framework's two-layer extension pattern so you can
add your own application-specific sensitive operations — confirming a wire
transfer, changing a billing address, exporting account data, etc. — using
the same primitives and the same security guarantees.

## The two layers

The framework splits the work into two layers so each is type-safe end to end.

### Layer 1 — Generic verification code primitives (in `core/auth/authentication`)

```go
// Issue a code keyed by (user_id, purpose). Returns the plaintext code.
// Does NOT emit any event — the caller is responsible for emitting their own
// strongly-typed event carrying the code.
func (a *Authenticator) IssueVerificationCode(
    ctx context.Context,
    userID, purpose string,
    opts ...IssueOption,
) (string, error)

// Validate and consume a code. Returns any stored context bytes attached at
// issue time, so the caller can validate that the code was issued for the
// same resource the user is acting on (e.g. provider, transaction id).
func (a *Authenticator) ConsumeVerificationCode(
    ctx context.Context,
    userID, code, purpose string,
) (storedContext []byte, err error)

// Optional: bind arbitrary JSON-serializable context to the code.
func WithStoredContext(v any) IssueOption
```

These provide the reusable mechanism: code generation, hashing, storage,
expiry, attempt counting, lockout, one-shot consumption. They're purpose-
agnostic — you pass a purpose string and the framework handles everything
else.

### Layer 2 — Your sensitive operation (in your application code)

For each sensitive operation you build:

1. **A purpose constant** — a stable string namespaced to your application
2. **A typed event** — a struct with the fields your email subscriber needs
3. **Send + verify methods** — wrappers around the layer-1 primitives that
   emit your typed event
4. **An email template + subscriber handler** — to deliver the code
5. **HTTP send + action handlers** — wired to your routes

The framework's built-in remove-password and unlink-OAuth operations follow
exactly this pattern. Reading
[`core/auth/authentication/sensitive.go`](https://github.com/gopernicus/gopernicus/blob/main/core/auth/authentication/sensitive.go)
and
[`bridge/auth/authentication/subscribers.go`](https://github.com/gopernicus/gopernicus/blob/main/bridge/auth/authentication/subscribers.go)
will show you the canonical shape.

## Worked example: confirm a wire transfer

Suppose your application has a "send wire transfer" feature and you want to
gate it behind an email confirmation code. The user enters the transfer
details, clicks "send code," confirms the code from email, and the transfer
goes through.

### 1. Define the purpose

```go
// myapp/core/transfers/sensitive.go
package transfers

const PurposeConfirmWireTransfer = "confirm_wire_transfer"
```

Use a stable, namespaced string. This goes into the `purpose` column of the
`verification_codes` table; changing it later invalidates any in-flight codes.
Treat it like a public API surface.

### 2. Define your typed event

```go
// myapp/core/transfers/events.go
package transfers

import "github.com/gopernicus/gopernicus/infrastructure/events"

const EventTypeWireTransferConfirmRequested = "myapp.wire_transfer_confirm_requested"

// WireTransferConfirmRequestedEvent is emitted when a user requests a code
// to confirm a pending wire transfer.
type WireTransferConfirmRequestedEvent struct {
    events.BaseEvent
    UserID      string `json:"user_id"`
    Email       string `json:"email"`
    DisplayName string `json:"display_name,omitempty"`
    Code        string `json:"code"`
    ExpiresIn   string `json:"expires_in"`
    // Per-operation context — strongly typed, so the email template can
    // render specific copy ("Confirm $1,000 transfer to Jane").
    TransferID  string `json:"transfer_id"`
    AmountCents int64  `json:"amount_cents"`
    Recipient   string `json:"recipient"`
}

func (e WireTransferConfirmRequestedEvent) Type() string {
    return EventTypeWireTransferConfirmRequested
}
```

The event type is yours to define. Strong typing on every field means the
subscriber and the template can rely on the contract without runtime type
assertions.

### 3. Send and verify methods

```go
// myapp/core/transfers/sensitive.go
package transfers

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/gopernicus/gopernicus/core/auth/authentication"
    "github.com/gopernicus/gopernicus/infrastructure/events"
)

// wireTransferCodeContext is the JSON shape stored in the verification code's
// Data field. Bind the code to a specific transfer so a code issued for one
// transfer cannot be used to confirm a different one.
type wireTransferCodeContext struct {
    TransferID string `json:"transfer_id"`
}

type Service struct {
    Auth *authentication.Authenticator
    Bus  events.Bus
    // ... your transfer repo, etc.
}

// SendConfirmCode issues a verification code bound to the given transfer
// and emits a WireTransferConfirmRequestedEvent for email delivery.
func (s *Service) SendConfirmCode(ctx context.Context, userID, transferID string) error {
    transfer, err := s.lookupPendingTransfer(ctx, userID, transferID)
    if err != nil {
        return err
    }
    user, err := s.lookupUser(ctx, userID) // your repo
    if err != nil {
        return err
    }

    code, err := s.Auth.IssueVerificationCode(ctx, userID, PurposeConfirmWireTransfer,
        authentication.WithStoredContext(wireTransferCodeContext{TransferID: transferID}),
    )
    if err != nil {
        return fmt.Errorf("issue confirm code: %w", err)
    }

    if err := s.Bus.Emit(ctx, WireTransferConfirmRequestedEvent{
        UserID:      user.UserID,
        Email:       user.Email,
        DisplayName: user.DisplayName,
        Code:        code,
        ExpiresIn:   "15 minutes", // or pull from config
        TransferID:  transfer.ID,
        AmountCents: transfer.AmountCents,
        Recipient:   transfer.Recipient,
    }); err != nil {
        return fmt.Errorf("emit confirm event: %w", err)
    }
    return nil
}

// VerifyConfirmCode validates a code AND enforces that it was issued for the
// same transfer the user is now confirming. A code issued for transfer A
// cannot be used to confirm transfer B.
func (s *Service) VerifyConfirmCode(ctx context.Context, userID, code, transferID string) error {
    storedContext, err := s.Auth.ConsumeVerificationCode(ctx, userID, code, PurposeConfirmWireTransfer)
    if err != nil {
        return err
    }

    var bound wireTransferCodeContext
    if len(storedContext) > 0 {
        if err := json.Unmarshal(storedContext, &bound); err != nil {
            return authentication.ErrCodeInvalid
        }
    }
    if bound.TransferID != transferID {
        // Provider/resource mismatch — treat as a failed verification.
        // The code is already consumed; the user must request a new one.
        return authentication.ErrCodeInvalid
    }
    return nil
}
```

The pattern mirrors the framework's built-in `SendUnlinkOAuthCode` /
`VerifyUnlinkOAuthCode` exactly. Note in particular:

- **`WithStoredContext`** binds the code to a specific resource. The stored
  context is opaque bytes from the framework's perspective — your code is
  the only thing that knows the shape. Always validate the bound context
  in your verify method, and treat a mismatch as `ErrCodeInvalid`.
- **The code is consumed even on a context mismatch.** This is the safer
  default: it forces the user to request a new code rather than allowing
  them to retry with a different resource ID. Don't try to "uncomsume" the
  code on a mismatch — that would let an attacker probe context bindings.
- **Don't re-use framework purpose constants** like `PurposeRemovePassword`
  or `PurposeUnlinkOAuth` — pick your own.

### 4. Email template and subscriber handler

Create your template under your application's emailer namespace:

```go
// myapp/bridge/transfers/subscribers.go
package transfers

import (
    "context"
    "embed"
    "fmt"
    "log/slog"

    "github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
    "github.com/gopernicus/gopernicus/infrastructure/events"
)

//go:embed templates/*
var transferTemplates embed.FS

func TransferTemplates() embed.FS { return transferTemplates }

type Subscribers struct {
    emailer *emailer.Emailer
    log     *slog.Logger
    subs    []events.Subscription
}

func NewSubscribers(e *emailer.Emailer, log *slog.Logger) *Subscribers {
    return &Subscribers{emailer: e, log: log}
}

func (s *Subscribers) Register(bus events.Bus) error {
    sub, err := bus.Subscribe(
        EventTypeWireTransferConfirmRequested,
        events.TypedHandler(s.handleWireTransferConfirm),
    )
    if err != nil {
        return fmt.Errorf("subscribe: %w", err)
    }
    s.subs = append(s.subs, sub)
    return nil
}

func (s *Subscribers) handleWireTransferConfirm(ctx context.Context, e WireTransferConfirmRequestedEvent) error {
    return s.emailer.RenderAndSend(ctx, emailer.SendRequest{
        To:       e.Email,
        Subject:  fmt.Sprintf("Confirm wire transfer to %s", e.Recipient),
        Template: "transfers:wire_transfer_confirm",
        Data: map[string]any{
            "DisplayName": e.DisplayName,
            "Code":        e.Code,
            "ExpiresIn":   e.ExpiresIn,
            "Amount":      formatCents(e.AmountCents),
            "Recipient":   e.Recipient,
        },
    })
}
```

And register the templates during app wiring:

```go
emailer.WithContentTemplates("transfers", transfers.TransferTemplates(), emailer.LayerCore)
```

Templates can branch on the strongly-typed fields you defined on your event —
no `map[string]any` lookups, no type assertions.

### 5. Bridge HTTP handlers

```go
// myapp/bridge/transfers/http.go
func (b *Bridge) httpTransferConfirmSendCode(w http.ResponseWriter, r *http.Request) {
    userID := httpmid.GetSubjectID(r.Context())
    transferID := r.PathValue("transfer_id")

    if err := b.transfers.SendConfirmCode(r.Context(), userID, transferID); err != nil {
        web.RespondJSONError(w, web.ErrFromDomain(err))
        return
    }
    web.RespondJSON(w, http.StatusOK, SuccessResponse{
        Success: true,
        Message: "verification code sent",
    })
}

func (b *Bridge) httpTransferConfirm(w http.ResponseWriter, r *http.Request) {
    req, err := web.DecodeJSON[ConfirmTransferRequest](r)
    if err != nil {
        web.RespondJSONError(w, web.ErrValidation(err))
        return
    }

    userID := httpmid.GetSubjectID(r.Context())
    transferID := r.PathValue("transfer_id")

    if err := b.transfers.VerifyConfirmCode(r.Context(), userID, req.VerificationCode, transferID); err != nil {
        web.RespondJSONError(w, web.ErrFromDomain(err))
        return
    }

    if err := b.transfers.Execute(r.Context(), userID, transferID); err != nil {
        web.RespondJSONError(w, web.ErrFromDomain(err))
        return
    }
    web.RespondNoContent(w)
}
```

Register routes alongside your other transfer routes:

```go
group.POST("/transfers/{transfer_id}/confirm/send-code",
    b.httpTransferConfirmSendCode,
    rl("transfers:confirm-send", ratelimiter.PerMinute(10)),
    authMid,
)
group.POST("/transfers/{transfer_id}/confirm",
    b.httpTransferConfirm,
    rl("transfers:confirm", ratelimiter.PerMinute(10)),
    authMid,
)
```

That's the whole pattern. Your sensitive operation now has the same security
properties as the framework's built-ins:

- 6-digit code, hashed at rest, constant-time compared
- Configurable expiry (`Authenticator.config.VerificationCodeExpiry`)
- Wrong-guess lockout (`Authenticator.config.MaxVerificationAttempts`)
- One-shot consumption — can't be replayed
- Resource binding via `WithStoredContext` — can't be reused for a different transfer
- Per-purpose isolation — can't be confused with codes for other operations
- Rate limited at the bridge

## Design rules

A few guidelines that come from the framework's own conventions:

**One purpose per operation.** Don't reuse a purpose across multiple
operations. Two operations sharing a purpose can read each other's codes.
The whole point of `purpose` is operation-level isolation.

**One event type per operation.** Don't try to multiplex multiple sensitive
operations through a single generic event with a `Data any` field. Per-op
event types give you compile-time safety on the data shape, make subscribers
explicit, and let templates branch on real fields. The framework's built-ins
follow this rule and so should yours.

**Always validate stored context in your verify method.** If you bound the
code to a resource via `WithStoredContext`, check it in
`VerifyVerificationCode...`. A code that decodes to a different resource ID
than the user is acting on must return `ErrCodeInvalid`.

**Treat context mismatches as failed verifications, not retryable errors.**
The code gets consumed either way. Forcing a re-issue is the safer default —
it prevents an attacker who somehow obtained a code from probing different
resource bindings.

**Keep messages generic on rejection paths in the HTTP layer; rely on codes.**
The framework's `httpErrFor` returns generic messages with stable codes for
verification failures (`verification_code_expired`, `verification_code_invalid`,
`too_many_attempts`). Your bridge handlers should do the same — frontends
branch on the code, not on human-readable copy.

**Email subjects and bodies CAN be specific.** This is the opposite advice
from HTTP responses. Email goes to the user's inbox, not to an attacker with
a stolen session. Naming the operation explicitly ("Confirm $1,000 wire
transfer to Jane Smith") helps the user notice unauthorized activity. Be as
specific as the user-experience benefits.

**Don't bypass the framework's verification machinery.** Don't roll your own
6-digit codes, your own hashing, your own attempt counter. The crypto and
the lockout logic are battle-tested. Use `IssueVerificationCode` and
`ConsumeVerificationCode` and put your operation-specific concerns in
wrappers around them.

## What lives where

| Concern | Layer | File |
|---|---|---|
| Code generation, hashing, storage, expiry, attempts, lockout | core | `core/auth/authentication/sensitive.go` |
| Per-operation purpose constants | application | yours, namespaced |
| Per-operation typed events | application | yours |
| Per-operation send/verify methods | application | yours, wrapping `IssueVerificationCode` / `ConsumeVerificationCode` |
| Email templates | application | embedded under your bridge namespace |
| Email subscriber handlers | application | yours |
| HTTP handlers + routes | bridge | yours |

The framework owns the security primitives. You own the operation contract,
the typed event, the email content, and the HTTP wiring. Each layer is
strongly typed and explicit — there is no shared "data map" path that
trades type safety for genericity.
