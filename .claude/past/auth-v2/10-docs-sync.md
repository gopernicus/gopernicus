# Phase A10 — docs sync, upgrade note, wiring page, records

Status: RATIFIED (cut from design §13 A10)
Executor model: **fable** (docs-judgment phase)
Depends on: everything (executes LAST).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §13 A10,
§7.3 (the UPGRADE NOTE requirement), §3/§4/§5.1/§6 (the nil-semantics
content), §10 (what changed where), §12 (the ratified AV table the
capability-map updates cite).

## Work items

1. **`features/auth/README.md`**: the grown `/auth/*` route surface
   (password/session v1 + change-password + oauth + machine lifecycle +
   token + invitations, each with its gate); **charter item-12
   nil-semantics tables for EVERY new port** — Config (Providers,
   TokenEncrypter, OAuthCallbackBase + RedirectAllowlist, TokenSigner +
   TokenTTL, Granter, MemberCheck, RequireVerifiedEmail) and
   Repositories (OAuthAccounts, OAuthStates, ServiceAccounts, APIKeys,
   SecurityEvents, Invitations) — including the deny-by-absence
   couplings and the loud partial-wiring errors
   (`ErrOAuthReposRequired`, `ErrMachineReposRequired`,
   `ErrInvitationRepoRequired`); the session-hashing note (service-side,
   stores opaque). **The `RequireVerifiedEmail` row gains the warning
   (plan-cut amendment): `true` requires a WORKING Mailer — with the
   console sender, verification codes appear only in server logs; a
   misconfigured mailer means total login lockout.** Document the new
   env keys the proof host introduced (mirroring its `.env.example`).
2. **The UPGRADE NOTE (design §7.3 — host-facing, not only a NOTES
   artifact)**: the A1 deploy invalidates all live sessions (forced
   logout) — **including remember-me/long-TTL sessions**; plaintext rows
   are unreachable and dead past their natural TTL; hosts may vacuum.
   **Deploy-mode guidance (plan-cut amendment, SRE): recommend a
   single-cutover or drained deploy — on a rolling deploy, mixed
   plaintext/hashed pods make the same cookie flap 401/200 for the whole
   window; and a ROLLBACK forces a second mass logout (hashed rows are
   unreadable to the old binary).** Lands in `features/auth/README.md`
   AND `RELEASING.md` (an upgrade-notes section keyed to the module's
   next tag).
3. **The wiring page** (plan-cut requirement 3): one diagram + one
   complete `main.go` for the identity capability — session + OAuth
   provider + machine credentials + JWT + invitations-with-Granter —
   with A9's host as the executable twin. Placement: the auth README or
   a `docs/` page beside it, matching whatever precedent events-v1's
   wiring page has set by then (if none exists yet, the README).
   **Migration-source language, corrected (plan-cut amendment, verified
   against the turso/pgxdb connectors' migrate.go): the connectors record
   everything under ledger source `"default"` with FULL-FILENAME dedup —
   the design's "per-source ledger" is aspirational vocabulary, and
   executors/hosts must not chase a source parameter the connector API
   doesn't expose. The wiring page states: the auth+cms numeric-prefix
   overlap (both trees start at 0001) is SAFE because dedup is by full
   filename, and hosts must NOT renumber scaffolded files.**
4. **Cross-repo doc touches**: ARCHITECTURE.md/README/RELEASING sanity —
   module count UNCHANGED (26; verify no stale counts were introduced);
   `.claude/plans/restructure/capability-map.md` — mark the v2 identity
   rows BUILT (OAuth port+flow, API keys, service accounts, security
   events audit, invitations, JWT adapter consumption), mark the
   principals row **not salvaged per ratified AV5**, leave
   ReBAC/authorization rows pointing at authorization-v1, and note the
   durable-rail deferral (AV10) on the security-events row.
5. **Records**: the milestone-close NOTES.md entry — phases, decisions
   exercised, the two live-store artifacts (A7a turso playground, A7b pgx
   docker: suite, store, DSN class, result, dated), the A9 protocol
   summary with exact codes.
6. **Fresh-eyes pass**: read the README + wiring page as a new host
   author; fix what confuses; log what was fixed.

## Acceptance

```sh
make check
```

Docs-only phase — the gate is the fresh-eyes pass plus: every new
Config/Repositories port has its nil row (grep the README for each field
name); the UPGRADE NOTE present in both files; capability-map rows
updated; NOTES artifacts recorded.

## Real-interaction check

Standing check (a); plus one full A9 protocol leg re-run (leg 0, the
amended five-step) against the shipped README's own curl instructions —
the docs must be executable as written.

## Execution log

(append dated entries here)

### A10 — 2026-07-07 — PASS (milestone close)

Executor: fable. Base tip: `6df2d55` (A9). Docs-only; no Go code changed
(the wiring example was verified by compilation in a scratch module, never
committed as code).

**Work items, all six:**

1. `features/auth/README.md` rewritten: the grown `/auth/*` route surface
   grouped by gate (always-on password/session incl. change-password; OAuth
   iff Providers; machine lifecycle iff both machine repos; `/auth/token`
   iff TokenSigner; invitations iff Granter, decline public+IP-limited);
   the middleware surface (RequireUser/RequireServiceAccount/
   RequirePrincipal + two-dot classing, arms active only when configured);
   item-12 nil rows for EVERY new Config port (Providers, TokenEncrypter,
   OAuthCallbackBase, RedirectAllowlist, TokenSigner, TokenTTL, Granter,
   MemberCheck, RequireVerifiedEmail, Logger) and Repositories port
   (OAuthAccounts, OAuthStates, ServiceAccounts, APIKeys, SecurityEvents,
   Invitations) with the deny-by-absence couplings and the three loud
   partial-wiring errors named; the RequireVerifiedEmail row carries the
   pinned WARNING (working Mailer or total login lockout); the
   session-hashing note (service-side, stores opaque, no DDL); the pinned
   v2 port contracts (GetByHash any-present-row, Consume
   delete-regardless-of-expiry, partial pending uniqueness, the id
   tiebreak); the audit-rail section incl. the honest
   forgot-password-records-nothing note; the proof-host env-key table
   mirroring `examples/auth-cms/.env.example`. Stale v1 lines fixed:
   "session tokens are stored plain / hashing is a v2 hardening candidate"
   and "login is not gated on email verification in v1".
2. The §7.3 UPGRADE NOTE landed in BOTH files: the README's "UPGRADE NOTE"
   section and a new RELEASING.md "Upgrade notes (keyed to each module's
   next tag)" section — forced logout for ALL users incl.
   remember-me/long-TTL; plaintext rows unreachable + dead past natural
   TTL, hosts may vacuum; single-cutover/drain guidance (rolling deploy =
   the same cookie flapping 401/200 for the window); rollback = a SECOND
   mass logout.
