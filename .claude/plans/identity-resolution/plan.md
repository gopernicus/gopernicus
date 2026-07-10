# identity-resolution — sdk/identity Resolver + Address vocabulary; invitations go kind-aware

Status: **RATIFIED 2026-07-10 (jrazmi, in-session) — direction ratified
verbatim: "1) I like the idea of a generic identity resolver that can be
configured without the user struct. lets build that. 2) let's drop that
constraint that it is email only." Design cut by fable same session; the
review gate runs pre-execution (it has caught a real major on every
milestone this week — 3 for 3). EXECUTING after the fold.**
Executor model policy (standing): implementation `model: opus`;
design/doc `model: fable`. Modules: no count change (35 stands).

## Owner rulings (2026-07-10, in-session — the ratification-contract session)

1. **Framework-first demand:** Segovia is A source of demand, not THE
   source — the owner's ratified foresight counts as demand. This plan
   exists under that rule: the Resolver ships with a real implementation
   (authentication) but no feature consumer yet, deliberately.
2. **The Resolver is generic and configured WITHOUT the User struct** —
   the profile record stays feature-owned (AV5 and the A-I1 thin-Principal
   posture are unchanged; this grows the VOCABULARY, not a registry).
3. **The invitation identifier's email-only pin is DROPPED** — this
   supersedes the auth-v2-era `Identifier string // invitee email` doc
   pin, by owner ratification this date (recorded here per the
   never-edit-ratified-without-discussion rule: the discussion happened).
4. **Authorization notifiers** (injectable delivery for grants/invites
   etc.): a LATER plan — named deferred-ledger entry at P3, not scoped
   here.
5. **Tenancy:** later; not touched.

## Phases

| Phase | What | Size | Model |
|---|---|---|---|
| P1 | sdk/identity: `Address`, `Info`, `Resolver` | S | opus |
| P2 | authentication: Resolver implementation + kind-aware invitations (entity/service/API/migrations/storetest, both stores) | M | opus |
| P3 | docs + records + close | S | fable |

### P1 — sdk/identity vocabulary growth

- **files:** sdk/identity/ (existing package) + tests
- `Address{Kind, Value string}` — a contact/claim address. Kinds are open
  strings; `KindEmail`/`KindPhone` consts ship as the conventional names.
  Doc pin: an Address is how you REACH or IDENTIFY a person out-of-band —
  it is not a Principal (a Principal is a resolved actor reference).
- `Info{Principal Principal, DisplayName string, Addresses []Address}` —
  the display/contact projection of a principal. Doc pin: this is a
  PROJECTION for rendering and out-of-band contact, never the identity
  record itself; the record (credentials, verification, lifecycle) stays
  feature-owned — no User struct enters sdk, ever (owner ruling 2).
- `Resolver interface { Resolve(ctx context.Context, p Principal) (Info, error) }`
  — one method, v1 (batch is an interface-upgrade seam later, doc-named;
  a helper `ResolveAll(ctx, r, []Principal)` loop ships for convenience).
  Not-found and unresolvable-type FAIL CLOSED with the `sdk/errs`
  not-found class — a Resolver never invents an Info.
- sdk stays stdlib-only (G1 structural). Unit tests on the helper +
  doc-comment contract examples.

### P2 — authentication: implement + go kind-aware

- **files:** features/authentication (service + domain/invitation +
  domain/user as needed), features/authentication/stores/{turso,pgx}
  (additive migration + row structs), storetest, README
- **(a) `auth.Service` satisfies `identity.Resolver`:** resolves `user`
  and `service_account` principals to Info (display-name fallback rules
  pinned in the doc: user → display_name, else email local part;
  service_account → its name; Addresses carries the user's email as
  `KindEmail` when present). Unknown type or missing row → the errs
  not-found class (fail closed). Hermetic tests via authmem/memstore.
- **(b) kind-aware invitation identifiers:** entity gains
  `IdentifierKind string` (default `email`); kind-aware normalization
  (email → trim+lowercase as today; all other kinds → trim, opaque);
  create API accepts an optional kind defaulting to email (existing
  callers unchanged — back-compat pinned by a storetest case); **additive
  migration in BOTH dialect stores** (next number in the auth source;
  `identifier_kind TEXT NOT NULL DEFAULT 'email'` — the NOT-NULL-DEFAULT
  precedent), executor AUDITS the existing invitation unique
  indexes/lookups and adds kind to the identity key where identifier
  equality is assumed email-shaped (verify, don't assume); storetest:
  kind round-trip, default-email back-compat, per-kind normalization,
  cross-kind same-value coexistence.
- **(c) delivery semantics, pinned honestly:** kind `email` → Mailer
  exactly as today. Any other kind → the invitation is created
  UNDELIVERED — loudly documented (README + a doc pin on the create
  path): the host reads it back (listing) and delivers out-of-band until
  the notifier seam lands (owner ruling 4, the named later plan).
  **Executor verifies what `accept` validates about the identifier**
  (does the acceptor's identity get checked against it, or is the token
  the sole claim?) and records the answer in the README's kind section —
  if acceptance is token-only, say so explicitly next to the undelivered
  note (it changes what "deliver out-of-band" means for security).
- No behavior change for existing email invitations anywhere — the A9
  protocol legs must still pass verbatim.

### P3 — docs + records + close

sdk/README identity row updated (Resolver/Address/Info + the no-User-
struct pin); auth README (Resolver section + the kind semantics +
undelivered-delivery note + the accept-validation answer); RELEASING
upgrade note (additive migration — hosts re-export + apply); NOTES.md
milestone entry; deferred-ledger entries (authorization notifiers —
trigger: the first host needing in-band delivery for a non-email kind or
grant notifications; tenancy — owner will schedule); archive to
`.claude/past/`; memory index update.

## Acceptance

```sh
make check    # 35 modules, eleven guards, scaffold legs
make guard
```

Plus: auth hermetic suites green incl. the new storetest cases; the
A9/Z4 email-invitation legs unchanged (spot-drive one live at close);
both dialect live conformance for authentication at close (the additive
migration is a schema change — the store phases' live-leg discipline
applies); RELEASING note present; sdk builds stdlib-only.

## Real-interaction check

Standing check (a) per phase commit. At close: boot `examples/auth-cms`,
drive one email invitation end to end (unchanged behavior), and one
non-email-kind invitation via curl (created → listed → accepted per the
verified accept semantics), exact codes recorded.

## Execution log

(append dated entries here)
