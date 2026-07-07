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
