# coordination-hub adoption checklist

Status: **PREPARED. No downstream edit has been made** — this repo does not touch
coordination-hub, and no tag has been cut yet. Every step below assumes the owner
has cut and pushed the tags in `tag-manifest.md`.

Date: 2026-08-16

---

## 0. Order matters

**Apply the store migrations BEFORE deploying binaries built against the new
tags.** Both store constructors now probe the added columns by name and refuse to
construct without them. That is a deliberate loud boot failure — but it is still a
failed boot if you deploy first.

---

## 1. Copy and apply the new store migrations

Two new canonical files, append-only, both dialects:

- `0014_user_status.sql` — account lifecycle (`users.status`,
  `users.status_changed_at`, the directory's `(created_at, id)` index, and on pgx
  the `COLLATE "C"` pin for `users.id`).
- `0015_challenge_subject_keys.sql` — `challenges.subject_key`, backfilled from
  `user_id`, with the single-active claim moving to `(subject_key, purpose)`.

```sh
# re-export into your migrations directory, then run your normal migrate step
```

**pgx maintenance-window warning:** `0014` runs
`ALTER TABLE users ALTER COLUMN id TYPE TEXT COLLATE "C"`, which rewrites the
primary-key index under an **ACCESS EXCLUSIVE** lock. On a large `users` table,
schedule it. Skipping only that statement does not corrupt anything; it means the
operator directory's `id` tiebreak follows the cluster's default collation.

---

## 2. Update module versions

```sh
go get github.com/gopernicus/gopernicus/sdk@v0.4.0
go get github.com/gopernicus/gopernicus/features/authentication@v0.3.0
go get github.com/gopernicus/gopernicus/features/authentication/stores/pgx@v0.2.0
go get github.com/gopernicus/gopernicus/integrations/datastores/pgxdb@v0.4.0
go mod tidy && go mod vendor
# then re-run your dependency and license guards
```

---

## 3. Move app-wide mail off the authentication feature

The layering inversion the hub flagged is fixed. The generic
`internal/integrations/mailer` (and any notifymail sibling) can **delete its
`features/authentication` import**:

| was | now |
|---|---|
| `auth.RuntimeMode` | `environment.Mode` |
| `auth.RuntimeModeProduction` | `environment.ModeProduction` |
| `auth.ErrInsecureDeliveryTransport` | `email.ErrInsecureTransport` / `notify.ErrInsecureTransport` |
| (hand-rolled check) | `email.CheckSender(mode, sender)` / `notify.CheckNotifier(mode, n)` |

```go
package mailer // your own generic package — no auth import

func RequireProductionCapable(mode environment.Mode, sender email.Sender) error {
    posture, err := email.CheckSender(mode, sender)
    if err != nil {
        return fmt.Errorf("mail transport: %w", err)
    }
    if !posture.ProductionCapable() {
        log.Warn("development-only mail transport wired; never use in production")
    }
    return nil
}
```

The **auth composition** may keep importing the feature — it needs `auth.Config`
regardless. `auth.RuntimeMode` is a type ALIAS, so this migration can be done
package by package with no flag day.

---

## 4. Configure `PasswordResetURL` — REQUIRED in production

```go
cfg.PasswordResetURL = "https://<hub-host>/reset-password"   // or AUTH_PASSWORD_RESET_URL
```

The hub already has a `/reset-password` SPA route that reads a `token` query
value, so this is the value to use. Production construction **fails** without it,
and rejects a plain-`http` value. No fragment, and no pre-existing `token`
parameter.

While you are there, confirm the landing page: read the token once and
`history.replaceState` it away, POST it to `/auth/password/reset` rather than
re-navigating, send `Cache-Control: no-store` + `Referrer-Policy: no-referrer`, and
do not load third-party resources before the URL is scrubbed.

---

## 5. Wire `UserAdminCheck` to the existing platform admin decision

```go
cfg.UserAdminCheck = func(ctx context.Context, req auth.UserAdminCheckRequest) error {
    return authorizer.Check(ctx, req.Principal, "platform:main", "admin")
}
```

This is what MOUNTS the admin routes. The bundled store already supplies
`Repositories.UserAdmin` and `Repositories.ActiveSessions`, so no repository
wiring is needed — but setting the check without them would be a loud
construction error, which is the point.

Authentication imports no authorization; the hub's own `platform:main#admin`
decision stays where it is.

**Decide your last-admin invariant.** Self-deactivation is not forbidden by the
feature. If the hub must not allow an admin to switch themselves off, that rule
belongs in this closure.

---

## 6. Use the admin surface

| route | for |
|---|---|
| `GET /auth/admin/users` | the operator directory (`limit`/`cursor`/`offset`/`count`) |
| `GET /auth/admin/users/{id}` | one user |
| `POST /auth/admin/users/{id}/deactivate` | switch an account off; `changed:false` on replay |
| `POST /auth/admin/users/{id}/reactivate` | switch it back on |
| `POST /auth/admin/users/{id}/verification/resend` | the console's "resend verification" control |

