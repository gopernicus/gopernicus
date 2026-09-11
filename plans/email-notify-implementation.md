# Email and notification implementation

Status: COMPLETE — 2026-09-10. Parent: [framework-audit.md](framework-audit.md).
Implements [S9b findings](framework-audit-email-notify.md) with the owner's approved
notification subsystem and subsequent per-call delivery selection requirement.

## Owner direction and constraints

Every send explicitly selects its deliveries. An outage can send email AND Slack;
a password reset selects only email. Preserve each channel's typed content. This
supersedes S9b's proposed common-text-only dispatcher and single-delivery limit.

SDK remains stdlib-only. Host owns provider/From configuration, recipient
eligibility, channel selection, queues and retries. Keep generic worker/work seams.
Do not create real Slack/SMS integrations, send external messages, change consumers,
publish modules or alter queued envelope schemas. Preserve prior audit work.

Baseline: firestore-authentication / 6807ed06; 662 existing dirty entries and 1990
visible files recorded in /tmp/gopernicus-email-notify-implementation-baseline.json.
Use task-relative file hashes, not the full HEAD diff. Go 1.26.1, 42 initial modules,
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter /Users/jrazmi/go/bin/goimports.
No task services running at start. Generated sources must not be manually edited.

## Design

- notify owns a prepared Delivery with Send(context.Context) error, a DeliveryFunc
  adapter for host implementations, and Send(ctx, deliveries...). Caller passes
  exactly the deliveries to run. No global registration, address-kind routing,
  universal rich-message struct, default fan-out or hidden background goroutines.
- Attempt selected deliveries sequentially in argument order. Ordinary failure
  does not suppress later deliveries; canceled context stops starting more work.
  Return inspectable indexed failures with wrapped causes so callers can retry
  selected failed deliveries rather than resend successful ones. Empty/nil input
  fails explicitly. Provider errors do not prove a message was never accepted.
- notify/email keeps typed Message/Sender and optional rendering. NewDelivery
  binds a sender and a snapshot of its message without sending; rich HTML/text
  reaches the same Sender.Send implementation as a direct email send.
- Host-provided Slack or SMS calls fit DeliveryFunc without adopting email fields.
  Keep notification dispatch and provider transport distinct. All provider defaults
  remain chosen by the host.
- Authentication's existing queued envelope retains its representation. Its
  router chooses one prepared delivery from purpose/kind and invokes notify.Send;
  common fan-out remains available to host workflows. Remove competing email
  override wiring; preserve a narrow, explicit non-email delivery extension.
- Remove integrations/notify/mailer and move email into notify/email. Consolidate
  shared production posture conservatively; do not turn unknown metadata into a
  production declaration. SMTP opportunistic TLS remains an explicit documented
  host choice, not silently changed to mandatory TLS.
- Rendering uses html/template for HTML and text/template for text. Require
  deliberate text content; surface errors and invalid explicit layouts/layers.
  Keep App > Core > Infra overrides, immutable branding snapshots, and explicit
  render subject/data. Make registry internals private and separate rendering from
  sender construction. Keep useful optional render-and-send behavior.

The named backend review confirmed the implemented result signature:
Send returns *SendError{Failures []Failure}; each Failure has Index, Attempted and
Err. Unwrap returns all causes. A final acknowledged success is not replaced with
a late context error; provider-local cancellation does not cancel other deliveries.

Authentication now declares BodySender{Send(ctx, destination, body string) error}
in its delivery implementation with a public authentication.BodySender alias.
Config.BodySenders is a copied map keyed by non-email identifier kind. Empty,
whitespace, email, nil and typed-nil entries fail at construction. No persisted
kind/purpose/envelope schema changed. Generic notify has no address-kind map.

Renderer is a concrete render-only type; NewRenderer keeps useful template/branding
constructor options accepting fs.FS. RenderRequest carries Template, Subject, Data
and Layout explicitly. Emailer.New/RenderAndSend remain optional; WithLogger and
single-field render functional options are removed. Registry mutation is private
and construction-only. Handwritten plain-text counterparts were added to all ten
authentication core templates.

## Implementation sequence

1. Establish notify delivery/failure contracts and migrate email namespace; remove
   bridge module and update workspace imports. Add host-selected email+custom-chat
   examples proving rich email, email-only reset and visible partial failures.
2. Fix message/header validation; SMTP cancellation, MIME encoding and protocol
   behavior; SendGrid per-call request ownership, redirects and safe status errors.
