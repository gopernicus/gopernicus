# SDK audit S9b: Email and notifications

Status: REVIEW COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
Review-only slice following the completed filestorage implementation. The owner
asks whether notify should be the SDK abstraction, with email/SMS/DM as pluggable
notifiers. Audit correctness and package boundaries together before source edits.

Implementation follow-up: [email-notify-implementation.md](email-notify-implementation.md)
implements the findings and the owner's subsequent per-call typed-delivery selection.
That approved design supersedes the common-text-only/single-delivery proposal below.
This document retains the historical review baseline and reproductions; consult
[AUDIT-014](../AUDIT.md#audit-014-explicit-notification-deliveries-and-email-correctness)
for implemented APIs and consumer migration. Do not rerun old API probes unchanged.

## Preconditions and scope

- Branch/base: firestore-authentication / 6807ed06; 661 prior dirty entries.
- Baseline /tmp/gopernicus-email-notify-review-baseline.json covers 1989 visible
  files. Preserve prior audits and user changes. AUDIT.md remains through AUDIT-013;
  recommendations do not create an implementation/migration entry.
- Go 1.26.1; 42 modules, no root go.mod. Use
  GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache. Review code remains unchanged.
- Inspect SDK email/notify, mailer bridge/SendGrid, authentication delivery and CMS
  consumers, original framework and current read-only app samples. No external
  delivery, real credentials, cloud APIs, consumer edits or production mutations.
- Repository changes are this plan and the master handoff. Independent temporary
  probes may use synthetic data and owned loopback/fake transports only; no email,
  SMS or DM is sent to a real recipient. Named project reviews remain read-only.

## Review sequence

1. Separate notification intent, destination/channel, rendering, provider transport
   and durable orchestration. Inspect what each current type actually owns.
2. Trace current framework/consumer wiring and original intent. Keep deliberately
   unfinished channel seams; lack of production adoption alone is not removal proof.
3. Compare the current sibling packages/bridge with a notification umbrella and
   channel adapters. Explain actual SDK placement (both packages are already SDK)
   and distinguish root package promotion from a coherent subsystem namespace.
   Challenge capability-import rules when they create unnecessary bridge modules.
4. Check validation, protocol construction, cancellation, recipient visibility,
   template ownership/escaping and fallback semantics, production metadata and
   provider error handling. Reproduce important gaps with temporary probes.
5. Run focused module build/test/vet and appropriate race tests; record skipped
   services. Use primary documentation for protocol/framework comparisons when
   needed. Finish recommendations, compatibility costs and a durable next step.

## Initial context

Notify is already sdk/capabilities/notify. It has a one-address Notifier port and
minimal Subject/Body message, keyed by sdk.IdentityAddress.Kind. Email has a typed
Message/Sender, SMTP/Console defaults and an optional rendering Emailer. The
zero-dependency integrations/notify/mailer bridge adapts Sender to Notifier; its
module location exists specifically because capabilities cannot import siblings.
Authentication owns routing, purpose-specific rendering and queued delivery.

The owner's question is interpreted as strengthening that common notification
boundary and clarifying channel composition, not merely moving types to root sdk.
No choice is accepted yet; preserve standalone rich email and intentional future
SMS/DM seams while comparing the smallest useful API shape.

## Architecture recommendation (proposed, not implemented)

The owner subsequently observed that notifier/email had ended up in a confusing
place. Agree with the concrete diagnosis: the common port cannot represent the
rich email already supported, while authentication accepts both routes. This is
more than an import-path problem.

Organize a small notification subsystem around these responsibilities:

| Responsibility | Owner / boundary |
| --- | --- |
| Why to notify, eligibility, consent, audience, digest | Application or pocket |
| Explicit delivery channel and resolved destination | Small SDK notify boundary |
| Email subject, plain text, HTML and branded rendering | Typed email channel API |
| One email transport attempt | email.Sender; SMTP or SendGrid implementation |
| Optional collection of channels | Thin explicit dispatcher, if needed by callers |
| Transactional intent, queueing, retries and outcomes | Application/pocket using existing work/jobs/workers |

Preferred package direction for the owner's umbrella vocabulary:

```text
sdk/capabilities/notify/        common notification delivery contract
sdk/capabilities/notify/email/  typed Message/Sender, rendering, email notifier adapter
integrations/email/sendgrid/   external provider implementing the email Sender
```

Future SMS/chat channel packages can join this subsystem when implemented; do
not scaffold empty packages or assume every DM system has the same rich payload.
Keep this out of root package sdk: it is a coherent feature with transports and
templates, unlike the small root helpers consolidated earlier.

An equally viable, smaller migration keeps sibling email/notify and permits a
documented one-way email-to-notify import. The backend reviewer prefers that
physical layout for minimal churn. Both remove the zero-dependency
integrations/notify/mailer module: its existence isolates no external dependency.
The umbrella layout offers clearer grouping at the cost of changing email
imports. Existing G12 permits imports within the same capability prefix; update
architecture wording and checks according to the chosen semantics, not merely
to satisfy a directory rule. Exact public signatures are not frozen here.

Keep email.Sender/Message directly usable. An email notifier wraps a Sender with
host-owned From/defaults to deliver the common message; SMTP and SendGrid do not
need to know identity policy. For an initial common contract, prefer explicitly
plain-text messages. Rich email retains its typed API. Do not advertise the
common text route as a transparent replacement for rich email, or silently drop
HTML. If a unified rich notification is later required, design explicit per-channel
representations from real examples instead of an any payload or growing options map.

Authentication should have one unambiguous rich-email route through email.Sender
and an independently constructed renderer. Remove the redundant email-kind
Notifier override if adopting this common-text contract. Other channels retain
the generic seam. This preserves auth's rendered HTML and avoids requiring a
dummy Mailer for a second email transport. Any different choice must demonstrate
how queued HTML/text survives end to end before replacing the current wiring.

A collection should select a channel explicitly. Address kind is not channel:
SMS and WhatsApp can use the same phone number. Keep SDK identity/address
resolution separate from delivery; the host/pocket resolves an eligible recipient
before invoking an adapter. Specify missing/duplicate channel errors, canceled
contexts, error causes and conservative posture forwarding before adding a
dispatcher. Start with a single selected delivery per call; fan-out, fallback and
retry are separate caller decisions.

For comparison, Symfony separates notification channels from provider transports
and uses its Mailer for the email channel. That supports this distinction, not
copying its entire orchestration framework. [Symfony notification documentation](https://symfony.com/doc/current/notifier.html).

## Confirmed correctness findings

These are review findings, not completed fixes. Source anchors refer to the
review baseline and will move during implementation.

1. **P1: concurrent SendGrid calls share mutable request state.**
   integrations/email/sendgrid/sendgrid.go:50,109 retains one vendor Client;
   pinned sendgrid-go base_interface.go:65,84 writes Body and copies that shared
   Request. A fake-transport race probe observed 333 mismatched complete request
   bodies across 2,048 sends, plus race reports. This demonstrates duplicate or
   missing intended messages, not independently cross-recipient secret disclosure.
   Build request/client state per call; do not serialize network sends with a lock.
2. **P1: SMTP ignores context cancellation.** email/smtp.go:53–61 never uses ctx.
   A pre-canceled call delivered successfully. A peer stalled before its greeting
   kept Send blocked after cancellation until the peer closed, then returned EOF
   without context.Canceled. This defeats auth's worker provider deadline. Use
   context-aware dialing, connection cancellation/deadlines and preserved causes.
3. **P1: SMTP permits subject header injection.** email/email.go:83–96 does not
   reject line breaks and smtp.go:132 interpolates Subject. An owned SMTP sink
   parsed an injected X-Audit-Injected header. There is a reachable CMS path:
   public contact Name → domain/messaging/inquiry.go:26–31 (trim/nonempty only) →
   internal/logic/messagingsvc/service.go:57 Subject. Reject CR/LF in header inputs
   and validate each documented bare mailbox; To containing empty/blank members
   currently passes Validate. Preserve SDK invalid-input error matching.
4. **P2: plain text is rendered with html/template.** email/templates.go:167 and
   the layout parser use the HTML engine for .txt too. The public API probe
   rendered https://example.test/reset?a=1&b=2 as ...a=1&amp;b=2, and branding
   Original & Co as Original &amp; Co in plain text. Use text/template for text
   content/layouts and keep html/template for HTML. This follows the engines'
   documented escaping distinction. [Go html/template](https://pkg.go.dev/html/template).
5. **P2: explicit text-template failures become successful fallback.**
   templates.go:304 catches every render error, including a present .txt referring
   to a missing struct field, and silently strips HTML. The probe returned nil
   error and replacement text. Distinguish absence from execution failure; never
   hide a broken registered template. The HTML-only fallback also discards hrefs
   (the probe's password-reset message becomes only “Reset your password”) and
   leaves HTML entities. Prefer requiring a deliberate text alternative for
   templated delivery over writing a general HTML-to-text converter in the SDK;
   specify this breaking contract before implementation.
6. **P2: email-notifier routing loses rich content and metadata.**
   authentication/internal/logic/delivery/router.go:499–526 keeps env.HTML only on
   direct Mailer sends; Notify receives Subject/Body. The bridge creates Text-only
   mail. It also lacks CapabilityReporter, so production rejects an otherwise
   production-capable Sender through the bridge. Its Notify ignores to.Kind; the
   fake sender accepted an email-shaped value labeled phone. Resolve the routing
   overlap above; adapters must forward declared posture conservatively and
   validate destinations. Missing metadata must not turn into trusted metadata.
7. **P2: SMTP encoding is incomplete.** smtp.go:80–85,95–112,132 emits raw UTF-8
   subjects/bodies and arbitrary-length lines without Content-Transfer-Encoding.
   A sink advertising no extensions received a 1,206-byte line and raw Unicode.
   Use appropriate stdlib MIME header/body encoding and line handling. Requirements
   are supported by [RFC 2045 §§2.7,6](https://www.rfc-editor.org/rfc/rfc2045) and
   [RFC 5322 §2.1.1](https://www.rfc-editor.org/rfc/rfc5322).
8. **P2: HTTPS metadata does not survive redirects.** SendGrid's default HTTP
   behavior follows a same-host HTTPS→HTTP 307 with bearer and message body. The
   fake-transport probe reproduced this for an instance accepted by CheckSender
   in production. Restrict redirects using adapter-owned HTTP behavior; do not
   infer a complete TLS guarantee from the initial host string.
9. **P2: raw provider responses become returned error strings.** SendGrid
   statusError at :122–134 includes the complete response body; the fake probe
   returned a synthetic sensitive marker unchanged. CMS's contact handler returns
   invalid-input error text to its submitter, so provider-400 diagnostics can
   cross the public boundary. Preserve inspectable status and root error causes
   with safe summaries; keep private diagnostics explicit. Auth's queued engine
   currently replaces failures with fixed reasons, so that path does not
   demonstrate the same exposure. Full CMS error/workflow review remains later.

## Smaller correctness and simplification items

- Public SendRequest.Subject does not populate the layout's Subject. The probe
  sends the correct header but an empty title/layout subject when Data is a struct
  without a second Subject field. Data is documented as any but layout.Data is
  populated only for map[string]any. Give rendering one explicit subject/data
  input; preserve arbitrary data consistently without mutating caller maps.
- Branding retains the caller's pointer and SocialLinks slice. Mutating those
  after construction changes future renders; the probe confirms aliasing.
  Snapshot owned configuration, including the slice, for predictable concurrent
  use. No branding race detector proof was run; this is an ownership hazard.
- TemplateLayer(99) registers successfully but is never considered by resolution;
  confirmed via the public constructor/render probe. Reject invalid layers early.
- Recursive template loading reduces paths to basenames, permitting silent
  same-layer collisions. Registration mutates content incrementally before a
  later parse error. These are source findings; reject ambiguous registrations
  and keep construction atomic without adding a runtime registry framework.
- Unknown layout fallback is documented but hides misspellings; Hub explicitly
  calls it a footgun. Recommend default layout only when omitted and an error for
  an explicitly unknown layout. Keep deliberate App > Core > Infra overrides;
  they have real callers. HTML-only layer overrides currently choose the entire
  pair, so any per-format fallback change needs explicit tests and migration text.
- TemplateRegistry is exported with an unexported constructor/private state;
  ResolveLayout returns an unexported layoutPair. Outside tests/Emailer, bounded
  framework search found no use of these APIs or the email.Renderer interface.
  Make registry internals private. Separate render-only construction from sending;
  the current Renderer interface bundles both, and auth only needs Render. Keep
  Emailer's useful render-and-send behavior, not an unused interface over it.
- RenderOption/RenderConfig/DefaultRenderConfig/ApplyOptions exist for one Layout
  field. Prefer an explicit render request/config and private defaulting when
  revising the rendering API. Keep constructor options that serve real template
  and branding composition if that remains the clearest configuration.
- Console behavior is deliberately development-only. Email's nil logger discards;
  notify's nil logger uses slog.Default. Clarify one convention and test context/
  validation; do not label intentional developer logging a production leak.
- SMTP's opportunistic STARTTLS is explicitly documented and accepted by current
  production policy. This is a policy choice, not a newly discovered violation.
  Any revised TLS configuration must preserve an explicit host-selected private
  relay/plaintext option. Do not silently replace host policy during this audit.
- Email/notify duplicate posture types and checks. Consolidate genuinely shared
  behavior inside the proposed subsystem only after preserving undeclared and
  development-only behavior. Avoid adding a global transport framework merely
  to save these few definitions.

## Consumer evidence and preserved behavior

Read-only lead-backend-engineer review; no consumer pulls/edits or real delivery.

| Sample | Snapshot and relevant adoption |
| --- | --- |
| Segovia v2 | main / 76b3d78; SDK v0.8.0, auth v0.10.0, SendGrid v0.2.0; two unrelated untracked parent plans. Auth and timeline notices use typed email. cmd/server/jobs.go:96–129 rechecks authorization and resolves an address before each send; :78–85 explicitly accepts at-least-once batch retry. |
| Coordination Hub | Clean main / 84ff08a; SDK v0.7.0, auth v0.9.0, SendGrid v0.2.0. integrations/mailer/mailer.go shares transport across auth/product. internal/outbound/domains/notification/render.go:234–245 registers handwritten HTML/text, layouts and branding; :337,560 RenderAndSend. runner.go owns immediate/digest policy, eligibility and outcomes; :631–670 branches on errors.Is SDK causes. |
| GPS360 | Clean main / e1ab3f0; SDK v0.7.1, auth v0.9.0, SendGrid v0.2.0. integrations/emailer builds raw/templated senders, but cmd/server/main.go:129–141 currently leaves them unused. Auth explicitly uses DeliveryOff/Console. Lifecycle notifications are persisted in-app banners, not outbound delivery. |
| Original framework | docs/fix-cli-and-framework-reference-drift / 0f763a9; untracked NEXT.md. infrastructure/communications/emailer/emailer.go claims a notifier implementation, but :149 is an empty implementation heading and there is no corresponding notify package/method. Useful precedent: typed mail transport plus optional rendering; not a working generic-notifier model. |

No current production SDK notify or framework mailer-bridge imports were found
in these sampled apps. Preserve the intended future channel seam; this bounded
absence does not justify deleting it. Host domain interfaces named Notifier mean
transactional enqueue/intent in Hub and Segovia; do not replace them with an
immediate SDK send or move their preferences/digests into the SDK.

## Verification and limits

All Go commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.

- From sdk: go build ./capabilities/email ./capabilities/notify;
  go test -race -count=1 ./capabilities/email ./capabilities/notify;
  go vet ./capabilities/email ./capabilities/notify — PASS.
- From each integrations/email/sendgrid and integrations/notify/mailer:
  go build ./...; go test -race -count=1 ./...; go vet ./... — PASS.
  SendGrid's first test invocation failed to bind httptest listeners in the
  sandbox; the same suite passed with approved local-listener access.
- From repository root: go run
  /tmp/gopernicus-email-notify-template-probe/main.go — successful reproduction
  of text escaping, swallowed errors, missing href, subject mismatch, branding
  aliasing and invalid-layer behavior. Embedded inputs are synthetic; recorder
  never sends. Sources live beside that temporary main.go.
- go test -race -v
  /tmp/gopernicus-email-notify-probes-ipz4jhr1/transport_test.go
  -count=1 -timeout=20s — PASS means four defect probes reproduced observed
  behavior. Approved owned loopback only, after initial sandbox bind failures.
  Log: /tmp/gopernicus-email-notify-transport-probes-loopback.log.
- go test -race -v
  /tmp/gopernicus-email-notify-probes-ipz4jhr1/sendgrid_test.go
  -run '^TestSendGridConcurrentSender$' -count=1 -timeout=20s — FAIL as expected
  for the demonstrated defect (race plus 333/2048 wrong request bodies).
  Log: /tmp/gopernicus-email-notify-sendgrid-race.log.
- Same temporary SendGrid source, -run
  '^TestSendGrid(HTTPSRedirectToPlaintext|ProviderErrorIncludesBody)$'
  -count=1 -timeout=20s — PASS reproducing both behaviors, fake RoundTripper only.
  Log: /tmp/gopernicus-email-notify-sendgrid-policy.log.

Existing focused suites are green; the added concurrent-send proof is an unresolved
product failure. No source fixes occurred. No full-workspace build/test/vet, docs
build, real provider delivery, SMTP TLS/authentication, browser flows or consumer
upgrade was run for this review. All owned listeners closed; no persistent service
was started. Formatter is unnecessary for the two Markdown-only repository edits.
Temporary probes are diagnostic artifacts and must become appropriate regression
tests as fixes are implemented; their current “defect reproduced” assertions are
not desired-behavior tests to copy unchanged.

## Implementation sequence and handoff

1. Make a bounded implementation plan from these findings and the owner's next
   direction. Fix SendGrid request ownership, SMTP cancellation/injection/MIME,
   message validation and template engine/failure handling first.
2. Adopt one package layout, define the common-text/rich-email boundary, remove
   the redundant auth email-notifier path, move the adapter into the SDK and
   simplify render-only configuration/registry internals. Keep host defaults,
   typed email, overrides and durable delivery policy intact.
3. Cover the concrete regressions with concurrent fake sends, controlled SMTP
   protocol/cancellation, safe errors/redirects, exact text/HTML output and auth
   envelope delivery. Add one small explicit channel-routing example if a
   dispatcher is introduced. Do not implement SMS/DM providers without a real use.
4. Update canonical architecture/docs/guards and append AUDIT-014 only for
   implemented breaking changes. Coordinate workspace imports/module removal;
   leave consumer upgrades and module publication to their later work.
5. Run affected Go checks plus full workspace/guards/docs when implementation
   lands. Then continue S9 oauth/tracing and S10 pocket wiring.

Review-owned repository paths: plans/framework-audit-email-notify.md and
plans/framework-audit.md only. Preserve the 661 prior dirty entries and previous
filestorage work. The SHA256 baseline is task-relative; whole HEAD diffs include
earlier audits. AUDIT.md stays through AUDIT-013 unchanged. No version/pin changes,
generated artifacts, source files or consumer files belong to this review.
