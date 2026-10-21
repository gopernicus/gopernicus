# gps-360-go: repin authentication to v0.19.1

Paste everything below the line into a session working in
`/Users/jrazmi/code/gps/three-sixty/gps-360-go`. This was prepared on 2026-10-21 from a read-only
look at that checkout, which was on `main` at `7afc4d39`, clean.

---

Repin gps-360-go's Gopernicus authentication dependencies from a **v0.14.0** baseline to the
external-identity-providers release:

| module | from | to |
|---|---|---|
| `sdk` | v0.12.0 | **v0.14.0** |
| `pockets/authentication` | v0.14.0 | **v0.19.1** (skip v0.19.0) |
| `pockets/authentication/stores/pgx` | v0.7.0 | **v0.9.0** |
| `pockets/authentication/views/goth` | v0.6.0 | v0.6.0 (unchanged; it resolves against v0.19.1) |

This crosses **five** authentication minors, each with its own host steps. It is a dependency
repin plus the host changes those releases require, not a feature. Do not adopt external
identity providers in this work. Plan it, ideally as a short plan file under `plans/` per this
repo's convention, implement it, and verify the real host behaviour.

## Before you edit

- Branch from `main` (confirm `git status`, the branch and HEAD; this repo vendors, so expect a
  large `vendor/` diff).
- Read the upstream release record in `github.com/gopernicus/gopernicus` `RELEASING.md`,
  **oldest first**, for every release you cross. Each entry's host steps are the source of truth:
  1. "Invitation resource rule (authentication v0.15.0)"
  2. "Namespace origin policy (sdk v0.10.0; authentication v0.16.0 — published 2026-09-28)"
  3. "Invitation grant operation ledger (published 2026-10-06)" (authentication v0.17.0)
  4. "Invitation terminal settlement (published 2026-10-13)" (authentication v0.18.0)
  5. "External identity providers (… authentication v0.19.0 → v0.19.1 …)": the BREAKING and
     Known sections.

  Some of steps 1–4 may already be satisfied on the authorization side: the host ledger already
  carries the operation-ledger migrations as `0026`/`0027`. Check each step against the code
  instead of assuming.
- **Why not v0.19.0:** it refused host-signer tokens that carry an `iss` or a non-JSON payload
  before the signer ran. gps-360-go's `golangjwt.New` signer sets no `iss`, so it was not
  affected, but v0.19.1 also carries the CSRF hardening below.

## What the v0.19.1 release changes for gps-360-go

1. **A host migration is REQUIRED, even though gps-360-go wires no external provider.** The
   pgx store's boot-time schema probe now checks `external_subjects`. gps-360-go squashes the
   upstream sources into one `auth/` ledger with host numbering (see
   `workshop/migrations/auth/README.md`; the last file is `0027_iam_operations_fenced.sql`). So:
   - take upstream `0020_external_subjects.sql` from `pockets/authentication/stores/pgx` v0.9.0
     **byte for byte**, as host **`0028_external_subjects.sql`**;
   - add the `"0020_external_subjects.sql": "0028_external_subjects.sql"` mapping to
     `workshop/migrations/ledger_test.go`, and update the README table (stores/pgx v0.9.0; file
     count and range);
   - also check whether any upstream authentication migration between `0019` and `0020`
     appeared in v0.15–v0.18. The ledger test's byte-for-byte pin will tell you.
2. **Native mutations now check the session is live.** By owner ruling, `LiveOnMutation` is the
   default for native tokens too. A mutating request with a valid access JWT whose session was
   revoked is now **401**, at the cost of one session-row read per mutation. gps-360-go has a
   large API, so look at whether any high-volume mutating route should opt into
   `RequirePrincipal(Stateless())`, and justify each one. The default is the safer behaviour.
3. **A session-store read error is now 503 with `Retry-After`**, not 401. Clients (the SPA in
   `ui/`, the `cmd/gps360` CLI) must treat 503 as retryable and not as signed out.
4. **The CSRF gate is strict, and the invitation routes are now gated.** A **cookie**
   authenticated POST, PUT, PATCH or DELETE to the pocket's JSON routes needs `X-CSRF-Token`
   (bootstrap `GET /auth/csrf`), whatever the content type. Form bodies no longer skip it except on
   the HTML account-form routes and the OAuth2 consent form (the MCP consent page already posts
   `csrf_token`, so it is unaffected). Bearer and API-key callers, including the `cmd/gps360` CLI,
   are unaffected. Audit every cookie mutation the SPA sends to a bundled `/auth/*` route
   (invitations, identifiers, service accounts and API keys, admin users, session revoke,
   password change) and make sure each sends the header.
5. **`CredentialService` changed signature** (`Authenticate`/`RequireLive` return
   `(ctx, error)`). gps-360-go neither implements it nor calls those methods (grepped
   2026-10-21); re-check outside `vendor/`.
6. **The default credential sets admit wired external tokens.** With none wired, nothing
   changes.

## Verify

- `go mod tidy && go mod vendor`, then `go build ./... && go vet ./... && go test ./...`, then
  the repo's own `make` guard targets. The ledger test must pass.
- Apply the migrations to a scratch database. **Never use port 55432.** Confirm
  `iam.external_subjects` exists and the server boots. Also confirm that booting without the
  migration fails at the schema probe.
- Run the server and the SPA, then check:
  - sign in;
  - an invitation create and accept through the UI succeeds;
  - a hand-made cookie POST to a bundled mutation without `X-CSRF-Token` gets **403**;
  - a revoked session gives 401 on the next POST while a GET still works;
  - the CLI, with a bearer token, still works end to end.
- Report what passed, what you could not run, and the PR link.

## Also pending for gps-360-go (separate work; do not fold in silently)

- `plans/gps-360-go-authorization-v0.26-repin-handoff.md` (authorization v0.21.0 → v0.26.0,
  with required authorization migrations and goredis v0.5.0) is independent of this repin. Do it
  as its own PR. Both touch the `auth/` ledger numbering, so whichever lands second renumbers on
  top of the first.
