# identity-resolution — sdk/identity Resolver + sdk/notify + kind-aware invitations

Status: **RATIFIED 2026-07-10 (jrazmi, in-session) — REWRITTEN same day by
owner direction ("let's rewrite it"): NOTIFIER-FIRST. The original
direction stands verbatim ("1) I like the idea of a generic identity
resolver… lets build that. 2) let's drop that constraint that it is email
only."), now joined by ruling 6 below. The 2026-07-10 gate fold's
token-bearer semantics are SUPERSEDED (see the marked fold section); its
mechanical findings STAND and are inherited by P3. The amended P2 (sdk/
notify) is gated pre-execution. P1 executed before the rewrite and stands
untouched. **CLOSED 2026-07-10** — P1–P4 executed; both dialect live legs green (0013 applied live); the close drive recorded (phone token delivered, accepted by token, unwired kind 400, email unchanged).**
Executor model policy (standing): implementation `model: opus`;
design/doc `model: fable`. Modules: no count change (35 stands — provider
integrations are demand-gated later modules).

## Owner rulings (2026-07-10, in-session)

1. **Framework-first demand:** Segovia is A source, not THE source — the
   owner's ratified foresight counts as demand.
2. **The Resolver is generic, WITHOUT the User struct** — the profile
   record stays feature-owned.
3. **The invitation identifier's email-only pin is DROPPED.**
4. ~~Authorization notifiers: a LATER plan~~ **superseded in part by
   ruling 6** — the delivery PORT is now; provider integrations
   (twilio/slack/…) and authorization's grant-notification consumers stay
   demand-gated later work.
5. **Tenancy:** later; not touched.
6. **NEW (the rewrite ruling): greenfield delivery — build as if
   providers exist.** "You should ONLY be able to use the identity
   methods that a given application is setup to support — you should not
   be able to use SMS identity resolution / invitations if you don't have
   an sms provider hooked up. But at this point we should build it out
   like we have an sms provider." Cash-out: an sdk `notify` port whose
   wired set DEFINES the host's supported kinds (deny-by-absence per
   kind, the Providers/Granter/authorization-kinds pattern); invitation
   tokens are DELIVERED for every kind exactly as email is today — no
   plaintext hand-back to anyone, ever.

## Phases

| Phase | What | Size | Model | State |
|---|---|---|---|---|
| P1 | sdk/identity: `Address`, `Info`, `Resolver`, `ResolveAll` | S | opus | **CLOSED `feb68fb`** |
| P2 | sdk/notify: the delivery port + stdlib defaults | S–M | opus | gated, then run |
| P3 | authentication: Resolver impl + kind-aware invitations DELIVERED via notifiers | M–L | opus | after P2 |
| P4 | docs + records + close | S | fable | last |

### P2 — sdk/notify (the delivery vocabulary)

- **files:** sdk/notify/ (new package in the sdk module) + tests
- The port, shaped on the `Providers []oauth.Provider` precedent:

  ```go
  type Message struct {
      Subject string
      Body    string
  }

  // Notifier delivers a message to one address of the kind it declares.
  // Kind() is the deny-by-absence hook: a host wires one Notifier per
  // address kind it supports; an unwired kind is structurally OFF.
  type Notifier interface {
      Kind() string // identity.KindEmail, identity.KindPhone, "slack", …
      Notify(ctx context.Context, to identity.Address, msg Message) error
  }
  ```

  Plus a small `Set`/lookup helper (find-by-kind over `[]Notifier`,
  duplicate-kind = loud construction error — mirror how auth validates
  Providers) if the consumer wiring wants it; keep it minimal.
- **Two stdlib implementations ship with the port** (the facility
  honesty rule — a port with no implementation is a scaffold, this one
  is real on day one): `Console` (any kind — constructor takes the kind;
  logs the delivery; the dev default, mirroring `email.Console`) and
  `MailerBridge` (Kind = email; wraps the existing `sdk/email` Mailer so
  the email kind can route through the same seam WITHOUT touching auth's
  current mail path — bridging, not replacing; `sdk/email` stays).
- Doc pins: fail loudly (a Notifier never silently drops); Message is
  deliberately minimal v1 (subject/body — templates/rich content are the
  consumer's job pre-render; a richer payload is future vocabulary, not
  now); intra-sdk import of `sdk/identity` for Address (G1-legal:
  self-module imports).