3. Fix and simplify template configuration/rendering while preserving actual
   branding/layout/text consumers. Update authentication routing/production checks
   and narrow CMS error-boundary regression coverage as required by these findings.
4. Update architecture, SDK/provider/pocket/example docs, guards, release notes and
   standalone AUDIT-014 migration. Earlier AUDIT entries remain unchanged except
   references necessary to point consumers to final destinations, documented here.
5. goimports, focused Go build/test/vet/race and owned SMTP/fake HTTP delivery probes;
   full workspace make check (module build/test/vet + guards), docs-build, scoped
   module tidy checks. No real-provider or consumer-upgrade claim.
6. Complete named backend/platform review, resolve findings, compare final hashes
   and generated status, close owned services and update master handoff.

## Final verification and handoff

Implementation and documentation are complete. The reviewed public API is the
Design above and the standalone migration is
[AUDIT-014](../AUDIT.md#audit-014-explicit-notification-deliveries-and-email-correctness).
All task checks passed; no unresolved product/verification failure or owned
persistent service remains. No real provider delivery, consumer edits, releases,
module pins or queued schema changes. Bridge removal leaves 41 workspace modules.

Formatter: /Users/jrazmi/go/bin/goimports -w applied to the 73 task-relative Go
source/test files (excluding generated sources). All commands below use
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache where applicable. Loopback suites and
the workspace gate used approved execution because sandbox listener binds fail.

| Command / location | Result and evidence |
| --- | --- |
| Root: go test -race -count=1 ./sdk/capabilities/notify/... ./integrations/email/sendgrid/... | PASS; /tmp/gopernicus-email-notify-final-focused-race.log |
| pockets/authentication: go test -race -count=1 ./... | PASS; /tmp/gopernicus-email-notify-auth-final-race.log |
| Root: go test ./pockets/cms/internal/logic/messagingsvc/... | PASS; CMS contact subject injection rejected before SMTP dial; existing persisted inquiry retained |
| Root: make check | PASS; /tmp/gopernicus-email-notify-final-make-check.log; all 41 modules build/test/vet, tagged compile checks, generated drift checks and 23 guards |
| Root: make docs-build | PASS; pnpm typecheck + build; /tmp/gopernicus-email-notify-docs-build.log |
| SendGrid isolated staged go mod tidy | PASS; /tmp/gopernicus-email-notify-tidy.log; only obsolete rest/x/net removed from repository dependencies |

The race suite executes real SMTP protocol exchanges with owned loopback peers:
pre-cancel and header validation before dial, cancellation at greeting/DATA,
configured Timeout, accepted DATA followed by failed QUIT, decoded Unicode
headers, folded lines and multipart bodies. No SMTP TLS negotiation, auth with
real credentials or actual mailbox receipt was exercised.

Owned HTTP/fake transports verify SendGrid's 512 concurrent requests retain each
call's exact context/body/recipients, redirects do not issue another request,
response bodies close without reading, and status summaries do not leak bodies.
This verifies request construction/ownership, not real SendGrid acceptance.

SDK behavior coverage includes explicit email+chat and email-only reset selection,
partial failures and original indices, cancellation/skipped attempts, accepted
success, rich email snapshots, structural posture, branding ownership, explicit
subject/struct layout data, text URLs, priority, parse/render errors and invalid
configuration. The external-package runnable ExampleSend models a host's custom
chat sender; no Slack integration or hidden sender registry was added.
Authentication's race suite covers queued/in-process delivery and retries; the
email route test wires a phone sender and proves it does not fire. Core HTML
goldens are preserved; deliberate text templates update plaintext goldens.

Named platform review corrected Console cancellation, response-body draining and
mailbox length. Final named backend review caught callable Option mutation and
empty/misnamed template roots; both were fixed, regression-tested and confirmed
by bounded read-only follow-up. Option is opaque and applies only to private
construction state. Both engines reject empty executable roots at registration.

Earlier verification failures were resolved: old notifier fixtures/metadata error
ownership, missing .text layout root suffix, old plaintext goldens and a queued
test fixture missing Subject. An initial sandbox auth run could not bind listeners;
the approved retry passed. Direct GOPROXY=off go mod tidy -diff in SendGrid cannot
resolve the new unpublished SDK package from its historical v0.1.0 requirement.
The scoped check therefore copied that module to a temporary directory and used
GOWORK=off plus a local SDK replacement. After tidy, normalized go.mod/go.sum
matched the repository, excluding expected released-SDK checksum removal caused
by that temporary replacement. No local replace or version change was written to
the repository; existing SDK release checksums were retained for the pinned module.

Unrelated live SQL/cloud datastore legs remain skipped by make check; no browser
app, real SMTP TLS/auth, real SendGrid or consumer upgrade was run in this slice.
Docusaurus reported a non-fatal update-check config-permission notice after its
successful build; no permission changes were made. All owned test listeners closed.
No generated *_templ.go or committed dist/manifest asset changed task-relative.
AUDIT-001 through AUDIT-013 are byte-for-byte preserved against the baseline hash.

Canonical architecture/SDK/provider/authentication/workshop docs and release notes
now describe the final design. Historical review proposals remain labeled and
point here. Next: capabilities/oauth and tracing, then S10 pocket wiring and the
full pocket audits. Keep host-owned password/provider/From/eligibility/retry policy
and generic workers; actual consumer upgrades are separate work.

## Task-relative changed-file inventory

132 paths: 40 additions, 66 modifications, 26 deletions, including the physical
email package move. Compared against the implementation baseline, not HEAD or the
662 pre-existing dirty entries. Exact inventory also saved at
/tmp/gopernicus-email-notify-final-changed-files.txt. No out-of-scope path changed.

```text
M ARCHITECTURE.md
M AUDIT.md
M Makefile
M README.md
M RELEASING.md
M examples/auth-cms/cmd/server/delivery_health_test.go
M examples/auth-cms/cmd/server/inprocess_delivery_test.go
M examples/auth-cms/cmd/server/jobs_delivery_live_test.go
M examples/auth-cms/cmd/server/jobs_delivery_proof_test.go
M examples/auth-cms/cmd/server/jobs_delivery_replace_test.go
M examples/auth-cms/cmd/server/jobs_delivery_retry_test.go
M examples/auth-cms/cmd/server/jobs_delivery_test.go
M examples/auth-cms/cmd/server/main.go
M examples/auth-cms/cmd/server/override_test.go
M examples/auth-cms/cmd/server/password_reset_link_test.go
M examples/auth-cms/cmd/server/production_test.go
M examples/cms/cmd/server/main.go
M examples/minimal/cmd/server/goth_htmx_proof_test.go
M examples/minimal/cmd/server/main.go
M go.work
M integrations/email/sendgrid/README.md
M integrations/email/sendgrid/go.mod
M integrations/email/sendgrid/go.sum
M integrations/email/sendgrid/posture_test.go
A integrations/email/sendgrid/regression_test.go
M integrations/email/sendgrid/sendgrid.go
M integrations/email/sendgrid/sendgrid_test.go
D integrations/notify/mailer/go.mod
D integrations/notify/mailer/go.sum
D integrations/notify/mailer/mailer.go
D integrations/notify/mailer/mailer_test.go
A plans/email-notify-implementation.md
M plans/framework-audit-email-notify.md
M plans/framework-audit.md
M pockets/authentication/README.md
M pockets/authentication/auth_test.go
M pockets/authentication/authentication.go
M pockets/authentication/delivery_mode_test.go
M pockets/authentication/internal/inbound/authentication/helpers_test.go
M pockets/authentication/internal/logic/authsvc/delivery_test.go
M pockets/authentication/internal/logic/authsvc/identifier_management_test.go
M pockets/authentication/internal/logic/authsvc/passwordless_events_test.go
M pockets/authentication/internal/logic/authsvc/service_test.go
A pockets/authentication/internal/logic/delivery/body_sender.go
M pockets/authentication/internal/logic/delivery/deliverychar/deliverychar.go
M pockets/authentication/internal/logic/delivery/inprocess_char_test.go
M pockets/authentication/internal/logic/delivery/inprocess_retry_test.go
M pockets/authentication/internal/logic/delivery/processor_char_test.go
M pockets/authentication/internal/logic/delivery/producer_seam_test.go
M pockets/authentication/internal/logic/delivery/router.go
M pockets/authentication/internal/logic/delivery/router_data_test.go
M pockets/authentication/internal/logic/delivery/router_test.go
A pockets/authentication/internal/logic/delivery/templates/identifier_change_notice.txt
A pockets/authentication/internal/logic/delivery/templates/identifier_change_proof.txt
A pockets/authentication/internal/logic/delivery/templates/invitation.txt
A pockets/authentication/internal/logic/delivery/templates/login_code.txt
A pockets/authentication/internal/logic/delivery/templates/magic_link.txt
A pockets/authentication/internal/logic/delivery/templates/member_added.txt
A pockets/authentication/internal/logic/delivery/templates/oauth_pending_link.txt
A pockets/authentication/internal/logic/delivery/templates/password_reset.txt
A pockets/authentication/internal/logic/delivery/templates/sensitive_code.txt
A pockets/authentication/internal/logic/delivery/templates/verification.txt
M pockets/authentication/internal/logic/invitationsvc/authorized_test.go
M pockets/authentication/internal/logic/invitationsvc/delivery_test.go
M pockets/authentication/internal/logic/invitationsvc/service.go
M pockets/authentication/internal/logic/invitationsvc/service_test.go
M pockets/authentication/runtime_mode_compat_test.go
M pockets/authentication/security.go
M pockets/authentication/security_test.go
M pockets/cms/cms.go
A pockets/cms/internal/logic/messagingsvc/email_boundary_test.go
M pockets/cms/internal/logic/messagingsvc/messaging_test.go
M pockets/cms/internal/logic/messagingsvc/service.go
M sdk/README.md
D sdk/capabilities/email/capabilities.go
D sdk/capabilities/email/capabilities_test.go
D sdk/capabilities/email/console.go
D sdk/capabilities/email/console_test.go
D sdk/capabilities/email/email.go
D sdk/capabilities/email/email_test.go
D sdk/capabilities/email/emailer.go
D sdk/capabilities/email/emailer_test.go
D sdk/capabilities/email/posture.go
D sdk/capabilities/email/posture_test.go
D sdk/capabilities/email/renderer.go
D sdk/capabilities/email/smtp.go
D sdk/capabilities/email/smtp_test.go
D sdk/capabilities/email/templates.go
D sdk/capabilities/email/templates/layouts/marketing.html
D sdk/capabilities/email/templates/layouts/marketing.txt
D sdk/capabilities/email/templates/layouts/minimal.html
D sdk/capabilities/email/templates/layouts/minimal.txt
D sdk/capabilities/email/templates/layouts/transactional.html
D sdk/capabilities/email/templates/layouts/transactional.txt
D sdk/capabilities/email/templates_branding_test.go
D sdk/capabilities/email/templates_test.go
M sdk/capabilities/notify/capabilities.go
M sdk/capabilities/notify/capabilities_test.go
M sdk/capabilities/notify/console.go
M sdk/capabilities/notify/console_test.go
A sdk/capabilities/notify/email/capabilities_test.go
A sdk/capabilities/notify/email/console.go
A sdk/capabilities/notify/email/console_test.go
A sdk/capabilities/notify/email/delivery.go
A sdk/capabilities/notify/email/delivery_test.go
A sdk/capabilities/notify/email/email.go
A sdk/capabilities/notify/email/email_test.go
A sdk/capabilities/notify/email/emailer.go
A sdk/capabilities/notify/email/emailer_test.go
A sdk/capabilities/notify/email/posture_test.go
A sdk/capabilities/notify/email/renderer.go
A sdk/capabilities/notify/email/smtp.go
A sdk/capabilities/notify/email/smtp_protocol_test.go
A sdk/capabilities/notify/email/smtp_test.go
A sdk/capabilities/notify/email/template_roots_test.go
A sdk/capabilities/notify/email/templates.go
A sdk/capabilities/notify/email/templates/layouts/marketing.html
A sdk/capabilities/notify/email/templates/layouts/marketing.txt
A sdk/capabilities/notify/email/templates/layouts/minimal.html
A sdk/capabilities/notify/email/templates/layouts/minimal.txt
A sdk/capabilities/notify/email/templates/layouts/transactional.html
A sdk/capabilities/notify/email/templates/layouts/transactional.txt
A sdk/capabilities/notify/email/templates_branding_test.go
A sdk/capabilities/notify/email/templates_test.go
A sdk/capabilities/notify/example_test.go
M sdk/capabilities/notify/notify.go
A sdk/capabilities/notify/notify_test.go
M sdk/capabilities/notify/posture.go
M sdk/capabilities/notify/posture_test.go
M workshop/documentation/docs/integrations/catalog.md
M workshop/documentation/docs/sdk/capabilities.md
M workshop/documentation/docs/sdk/overview.md
```