3. The wiring page: in the auth README (checked — no events-v1 wiring-page
   precedent exists; `features/events` and `docs/` don't exist). One
   diagram (repos + Config collaborators → NewService/Register → the two
   surfaces) + one complete `main.go` (turso store, google provider,
   golang-jwt signer, AESGCM encrypter, a roleGranter adapter, a
   RequirePrincipal-gated host route) — **extracted verbatim from the
   README code fence and compiled + vetted green, gofmt-clean, in a
   scratch module with local replaces**; `examples/auth-cms/cmd/server`
   named as the executable twin. The corrected migration-source paragraph:
   full-filename dedup under ledger source `"default"` with the checksum
   guard; "per-source ledger" flagged as aspirational vocabulary; hosts
   must NOT renumber scaffolded files.
4. Cross-repo sanity: module count 26 verified unchanged in
   ARCHITECTURE.md, README.md ("twenty-six"), RELEASING.md ("twenty-six")
   — no stale counts found, no edits needed beyond RELEASING's new
   upgrade-notes section. Capability map: an execution-note block added
   (the jobs-v1/auth-v1/telemetry precedent — the header notes are the
   record; historical rows not rewritten): v2 identity rows BUILT,
   principals NOT SALVAGED per AV5, invitations decoupled per AV4 (the
   row's original authorization+events dependency dissolved), durable rail
   DEFERRED per AV10 with its trigger, ReBAC/authorization rows left at
   authorization-v1, tenancy still trigger-gated.
5. Records: the NOTES.md "2026-07-07 — auth-v2 milestone CLOSED" entry —
   phases, decisions exercised, both dated live-store artifacts (turso
   playground 69 leaves/205s incl. the 0003-checksum ledger reset; pgx
   docker 69 leaves/~2s incl. the BYTEA payload divergence), the A9
   protocol summary with exact codes + the 22-row audit dump, the honest
   divergence ledger (8 items), the close-acceptance results.
6. Fresh-eyes pass, fixes logged: (a) the phase file's "both trees start
   at 0001" premise is FALSE — cms's scaffolded tree starts at
   `0009_terms.sql` (gaps reproduced); the README states the real, checkable
   overlap (auth `0009_api_keys.sql` vs cms `0009_terms.sql`) — closest
   correct thing, divergence logged. (b) The wiring diagram redrawn after
   the first draft misaligned and implied Repositories feeds Config.
   (c) `roleGranter struct{ /* … */ }` was not gofmt-clean as first
   written; comment moved to the doc comment. (d) The "optional
   collaborators" comment in the example implied unset env keys degrade
   silently — clarified that THIS host's constructors error loudly on
   empty secrets and deny-by-absence means dropping the Config field.

**Milestone-close acceptance (all PASS, run this session):** `make check` →
`all checks passed` (26 modules + templ drift + integration-tag vet + 4
guards). Rule-6 import-anchored greps both directions → empty (auth ↛
cms/jobs/authorization; cms ↛ auth). Deferred-rail grep
(`sdk/events|features/events` in features/auth) → empty.
`features/auth/go.mod` requires exactly `sdk`.

**Real-interaction (all PASS):** (a) `examples/minimal`
(`HOST=localhost PORT=8081`): `GET /` 200, `GET /products/widget-3000` 200;
killed; 0 listeners. (b) Docs-executability: A9 leg 0 (the amended
five-step) re-run against `examples/auth-cms` (`HOST=localhost PORT=8082`,
fixed `AUTH_JWT_SECRET`, `AUTH_DEBUG=1`) using the shipped README's OWN
curl commands verbatim: `GET /articles` **401** → register **201** →
login-before-verify **403** → verify **200** (code
`tll2ij2p7tqhi6zvbij7kybwba` read from the console-mailer log) → login
**200**+cookie → `GET /articles` **200** → logout **200** →
`GET /articles` **401**. Killed; 0 listeners.

Housekeeping (same session, after this entry): plan directory moved to
`.claude/past/auth-v2/` + row added to `.claude/past/README.md`.