- Naming: package `notify`, interface `Notifier` — never an
  abbreviation.
- Unit tests: Console output shape, MailerBridge delegation, Set
  duplicate-kind rejection, kind lookup miss = loud error class.

### P3 — authentication: implement + go kind-aware, delivered

Inherits the STANDING mechanical folds (items 3–12 of the marked fold
section below) — migration 0013 with the pending-index rebuild in both
dialects, service-owned kind-aware normalization, unified kind
vocabulary (`identity.KindEmail`'s value everywhere), email-keyed
auto-paths filtered to kind=email, authmem in scope, in-package fakes
for tests, `mustNewInvitation`/reference-scan updates.

- **(a) `auth.Service` satisfies `identity.Resolver`** — unchanged from
  the original cut: user → display_name else email local part;
  service_account → its name; Addresses carries the user's email as
  KindEmail; unknown type / missing row / machine-subsystem-off (nil
  repo) → the errs not-found class, nil-guarded.
- **(b) kind-aware invitations, DELIVERED (supersedes the token-bearer
  fold):** `auth.Config` gains `Notifiers []notify.Notifier`
  (validated at NewService: duplicate kinds = loud error, the Providers
  precedent). Create-invitation of kind X requires a wired X-notifier —
  missing ⇒ loud `ErrKindNotSupported` (deny-by-absence; ruling 6), the
  invitation is NOT created. Delivery: the token rides
  `Notifier.Notify(ctx, Address{Kind, Identifier}, msg)` for every
  non-email kind; the EMAIL kind keeps today's Mailer path byte-for-byte
  when no email-kind notifier is wired, and routes through a wired
  email-kind notifier when one is (documented precedence; existing
  hosts unchanged with zero Config edits). `CreateResult` carries NO
  plaintext token for any kind — delivery is the only channel, exactly
  like email today.
- **(c) accept, uniform trust model:** email kind keeps the
  email-match binding (account emails exist). Non-email kinds accept on
  authenticated session + valid token — the binding is
  ADDRESS-POSSESSION via delivery (the code went to the invited
  address), i.e. the same trust email has, minus the account-match that
  cannot exist until address verification lands (named future work, P4
  ledger). Kind-aware `Accept`: the identifier-match check applies only
  where the acceptor's record carries that address kind (today: email).
  README documents the model plainly.
- **(d)** A9/Z4 email-invitation legs must pass verbatim (zero behavior
  change for email hosts); new storetest cases per the standing folds +
  a delivery-seam case (create non-email kind with no notifier → loud
  error; with a fake notifier → the fake received the token's message).

### P4 — docs + records + close

sdk/README: notify row + identity row (+ the no-User-struct pin);
auth README (Resolver, kind semantics, the Notifiers Config row +
deny-by-absence table entry, the trust-model note); features/README §5
corollary framing + ARCHITECTURE identity parenthetical (standing fold
item 11) + notify's place in the facility list; RELEASING upgrade note
(migration 0013); NOTES milestone entry; deferred ledger: provider
integrations (`integrations/notify/<tech>` — trigger: the first host
wiring a real SMS/Slack provider; Segovia is the likely trigger),
address verification (enables non-email account-match binding),
authorization grant-notifications (consumes this port), tenancy.
Archive; memory.

## Acceptance

```sh
make check    # 35 modules, eleven guards, scaffold legs
make guard
```

Plus: sdk (identity+notify) hermetic green; auth hermetic suites green
incl. the new storetest cases; both dialect live conformance for
authentication at close (migration 0013 is a schema change); the A9
email leg unchanged live; RELEASING note present.

## Real-interaction check

Standing check (a) per phase commit. At close, on `examples/auth-cms`:
(1) one email invitation end to end — unchanged behavior, exact codes;
(2) wire a `notify.Console` notifier for KindPhone in the host, drive a
phone-kind invitation: created → the console notifier LOGS the delivery
(the token visibly delivered to the address, not handed back) → accept
by session+token → grant verified live; (3) unwire the phone notifier,
repeat create → the loud `ErrKindNotSupported` (deny-by-absence driven
live). Exact codes recorded.

## Review-gate fold (2026-07-10) — items 1–2 SUPERSEDED; items 3–12 STAND

**[SUPERSESSION MARKER 2026-07-10, owner-directed rewrite ("let's
rewrite it" — the notifier-first discussion is the record): fold items
1–2 (token-bearer acceptance; the once-only plaintext hand-back to the
creator) are DEAD — replaced by P2/P3's delivered-via-notifier design
under ruling 6. They existed only because delivery was out of scope;
it no longer is. Items 3–12 are mechanical schema/normalization/scope
truths independent of delivery and are INHERITED BY P3 as written.]**

**lead-backend-engineer: SHIP-WITH-EDITS (6). architecture-steward:
ALIGNED-WITH-EDITS (7).** The interlocking MAJORs (lead 1 + steward
1+2): accept is EMAIL-MATCH (pre-verified: the handler hard-wires the
acceptor's email via `EmailForUser`; `invitationsvc.Accept` compares
normalized identifiers), AND the token is hashed at rest with
`CreateResult` carrying no plaintext — so the original draft could
neither deliver nor redeem a non-email invitation.

~~1. Non-email create hands the plaintext token back once…~~ (DEAD)
~~2. Accept skips the identifier match on valid token alone…~~ (DEAD —
P3(c)'s kind-aware accept is the successor: same skip, but the binding
is restored by DELIVERY to the invited address.)

3. **Email-keyed auto-paths filter to kind=email**: `ResolveInvitations`,
   `Mine`, `userLookup`/`resolvePendingInvitations` never match
   non-email rows; `AutoAccept` never direct-adds for non-email kinds
   (README states it).
4. Migration **0013**, BOTH dialects: `ADD COLUMN identifier_kind TEXT
   NOT NULL DEFAULT 'email'` PLUS drop/recreate the pending-tuple
   partial unique index over `(resource_type, resource_id,
   identifier_kind, identifier, relation) WHERE status='pending'`
   (mandatory — the cross-kind-coexistence case forces it; safe: all
   backfilled rows are kind=email). Update the storetest reference
   collision scan + both stores' create/columns/scan +
   `mustNewInvitation`.
5. Normalization stays IN THE SERVICE (`normalizeIdentifier` grows a
   kind parameter; the entity keeps store-verbatim): email →
   trim+lowercase; every other kind → trim only.
6. Kind vocabulary unified: `IdentifierKind` values ARE the sdk/identity
   Address-kind vocabulary; the entity default and migration literal are
   `identity.KindEmail`'s value.
7. `Resolve` on a machine-subsystem-off host (nil ServiceAccounts) →
   errs not-found class, nil-guarded.
8. `ResolveAll` strict: `([]Info, error)`, first error aborts; lenient
   batch doc-named as the future upgrade seam. **(landed at P1)**
9. P1 rewrites sdk/identity's stale doc pins in the same commit.
   **(landed at P1)**
10. P2→P3's file list includes `examples/auth-cms/internal/authmem`;
    feature tests use in-package fakes, never authmem.
11. P4 sweeps features/README §5's corollary framing + ARCHITECTURE's
    identity parenthetical.
12. The auth phase is **M–L**; the non-email drive uses
    `ListByResource`, never `Mine`.

## Delta-gate fold (2026-07-10, on the rewrite) — GOVERNS P2/P3 where it conflicts

**lead-backend-engineer: SHIP-WITH-EDITS (6 findings + 3 author
questions, all resolved here):**

1. **The supported-kind predicate, verbatim (lead 1 — the drafted rule
   would have DENIED email invitations on every existing host):** a kind
   is supported iff `kind == identity.KindEmail && Config.Mailer != nil`
   OR a notifier of that kind is wired. `ErrKindNotSupported` fires for
   NON-EMAIL kinds only; email is always-on via the already-required
   Mailer — existing hosts create email invitations with zero Config
   edits, and the A9 leg passes verbatim by construction.
2. **MailerBridge carries From (lead 2):**
   `NewMailerBridge(sender email.Sender, from string)` —
   `email.Message.Validate` requires From and neither `notify.Message`
   nor `identity.Address` carries one. Maps `to.Value → To[0]`,
   `msg.Body → Text`, `from → From`. (And the plan's "Mailer" is
   precisely `email.Sender` — auth's `Config.Mailer` field type.)
3. **Duplicate-kind rejection is a NEW check (lead 3):** auth's provider
   map silently last-wins (`authsvc/service.go:230`) — do NOT copy it;
   follow the loud-construction posture (`ErrOAuthReposRequired` class).
   Duplicate kind in `Config.Notifiers` → loud NewService error.
4. **The fork covers BOTH send paths (lead 4):** `sendInviteSent` AND
   `sendMemberAdded` (Accept sends member-added to the IDENTIFIER —
   unpatched, a phone number would route through the email Mailer).
   Build the body once, dispatch by `inv.IdentifierKind`: email-with-
   no-notifier → `Config.Mailer`; otherwise the wired notifier. The
   fork lives in `invitationsvc` only — `Deps` grows `Notifiers`;
   `authsvc.Deps` does NOT (verification/reset mail untouched).
5. **Test homes split (lead 5):** the identifier_kind column +
   collision-scan cases → storetest (Storer suite); the delivery-seam
   cases (no-notifier loud error / fake-notifier receives the token
   message) → `invitationsvc` service tests with in-package fakes; the
   duplicate-kind NewService rejection → package-auth tests; Console/
   MailerBridge/lookup → sdk/notify tests.
6. **Accept's edit is SERVICE-ONLY (lead 6):** the kind-conditional
   identifier-match lives in `invitationsvc.Accept` (compare only when
   `inv.IdentifierKind == identity.KindEmail`); the inbound handler
   keeps passing the acceptor's email (doc-comment only — do not invent
   an acceptor-address-of-kind resolution path; no data source exists
   until address verification).

**Author-question rulings (coordinator, within the ratified direction):**

7. **Email-notifier scope = INVITATIONS-ONLY this milestone.** A wired
   email-kind notifier routes invitation mail; verification/reset mail
   stays on `authsvc`'s `Config.Mailer` directly. The asymmetry is
   documented in the auth README; unifying ALL outbound onto notify is
   a deferred-ledger item (it would move authsvc's mail seam — real
   scope, not this milestone's).
8. **`ErrKindNotSupported` wraps `errs.ErrInvalidInput`** (→ 400 at the
   transport) — a new package-authentication sentinel; the live-drive
   records that exact code.
9. **NO `notify.Set` in sdk v1** (one consumer today — the five-point
   test says wait): auth holds its own kind-lookup map internally,
   validated loud at NewService. sdk/notify ships the port + Console +
   MailerBridge only; a Set helper graduates with the second consumer.

**P3 resized L** (lead sizing: migration ×2 dialects + stores + the
two-seam fork + kind-aware accept + Resolver + split test homes + live
conformance). The real-interaction check's step 3 also asserts email
invitations still succeed with the phone notifier unwired (regression
guard on fold item 1).

## Execution log

(append dated entries here)

### 2026-07-10 — P1 CLOSED (sdk/identity vocabulary + Resolver port)

`Address{Kind,Value}` + `KindEmail`/`KindPhone` (open-string kinds),
`Info{Principal, DisplayName, Addresses}` (projection-never-record pin),
`Resolver` (one method, fail-closed on the `errs.ErrNotFound` sentinel —
cited in prose/tests; identity.go still imports `context` only, G1
clean), strict positional `ResolveAll` (first-error abort; lenient batch
doc-named as the future upgrade seam), and the fold-9 header rewrite
(the "vocabulary only / nothing to implement" pins were false the moment
the port landed — rewritten in the same commit; no default Resolver,
deliberately: identity data is feature-owned, the sdk/oauth precedent).
Tests: positional alignment, strict abort (`errors.Is` on ErrNotFound),
empty/nil slice, compile-time interface assertion. `make check` (35) +
`make guard` (11, G1 clean) green. Committed CI-green.

### 2026-07-10 — P2 CLOSED (sdk/notify)

`Message{Subject,Body}` + `Notifier{Kind,Notify}` (deny-by-absence +
no-silent-drop pins), `Console` (any kind; nil logger → slog.Default —
a DELIBERATE divergence from email.Console's io.Discard fallback,
logged: a discarding notifier would violate the port's own pin),
`MailerBridge` (From at construction per delta-fold 2; Validate+Send
propagate; email.Sender signature matched the fold exactly). NO Set
helper (delta-fold 9). Source-level interface assertions (the sibling
convention). Tests: log shape, field mapping + From injection, both
error propagations, default-logger fallback. sdk green; `make check`
(35) + `make guard` (11, G1 clean) green. Committed CI-green.
**Next: P3 (L).**

### 2026-07-10 — P3 CLOSED (auth: Resolver + kind-aware delivered invitations)

All delta-fold specs landed: `authsvc/resolver.go` (user → DisplayName
else email local part + KindEmail address; service_account → Name;
unknown/missing/nil-machine-repo → ErrNotFound, nil-guarded) promoted on
`auth.Service` with the `identity.Resolver` assertion; the supported-kind
predicate verbatim (email always-on via required Mailer; non-email needs
a wired notifier → `ErrKindNotSupported` wrapping ErrInvalidInput —
defined in invitationsvc and ALIASED at the root per the
ErrOAuthLastMethod precedent, a cycle the plan's "package-authentication
sentinel" wording missed, logged); `deliver` forks BOTH send paths
(sendInviteSent + sendMemberAdded) by kind — email-no-notifier rides
Mailer byte-for-byte; Accept's identifier-match is kind-conditional,
service-only; Mine/ResolveInvitations/userLookup filter kind=email;
`Config.Notifiers` + loud `ErrDuplicateNotifierKind` (a NEW check — the
silently-last-wins provider map explicitly not copied); inbound create
accepts optional identifier_kind. Migration **0013** both dialects
(column + pending-index rebuild); both stores' row structs/creates/scans
kind-aware; storetest KindRoundTripVerbatim + CrossKindCoexistence +
default-email asserts; reference scan + authmem pending-tuple gain the
kind. Test homes per delta-fold 5. **Live A9 email leg driven on an
UNCHANGED host — all six codes match the record exactly** (mailer log
shows both sends riding Config.Mailer). Premise-drift logged
(CreateInput lives in invitationsvc; line-number drift; mustNewInvitation
kept its signature — ~25 call sites). `make check` (35) + `make guard`
(11) green; coordinator re-verified builds. Committed CI-green.
**Next: P4 (close).** Known P4 items from the executor: invitation JSON
response doesn't yet surface identifier_kind (scoped: request-only);
migration 0013's on-DB apply is the close's live-leg job.

### 2026-07-10 — REWRITE (owner-directed): notifier-first

The token-bearer P2 semantics never executed — the owner redirected at
the flag ("let's rewrite it") after the trust-model walkthrough. Plan
rewritten in place per the ratification contract (the discussion IS the
record): NEW P2 = sdk/notify (port + Console + MailerBridge; ruling 6);
P3 = the auth phase reworked to delivered-via-notifier with
deny-by-absence kinds and NO plaintext hand-back; fold items 1–2
superseded, 3–12 inherited. P1 unaffected (Address was built for
exactly this). The amended P2 design goes through the review gate
before execution.

### 2026-07-10 — P4 EXECUTED — **MILESTONE CLOSED**

Coordinator-inline (fable). Live legs: pgx conformance 4.8s (C-locale
docker :55434 — :55432 held by an unrelated gps360 container, avoided) +
turso 460.9s (playground, URL asserted) — migration 0013 applied live on
both. Doc sweep: sdk/README identity row rewritten + notify row;
ARCHITECTURE facility examples + identity parenthetical; features/README
§5 corollary growth note; RELEASING 0013 upgrade note; auth README
(Notifiers Config row, the Invitation-identifier-kinds section incl. the
trust model + the invitations-only asymmetry + the response-gap note,
Resolver wiring note); auth-cms README kinds-demo section. **Close drive
(recorded verbatim in the session):** phone invite 201 → the console
notifier line shows kind=phone, the address, and the token DELIVERED →
B accepts by token 200 → members-only 200 (the member-added notice rode
the same fork — 2 notifier lines) → slack kind (unwired) 400 → email
invite 201 with mailer-only lines. `make check` (35) + `make guard` (11)
green at close. Archived. Open flags to NOTES: invitation ownership
(keep — the executor's endorsed read), securityevent's home, the
response-side identifier_kind gap.