All are live-session-gated; the POSTs also need the Origin + `X-CSRF-Token`
double-submit the hub already sends for other mutations.

Two things to build into the console UI:

- **Deactivation is immediate for live sessions but bounded for stateless access
  JWTs.** Anything behind `RequireLiveSession` denies on the next request;
  `RequireUser`-tier routes honor an already-issued token for up to
  `AccessTokenTTL`. Say so in the confirmation copy rather than implying instant
  global revocation.
- **`changed:false` is a success, not an error.** A retried deactivate returns it.

---

## 7. Add the public verification-resend path

`POST /auth/verification/resend` with `{"email": "..."}` — origin-gated, **no**
CSRF token (the caller has no session, which is the population it serves).

**Always 202.** Do not branch on the response: it is byte-identical for unknown,
malformed, verified, deactivated, and active-unverified addresses. 429 means a
budget (3/address/min, 10/IP/min); 503 means the delivery queue is saturated.

Copy suggestion: "If that address needs verifying, we've sent a new link." Never
"we've sent it to <address>" — that confirms existence.

The registration screen uses **this** route. The admin console uses the
**authorized** one from step 6, which reports real state and returns a receipt.
Do not swap them.

---

## 8. OAuth linking — nothing to adopt, a flag to close

The "no user-initiated OAuth linking" row is **not a code gap**.
`Service.StartLink`, the session-gated `GET /auth/oauth/{provider}/link/start`,
the `LinkUserID` state binding, the `ActionLinked` callback branch,
`GET /auth/methods`, and the code-gated unlink pair have all shipped since
`features/authentication/v0.1.0`.

Close the row citing that version plus the README's new
**"OAuth account linking — two distinct flows"** section and the runnable recipe
at `examples/auth-cms/cmd/server/oauth_link_settings_test.go`. Remove any local
workaround.

Two client notes: `link/start` is a **navigation**, not an XHR (the browser must
follow the 302 and come back with cookies), and the explicit link callback
deliberately sets **no** cookie — re-read `/auth/methods` after the redirect.

---

## 9. Searchable lists

`crud.ListParams.Search` now carries the term; `q` is the canonical key.
`internal/inbound/http/list.go` already parses `q` into a search string and can
ride `crud.ListParams.Search` instead of its own field.

If the hub has v1 clients sending `s`, keep that fallback **at the hub's own
transport edge** with a documented removal milestone. It is deliberately not in
`crud.ListParams`.

Rewrite `list-surfaces.md` D4 to "SQL-side always" and replace R1 with a pointer
to the tags above.

---

## 10. Provision-on-consumption — second train, opt in deliberately

`Config.PasswordlessProvisionOnRedeem` is **false by default** and should stay
false until the hub has accepted the threat model in the README's
**"Provision-on-consumption"** section. Adopt it only after the second train's
tags are in and that checklist is signed off. It is email-links-only and requires
a shared limiter, durable (`jobs`) delivery, a shared store, and the **same**
`IdentifierKeyer` / `ChallengeProtector` key material on every instance.

---

## 11. Upstream-flag dispositions

| coordination-hub flag | disposition | evidence |
|---|---|---|
| no administrative user listing | **RESOLVED** | `GET /auth/admin/users`; `features/authentication v0.3.0` + store `v0.2.0` (migration `0014`) |
| no account status/deactivation | **RESOLVED** | deactivate/reactivate routes + trusted methods; atomic transition + fenced mint, live-proven on both dialects |
| no verification resend | **RESOLVED** | public `POST /auth/verification/resend` + the authorized admin route |
| sdk layouts ignore `Brand.LogoURL` | **RESOLVED, with a correction** | the flag was broader than the code: `marketing.html` already rendered it and `minimal.html` is intentionally unbranded. The real defect was `transactional.html` — the layout ALL auth mail uses — and it is fixed in `sdk v0.4.0`. **The hub overrides the transactional layout at `LayerApp`, so this fix is a local no-op for you**; it matters for default-layout adopters. If you ever drop the override, the logo now works without one. |
| app mail imports auth's `RuntimeMode` / transport error | **RESOLVED** | step 3 above; `sdk v0.4.0` |
| password-reset mail exposes only the raw token | **RESOLVED** | step 4 above; **new required production config** |
| no user-initiated OAuth linking | **NOT A CODE GAP — close as documentation** | shipped since `v0.1.0`; step 8 |
| magic link never provisions an unknown address | **RESOLVED, second train, default-off** | step 10 |
| authorization resolver / role middleware | **OUT OF SCOPE** | not part of this packet; needs its own audit |
| filestorage | **OUT OF SCOPE — do NOT dispatch verbatim** | this checkout now contains `sdk/capabilities/filestorage`; the row needs a fresh version audit before anyone acts on it |

---

## 12. Interim removals

- The hub's `web.ErrConflict(msg).WithCode("conflict")` workaround can revert to
  the plain `web.ErrStateConflict` constructor — that shipped in `sdk v0.3.1` and
  is unchanged here.
- Any local OAuth-link workaround: delete (step 8).
- Any local search-param vocabulary: replace with `crud.ListParams.Search`
  (step 9).
