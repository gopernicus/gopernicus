# Segovia v2: repin authentication to v0.19.1

Paste everything below the line into a session working in
`/Users/jrazmi/code/segovia/segovia/v2`. This was prepared on 2026-10-21 from a read-only look
at that checkout, which was on branch `josh/clips-template-key` at `a1c6b9ab` with 5 uncommitted
files.

---

Repin Segovia v2's Gopernicus authentication dependencies to the external-identity-providers
release:

| module | from | to |
|---|---|---|
| `sdk` | v0.12.0 | **v0.14.0** |
| `pockets/authentication` | v0.18.0 | **v0.19.1** (skip v0.19.0) |
| `pockets/authentication/stores/pgx` | v0.8.0 | **v0.9.0** |

This is a dependency repin with one host migration and a client-side CSRF change, not a feature.
Do not adopt external identity providers in this work; that is optional and separate. Plan it
briefly, implement it, and verify the real host behaviour, meaning the running server and the
flightdeck SPA, not only the tests.

## Before you edit

- The main checkout is on a feature branch with uncommitted work. **Do not touch it.** Create a
  worktree or branch from `main` (confirm `git status`, the branch and HEAD first).
- Read the upstream release record: `RELEASING.md` in `github.com/gopernicus/gopernicus`, the
  entry "External identity providers (… authentication v0.19.0 → v0.19.1 …)". Its BREAKING and
  Known sections are the source of truth; this prompt summarises them for Segovia.
- **Why not v0.19.0:** it refused host-signer tokens that carry an `iss` or a non-JSON payload
  before the signer ran. Segovia's `golangjwt.New` signer sets no `iss`, so it was not affected,
  but v0.19.1 also carries the CSRF hardening below.

## What changes for Segovia

1. **A host migration is REQUIRED, even though Segovia wires no external provider.** The
   pgx store's boot-time schema probe now checks `external_subjects`, so the server will not
   construct without it. Segovia squashes the three upstream sources into one `auth/` ledger with
   host numbering (see `workshop/migrations/auth/README.md`; the last file is
   `0023_iam_operations_fenced.sql`). So:
   - take upstream `0020_external_subjects.sql` from `pockets/authentication/stores/pgx` v0.9.0
     (`MigrationsFS`/`MigrationsDir` or `ExportMigrations`) **byte for byte**, as host
     **`0024_external_subjects.sql`**;
   - add `"0020_external_subjects.sql": "0024_external_subjects.sql"` to the rename map in
     `workshop/migrations/ledger_test.go`, and update the README table (stores/pgx v0.9.0;
     23 files, `0001`–`0024`; the `0017` gap stays);
   - `workshop/authenticationtest/fixture.go` reads the upstream FS directly, so it picks the file
     up from the new pin; check it still passes.
2. **Native mutations now check the session is live.** By owner ruling, `LiveOnMutation` is the
   default for native tokens too. A POST, PUT, PATCH or DELETE with a valid access JWT whose
   session was revoked is now **401**; GETs are unchanged. This costs one session-row read per
   mutating request. `RequirePrincipal(Stateless())` restores the old behaviour per route; use it
   only where a route must stay stateless, and say why.
3. **A session-store read error is now 503 with `Retry-After`**, not 401. The flightdeck SPA must
   treat 503 on an authenticated call as retryable and must **not** sign the user out on it.
4. **The CSRF gate is strict, and the invitation routes are now gated.** A cookie-authenticated
   POST, PUT, PATCH or DELETE to the pocket's JSON routes needs the `X-CSRF-Token` header
   (bootstrap with `GET /auth/csrf`), whatever the content type. Form-encoded bodies no longer skip
   it except on the HTML account-form routes.
   - `ui/ts/apps/flightdeck/src/pockets/auth/logic/invitation-accept.ts` says invitations are
     "NOT CSRF-gated", which is no longer true. `POST /auth/invitations/{type}/{id}`, `/accept`,
     `/{id}/cancel` and `/{id}/resend` now need `csrf: true` through the shared wrapper.
     `/{id}/decline` stays session-less but is now origin-gated.
   - Audit **every** cookie mutation the SPA sends to a bundled `/auth/*` route (identifiers
     PATCH/DELETE, service accounts and API keys, admin user routes, session revoke, password
     change). Each needs the header. The simplest correct change may be to make the shared
     wrapper send it on every non-GET call.
   - CORS already allows `X-CSRF-Token` (`cmd/server/cors_headers_test.go`).
5. **`CredentialService` changed signature** (`Authenticate`/`RequireLive` return
   `(ctx, error)`). Segovia neither implements it nor calls those methods (grepped 2026-10-21);
   re-check with `git grep -nE '\.Authenticate\(|\.RequireLive\(|CredentialService'`.
6. **The default credential sets admit wired external tokens.** With none wired, nothing
   changes.

## Verify

- `go build ./... && go vet ./... && go test ./...`. The ledger test must pass with the new
  rename entry.
- `make migrate` against a scratch database (never the shared or dev database without asking).
  Confirm `iam.external_subjects` exists, then that the server boots. Also confirm that booting
  **without** the migration fails at the schema probe, which proves the probe is live.
- Run the server and the flightdeck SPA, then check:
  - sign in;
  - create and accept an invitation through the UI (it must succeed with the header);
  - a hand-made cookie POST to `/auth/invitations/{id}/cancel` **without** `X-CSRF-Token` gets
    **403**;
  - revoke the session from another tab, and the next mutation is 401 while a GET still loads;
  - stop the database briefly: an authenticated call is 503 and the SPA does not sign out.
- Report what passed, what you could not run, and the PR link.

## Also pending for Segovia (separate work; do not fold in silently)

- `plans/segovia-v2-authorization-v0.26-repin-handoff.md` (authorization v0.24.0 → v0.26.0) is
  independent of this repin. Do it as its own PR, before or after this one.
- `plans/segovia-v2-invitation-settlement-handoff.md` targeted authentication v0.18.0, which
  Segovia already pins. Check whether it was fully applied. If so, tell the owner it can be
  deleted from `plans/`.
