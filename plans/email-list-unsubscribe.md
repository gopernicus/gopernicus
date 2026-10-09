# Email List-Unsubscribe / one-click headers (sdk notify/email + sendgrid) — issue #64

Status: RATIFIED 2026-10-09 (owner: "do it"). RELEASED 2026-10-09 — sdk/v0.11.0 @ 7cc785ea
(PR #65) and integrations/email/sendgrid/v0.4.0 @ 0d0b6f4d (PR #66, pins sdk v0.11.0), both
cold-verified from the public proxy (see RELEASING.md). Owner merged #66 before the live check;
T5 (real SendGrid → Gmail one-click send) remains OPEN. Build correction: the sendgrid pin moved in
a follow-up commit after the sdk tag (Task 4), not via a local replace. Direction agreed in-session 2026-10-09
(owner: "yeah agree. plan it out" to: typed field in the port, per-sender wire mapping,
OneClick refused for multi-recipient messages, `SendRequest` plumbing in scope).

## Problem

`sdk/capabilities/notify/email.Message` (`email.go`) has no way to carry message headers, so a
host cannot send RFC 2369 `List-Unsubscribe` or RFC 8058 `List-Unsubscribe-Post:
List-Unsubscribe=One-Click`. Gmail and Yahoo expect one-click unsubscribe on recurring mail, and
clients only show their native Unsubscribe button when the headers are present.

Consumer: Segovia v2 discussion notifications (mention + followed-thread), per-recipient,
per-tenant signed unsubscribe URL.

## Principle: intent in the port, encoding in the adapter

The unsubscribe target is per-recipient data, so it must ride the `Message`. Sender config
(`SMTPConfig`, `sendgrid.Config`) is host-static and cannot carry it. The port carries a typed
*intent* and validates it once; each `Sender` maps that intent to its wire format. A free-form
`Headers map` is rejected: it moves header-injection safety onto every adopter.

## Rulings

- **R1 Typed value field, not pointer.** `Message.Unsubscribe Unsubscribe`; the zero value means
  "none". The issue proposed `*Unsubscribe`. A value type keeps `NewDelivery`'s snapshot promise
  (`delivery.go` clones `To` so later caller mutation cannot leak into the send) without a deep
  copy, and all fields are immutable strings/bool.
- **R2 OneClick is single-recipient.** `Validate()` refuses `OneClick` when `len(To) > 1`: a
  per-recipient signed one-click URL on a shared message lets any visible recipient unsubscribe
  the others. A non-one-click `URL`/`Mailto` on a multi-recipient message stays allowed
  (list-level unsubscribe pages are legitimate).
- **R3 `SendRequest` plumbing in scope.** `Emailer.RenderAndSend` (`emailer.go:51`) builds the
  `Message`; without `SendRequest.Unsubscribe` hosts on the template path cannot use the feature.
- **R4 No capability flag.** A sender that cannot express the headers must return an error wrapping
  `sdk.ErrInvalidInput` rather than drop them (documented on `Sender`). No
  `notify.Capabilities` field until a second sender actually needs one.
- **R5 `Branding.UnsubscribeURL` untouched.** It is a static per-`Emailer` body link for
  `LayoutMarketing`; the header is per-send. No change to the layout matrix
  (`TestBundledLayoutBrandingMatrix` stays green unchanged). Docs distinguish the two.

## Design

### D1. Port type (`sdk/capabilities/notify/email/email.go`)

```go
// Unsubscribe requests RFC 2369 List-Unsubscribe and, with OneClick, RFC 8058
// List-Unsubscribe-Post headers. The zero value sends neither.
type Unsubscribe struct {
	URL      string // absolute https URL, ASCII; required when OneClick
	Mailto   string // optional bare ASCII mailbox, rendered as <mailto:...>
	OneClick bool   // adds List-Unsubscribe-Post: List-Unsubscribe=One-Click
}

func (u Unsubscribe) IsZero() bool
```

`Message` gains `Unsubscribe Unsubscribe` (doc line: per-recipient URLs need per-recipient
messages; see R2). Type declared after `Message`, before `Sender` (vars → types → interfaces order
already holds in the file).

### D2. Validation (`Message.Validate`, failures wrap `sdk.ErrInvalidInput`, no input echoed)

Skipped entirely when `Unsubscribe.IsZero()`. Otherwise:

- At least one of `URL` / `Mailto`.
- `OneClick` requires `URL`; `OneClick` requires `len(To) == 1` (R2).
- `URL`: ≤ 900 bytes (each list element sits on its own folded line under the 998-byte RFC 5322
  limit); ASCII printable only — no space, control chars, `<` or `>` (they would break the
  bracketed list); `url.Parse` succeeds with scheme `https`, non-empty host, no userinfo, no
  fragment.
- `Mailto`: existing `validateMailbox`, plus ASCII-only and no `"`, `<`, `>`, `?` (so the value is
  emitted verbatim inside `<mailto:...>` with no URI encoding).

### D3. SMTP (`smtp.go`)

In `writeAddressHeaders`, after `Subject`, when set:

```
List-Unsubscribe: <https://...>,\r\n <mailto:...>\r\n
List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n      (OneClick only)
```

URL first when both are present (one-click clients use the https entry). Doc comment on `SMTP`:
RFC 8058 requires both headers to be covered by DKIM; this sender does not sign, so the relay must
sign them.

### D4. Console (`console.go`)

Add `list_unsubscribe` and `list_unsubscribe_post` log attributes only when set (the console
already logs magic links; it is development-only).

### D5. Emailer (`emailer.go`)

`SendRequest.Unsubscribe Unsubscribe`, copied into the built `Message`. No renderer change.

### D6. `Sender` doc

"A Sender that cannot deliver a non-zero `Message.Unsubscribe` must return an error wrapping
sdk.ErrInvalidInput rather than send without the headers."

### D7. SendGrid (`integrations/email/sendgrid/sendgrid.go`)

When set, `m.SetHeader("List-Unsubscribe", ...)` and, for OneClick,
`m.SetHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")` on the top-level v3
`headers` object (one personalization, so top-level is equivalent and simpler). The header value
is built by the same rule as SMTP but on one line (JSON, no folding). Build it with an
adapter-local helper, not an exported sdk func — keeps the sdk surface to the type.

README: Subscription Tracking (account-level) may insert SendGrid's own `List-Unsubscribe`;
hosts using this field should leave it off. **To verify with one live send before tagging (T5).**

## Tasks

1. **sdk port + validation** — D1, D2, D6. Tests: table rows in `email_test.go` for every D2 rule
   (http URL, userinfo, fragment, CR/LF in URL and Mailto, `>` in URL, non-ASCII, 901-byte URL,
   OneClick without URL, OneClick with 2 recipients, Mailto-only valid, URL+Mailto valid, zero
   value valid); error never contains the input.
2. **SMTP + Console** — D3, D4. Tests: parse `buildMessage` output with `net/mail.ReadMessage`
   (the `Date` header makes byte goldens flaky) and assert exact header values for nil / URL only /
   Mailto only / both / OneClick; assert absence when zero. Extend `smtp_protocol_test.go`'s fake
   server path with one OneClick case. Console: attributes present/absent.
3. **Emailer plumbing** — D5. Test: `RenderAndSend` with `Unsubscribe` reaches the mock sender
   unchanged; `NewDelivery` snapshot test gains an `Unsubscribe` assertion.
4. **SendGrid** — D7. The `sdk` require stays v0.9.0 in the PR (the workspace resolves the local
   sdk); a separate pin commit moves it to v0.11.0 after that tag publishes, matching the
   `d71e1a1a` precedent. (Build correction: originally "local `replace`".) Tests: decode the request JSON (existing `roundTripFunc` pattern) and
   assert `headers` exactly with and without `Unsubscribe`; invalid `Unsubscribe` makes no request.
   README section.
5. **Live check (owner)** — one real SendGrid send to a Gmail inbox with OneClick: native
   Unsubscribe shows, "Show original" has exactly one `List-Unsubscribe` and DKIM `h=` covers both
   headers. Same for SMTP if a signing relay is at hand (optional).
6. **Docs** — package doc in `email.go` gets a short "Unsubscribe headers" section (header vs
   `Branding.UnsubscribeURL`, DKIM note, single-recipient one-click rule). RELEASING.md entry.

## Verify

```
cd sdk && go build ./... && go vet ./... && go test ./capabilities/notify/...
cd integrations/email/sendgrid && go build ./... && go vet ./... && go test ./...
make guard    # layering guards unchanged; confirm green
```

## Release

Two PRs, stacked, same train:

- PR 1 (sdk): `sdk/v0.11.0` — MINOR (new exported type + fields; `Validate` only stricter when
  the new field is set).
- PR 2 (sendgrid): `integrations/email/sendgrid/v0.4.0` — MINOR; pin commit to `sdk v0.11.0`
  after the sdk tag's proxy `.info` resolves, then tag.

Standing release discipline: tag from the main merge commit; merge / tag / push as separate steps;
poll the proxy `.info` before `go mod tidy` against a new tag; retarget PR 2 to main before merging
PR 1 and never `--delete-branch` the stacked base; cold-verify each tag with `GOWORK=off`.
Not bumped: pockets (they build `Message` without the field — unaffected), examples, workshop
scaffold pins (already trail at v0.9.0, by precedent).

## Downstream (owner)

- Segovia v2: repin sdk (+ sendgrid if used), set `Unsubscribe{URL, OneClick: true}` per recipient
  on discussion notifications; its one-click endpoint must accept `POST` with body
  `List-Unsubscribe=One-Click`, no cookies/CSRF (RFC 8058), and be idempotent.
- Any host-owned `email.Sender` (hub, gps-360-go, Segovia) must honor or refuse the field per D6 —
  grep for `Send(ctx context.Context, msg email.Message)` before repinning.

## Out of scope

Generic header map; `Precedence`/`List-Id` headers; SendGrid ASM groups; capability flag (R4);
hosting a one-click endpoint in any pocket.
