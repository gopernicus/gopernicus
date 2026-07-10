# features/authentication — the identity feature

A pluggable, datastore-free identity feature. v1 shipped password + session
authentication — registration, email verification, login/logout, password
reset — and the `RequireUser` middleware other features gate on. v2 grew the
rest of the identity capability: password change, OAuth login/linking, machine
identity (service accounts + API keys), stateless bearer JWTs, a synchronous
security-event audit rail, and ReBAC-decoupled resource invitations. Session
identity stays server-side (opaque token, cookie-delivered, hashed at rest —
see below); the surface is JSON API only — no server-rendered pages (a host
wanting a login *page* renders its own form and calls this API, exactly as a
SPA would).

Designs of record: `.claude/plans/restructure/auth-feature-design.md` (v1) and
`.claude/plans/roadmap/auth-v2-feature-design.md` (v2, ratified AV1–AV11).

## Layout (the trio — see `features/README.md` §2 for the full contract)

```
auth.go                  the socket: Repositories, Config, PasswordHasher,
                         Granter, MemberCheck, Principal, Service, NewService,
                         Register — the entire host-facing exported surface
domain/                  the hexagon's public rim — entities + repository
  user/ session/         ports. Public BY NECESSITY: hosts and store modules
  verification/          implement/import these across module boundaries
  oauthaccount/ oauthstate/
  serviceaccount/ apikey/
  securityevent/ invitation/
internal/
  logic/authsvc/         the identity service — the sealed interior
  logic/invitationsvc/   the invitation service (built only when a Granter is wired)
  inbound/http/          driving adapter: JSON handlers + route table
  redirect/              exact-match open-redirect allowlist matcher
storetest/               executable spec for domain/'s ports + the reference
                         in-memory implementation
stores/turso/            the outbound tier: per-dialect SQL + canonical
stores/pgx/              migrations, each its own module
```

## Route surface

Claimed namespace **`/auth/*`** (prefixable via `feature.PrefixRegistrar`;
JSON bodies carry no in-page links, so C1's absolute-link limitation does not
apply). Requests are strictly decoded — unknown fields are rejected (400).
The optional subsystems are **deny-by-absence**: leave the enabling
collaborator nil and their routes are NOT registered (404) — never a
half-registered state.

**Always registered — password + sessions:**

- `POST /auth/register` — `{email, password, display_name}` → 201
- `POST /auth/verify` — `{code}` → 200
- `POST /auth/login` — `{email, password}` → 200 + session cookie. Rate-limited
  (email+IP key) BEFORE any credential work → 429. With
  `Config.RequireVerifiedEmail` set, an unverified user gets 403.
- `POST /auth/logout` — session-gated → 200 + cookie cleared
- `POST /auth/password/forgot` — `{email}` → 200 (never reveals whether the
  email exists)
- `POST /auth/password/reset` — `{token, password}` → 200
- `POST /auth/password/change` — session-gated,
  `{current_password, new_password}` → 200 + a NEW session cookie. A password
  change revokes ALL the user's sessions (`SessionRepository.DeleteByUser`)
  and mints a fresh one for the caller.

**OAuth — registered only when `Config.Providers` is non-empty:**

- `GET /auth/oauth/{provider}/start` → 302 to the provider (PKCE S256,
  server-side state, OIDC nonce when the provider supports it)
- `GET /auth/oauth/{provider}/callback` → 302. Three-way branch: existing link
  → login; existing user with a matching email but no link → **pending link**
  (a single-use expiring secret is mailed; the link completes only via
  verify-link — the account-takeover gate); no user → register + link.
- `POST /auth/oauth/verify-link` — `{token}` → completes a pending link and
  logs the user in (the mailed secret proves email ownership)
- session-gated: `GET /auth/oauth/linked`,
  `GET /auth/oauth/{provider}/link/start`,
  `DELETE /auth/oauth/{provider}/link` — unlink refuses to remove the user's
  only credential when no password is set (409, `auth.ErrOAuthLastMethod`)

**Machine identity — registered only when `Repositories.ServiceAccounts` and
`Repositories.APIKeys` are both wired; all session-gated:**

- `POST /auth/service-accounts`, `GET /auth/service-accounts`
- `POST /auth/service-accounts/{id}/keys` — mint; the response carries the
  key's plaintext EXACTLY ONCE (SHA-256 at rest; listing never re-exposes it)
- `GET /auth/service-accounts/{id}/keys`
- `POST /auth/api-keys/{id}/revoke`

**Bearer JWT — registered only when `Config.TokenSigner` is wired:**

- `POST /auth/token` — login-shaped `{email, password}` → 200
  `{token, expires_at}`; shares `/auth/login`'s pre-credential rate limit
  (same key, same budget) and its verified-email gating

**Invitations — registered only when `Config.Granter` is wired; session-gated
except decline:**

- `POST /auth/invitations/{resource_type}/{resource_id}` —
  `{identifier, relation, ...}` → 201 pending; a known user + `auto_accept`
  short-circuits to an immediate grant (direct-add)
- `GET /auth/invitations/{resource_type}/{resource_id}`
- `GET /auth/invitations/mine` — invitations addressed to the caller's email
- `POST /auth/invitations/accept` — `{token}` → grant through the Granter,
  mark accepted
- `POST /auth/invitations/{id}/cancel`, `POST /auth/invitations/{id}/resend` —
  plain `InvitedBy == caller` ownership checks (no tuples, ever)
- `POST /auth/invitations/{id}/decline` — public, token-authorized,
  IP-rate-limited

## The middleware surface (what other features and host routes gate on)

- `Service.RequireUser` — v1 semantics unchanged: session cookie → user
  identity. When `Config.TokenSigner` is wired it ALSO accepts
  `Authorization: Bearer <jwt>`, resolving to the same user identity.
- `Service.RequireServiceAccount` — API-key bearer only.
- `Service.RequirePrincipal` — any configured credential class (session,
  API-key bearer, bearer JWT); stashes the resolved `auth.Principal{Type, ID}`.
- `Service.CurrentUser(ctx)` / `Service.CurrentPrincipal(ctx)` — read the
  resolved identity; `Service.AuthenticateAPIKey(ctx, rawKey)` for non-HTTP
  callers.

Bearer classing uses the two-dot heuristic (a JWT has exactly two dots; minted
API keys use a dotless encoding so they can never collide), and each arm is
active only when its credential class is configured: no `TokenSigner` → bearer
JWTs are NEVER parsed; no machine repos → keys are never looked up.

## Repositories (the eleven ports a host or store adapter satisfies)

```go
type Repositories struct {
    // v1 core — required.
    Users              user.UserRepository
    Passwords          user.PasswordRepository // separate from Users: credential
                                               // material rotates independently
    Sessions           session.SessionRepository
    VerificationCodes  verification.CodeRepository
    VerificationTokens verification.TokenRepository
    // v2 — each optional, with the nil semantics below.
    OAuthAccounts   oauthaccount.OAuthAccountRepository
    OAuthStates     oauthstate.StateRepository
    ServiceAccounts serviceaccount.ServiceAccountRepository
    APIKeys         apikey.APIKeyRepository
    SecurityEvents  securityevent.SecurityEventRepository
    Invitations     invitation.InvitationRepository
}
```

Nil semantics for the optional ports (charter item 12):

| port | nil means | coupling / loud error |
|---|---|---|
| `OAuthAccounts`, `OAuthStates` | allowed only while `Config.Providers` is empty (OAuth off) | Providers set + either nil → **`ErrOAuthReposRequired`** at construction |
| `ServiceAccounts`, `APIKeys` | both nil → machine subsystem OFF: lifecycle routes not registered, the bearer API-key path inert | **both-or-neither** — exactly one wired → **`ErrMachineReposRequired`** at construction |
| `SecurityEvents` | **no audit trail** — the synchronous recording site is a no-op (ratified AV9); this is the one port whose absence degrades silently by design | none — independent of every other port; never a construction error |
| `Invitations` | allowed only while `Config.Granter` is nil (invitations off) | Granter set + nil → **`ErrInvitationRepoRequired`** at construction |

Sentinel contract (the port doc comments are the spec; `storetest` is its
executable form): duplicate → `errs.ErrAlreadyExists`; absent →
`errs.ErrNotFound`; expired session/code/token/invitation → `errs.ErrExpired`
on read. Pinned v2 contracts worth knowing before implementing the ports:
`APIKeyRepository.GetByHash` selects by hash ALONE and returns ANY present row
(revoked and expired included; NULL expiry = never expires) — revocation and
expiry are service-layer decisions, so the audit rail can attribute a blocked
key to its service account; `StateRepository.Consume` is single-use
get-and-delete regardless of expiry (expired → `ErrExpired` with the row
gone); `InvitationRepository.Create` enforces one PENDING invitation per
`(resource_type, resource_id, identifier, relation)`. Paginated ports order by
`created_at DESC, id DESC` — the id tiebreak is contractual.

## Config — required vs defaulted vs deny-by-absence

Every optional field documents its nil semantics (charter item 12). Three
partial-wiring states fail LOUDLY at `NewService`/`Register` —
`ErrOAuthReposRequired`, `ErrMachineReposRequired`,
`ErrInvitationRepoRequired` — never a silent half-on subsystem.

| field | nil/zero means |
|---|---|
| `Hasher` (PasswordHasher) | **hard error** (`ErrHasherRequired`) — a password feature with no hasher is a security foot-gun, not a convenience |
| `Mailer` (email.Sender) | **hard error** (`ErrMailerRequired`) — silently dropping verification/reset/invitation mail is unsafe degradation |
| `MailFrom` | From address on verification/reset/invitation mail |
| `Notifiers` (`[]notify.Notifier`) | **nil-safe, deny-by-absence per kind** (identity-resolution, 2026-07-10): the wired set DEFINES which invitation identifier kinds this host supports beyond email. Email is ALWAYS supported via the required Mailer; a non-email kind (phone, slack, …) needs a wired Notifier of that kind or create fails loudly (`ErrKindNotSupported`, 400). Duplicate kinds → `ErrDuplicateNotifierKind` at NewService. Scope: INVITATION delivery only — verification/reset mail stays on `Mailer` directly (a documented asymmetry; unifying all outbound onto notify is deferred) |
| `RateLimiter` | `ratelimiter.NewMemory()` — an in-process limiter, not "unlimited" |
| `SessionCookie` (CookieConfig) | zero value usable: name `session`, path `/`, browser-session cookie backed by a 7-day server session |
| `RequireVerifiedEmail` | **false** (ratified AV8). `true` → `/auth/login` AND `/auth/token` refuse unverified users with 403 (`auth.ErrEmailNotVerified`). **WARNING: `true` requires a WORKING Mailer.** Verification codes only reach users through it — with the console sender they appear ONLY in server logs, and a misconfigured mailer means nobody can verify, so nobody can log in: total login lockout. |
| `Providers []oauth.Provider` | OAuth subsystem OFF — routes not registered (deny-by-absence); `Repositories.OAuthAccounts`/`OAuthStates` may then be nil. Non-empty → both repos required (`ErrOAuthReposRequired`). |
| `TokenEncrypter` (cryptids.Encrypter) | provider tokens are **not persisted** (login/linking work; no offline provider-API access) — a safe, documented silent degradation; wire `cryptids.NewAESGCM` to store them |
| `OAuthCallbackBase` | the absolute origin callback URLs are built from; meaningful only when `Providers` is set |
| `RedirectAllowlist` | only the same-origin default `/` is allowed; a requested redirect not on the exact-match list falls back to `/` (never an open redirect, never a hard 400) |
| `TokenSigner` (cryptids.JWTSigner) | JWT mode OFF — bearer JWTs are NEVER parsed and `POST /auth/token` is not registered (deny-by-absence). When wired: note the revocation asymmetry — a JWT outlives password change/logout until expiry; short TTL is the mitigation. No refresh tokens (AV6). |
| `TokenTTL` | `0` → 1h; meaningful only when `TokenSigner` is wired — keep it short, it bounds the revocation-asymmetry window |
| `Granter` | invitation subsystem OFF — the entire route surface not registered (deny-by-absence); `Repositories.Invitations` may then be nil. Non-nil → `Repositories.Invitations` required (`ErrInvitationRepoRequired`). |
| `MemberCheck` | no duplicate-membership check before direct-add (idempotent grants absorb duplicates); meaningful only when `Granter` is wired |
| `Logger` | `Register` defaults it to the Mount's logger; `NewService` alone → `slog.Default()`. Receives the audit rail's best-effort WARN lines. |

`integrations/cryptids/bcrypt` satisfies `PasswordHasher` structurally
(`bcrypt.New()`); `integrations/cryptids/golang-jwt` satisfies
`cryptids.JWTSigner`; `integrations/oauth/{google,github}` satisfy
`sdk/oauth.Provider`. None of them imports this module, and this module
imports none of them — `features/authentication/go.mod` requires exactly `sdk`.

## Invitation identifier kinds (identity-resolution, 2026-07-10)

`Invitation.IdentifierKind` (default `email` — `identity.KindEmail`) makes
the invitee address polymorphic: email, phone, or any open kind the host
wires a `notify.Notifier` for. The rules:

- **Supported kinds** = email (always, via the required Mailer) + every
  wired Notifier kind. An unsupported kind fails create loudly
  (`ErrKindNotSupported` → 400); the invitation is not created.
- **Delivery**: the token is DELIVERED for every kind (never returned in a
  response) — email rides `Mailer` exactly as before (or a wired
  email-kind Notifier, which takes precedence); other kinds ride their
  Notifier. Both the invite and the member-added notice follow the fork.
- **Normalization** is kind-aware and service-owned: email →
  trim+lowercase; every other kind → trim only (opaque).
- **The trust model**: email acceptance keeps the acceptor-email match
  (accounts have emails). Non-email acceptance is authenticated session +
  valid token — the binding is ADDRESS-POSSESSION via delivery, i.e. the
  email trust model minus the account-match, which cannot exist until
  address verification lands (a named deferred item). `AutoAccept` and
  the email-keyed listings (`Mine`, login-time resolution) apply to
  email-kind invitations ONLY — a non-email invitation is claimed by
  token, never auto-attached.
- The create API accepts an optional `identifier_kind` (default email);
  the invitation JSON response does not surface the kind yet (a known
  v1 gap, noted).

## Session tokens are hashed service-side (v2)

The service SHA-256-hashes the cookie token before every repository
`Create`/`Get`/`Delete` — one private hash helper in `authsvc` is the ONLY
hashing site, and every mint path (login, OAuth callback, verify-link,
password change) routes through it. Stores keep persisting an opaque string
under the existing `token` column: **no DDL, no store knowledge of hashing**.
`Session.Token` holds the stored value (the hash); the plaintext cookie value
is returned exactly once at mint and never persisted anywhere. Upgrading a
live v1 host across this change forces a logout — see the upgrade note below.

## The security-event audit rail

When `Repositories.SecurityEvents` is wired, every sensitive operation records
an append-only `securityevent.SecurityEvent` synchronously: register, login
(success/failure/blocked), logout, email_verified, password_change,
password_reset, the five OAuth events (oauth_login, oauth_register,
oauth_link_verified, oauth_linked, oauth_unlinked), apikey_auth (success;
revoked → blocked with service-account attribution; expired → failure),
token_issued, and the invitation events (invitation_created,
invitation_granted success/failure, invitation_declined,
invitation_cancelled). Two properties are non-negotiable: an audit-write
failure is logged at WARN (coarse fields only) and NEVER fails the auth flow;
and audit content carries identifiers and key PREFIXES only — raw API keys,
JWTs, session tokens, passwords, and OAuth tokens never land in it. One
deliberate quiet spot: the forgot-password *request* records nothing (it must
not reveal whether an email exists); `password_reset` is recorded when a reset
completes. There is no HTTP read surface — query the table, or see the proof
host's dev-only debug route for the pattern.

## UPGRADE NOTE — v1 → v2 invalidates all live sessions

The session-hashing change (design §7.3) means a v1 host's existing plaintext
session rows never match a hashed lookup again. Deploying past it:

- **Every live session is invalidated — a forced logout for all users,
  remember-me/long-TTL sessions included.** Users just log in again; no data
  is lost.
- The orphaned plaintext rows are immediately unreachable and dead past their
  natural `ExpiresAt` TTL. No purge ships; hosts may vacuum them
  (`DELETE FROM sessions WHERE expires_at < now`-style) at leisure.
- **Deploy in a single cutover or drain first — do not roll.** On a rolling
  deploy, mixed plaintext/hashed pods make the SAME cookie flap 401/200
  depending on which pod answers, for the whole rollout window.
- **A rollback forces a SECOND mass logout**: sessions minted by the new
  binary are hashed rows the old binary cannot read.

The same note lives in `RELEASING.md` keyed to this module's next tag.

## Wiring the identity capability

`auth.Service` also implements `identity.Resolver` (identity-resolution,
2026-07-10): `Resolve(ctx, Principal) (identity.Info, error)` — a user
resolves to display_name (else the email local part) + a KindEmail
address; a service account to its Name; anything unknown, missing, or on
a host with the machine subsystem off fails closed with the errs
not-found class. Hosts hand `authSvc` to any consumer wanting
display/contact projection without ever sharing the User record.

One host `main.go` wires everything; it is the only place concrete adapters
are named. **`examples/auth-cms/cmd/server` is this page's executable twin** —
the same wiring on in-memory stores, a host-local fake OAuth provider, and a
toy membership Granter (zero infra; its README carries the full curl
protocol).

```
  stores/turso · stores/pgx · your own impls        host-picked collaborators
                 │ Repositories(db)                             │
                 ▼                                              ▼
        auth.Repositories                                  auth.Config
   (eleven ports; which optional              Hasher ← integrations/cryptids/bcrypt
    ones are wired decides which              Mailer ← sdk/email (SMTP/Console) or sendgrid
    subsystems exist at all)               Providers ← integrations/oauth/google|github
                 │                       TokenSigner ← integrations/cryptids/golang-jwt
                 │                    TokenEncrypter ← sdk/cryptids.AESGCM
                 │                           Granter ← a host authorizer adapter
                 │                                              │
                 └──────────────────┬───────────────────────────┘
                                    ▼
        auth.NewService(repos, cfg) ──► the driving surface (FS2): every use-case
                 │                      as a method (Login, RegisterUser, …) plus
                 │                      RequireUser / RequirePrincipal / CurrentUser /
                 │                      CurrentPrincipal → wired into other features
                 ▼                      + host routes
        authSvc.Register(mount) ──► mounts the /auth/* route surface (optional adapter)
```

```go
// Command server wires the full identity capability: sessions + OAuth +
// machine credentials + bearer JWTs + invitations-with-a-Granter.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	authstore "github.com/gopernicus/gopernicus/features/authentication/stores/turso"
	"github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	googleoauth "github.com/gopernicus/gopernicus/integrations/oauth/google"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/oauth"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// roleGranter adapts the ONE invitation seam (auth.Granter) to whatever
// authorization model the host runs — here a role write over the host's own
// role store; authorization-v1's ReBAC CreateRelationships satisfies the same
// seam later. Grants must be idempotent.
type roleGranter struct{}

func (roleGranter) Grant(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return nil // e.g. INSERT ... ON CONFLICT DO NOTHING into a host role table
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "server exited", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	// The store adapter. The schema was scaffolded into the HOST's migration
	// tree (authstore.ExportMigrations) and applied PRE-BOOT by the host's own
	// runner — the server never migrates (see "Migrations are host-owned").
	db, err := tursodb.Open(tursodb.Config{
		URL:       os.Getenv("TURSO_DATABASE_URL"),
		AuthToken: os.Getenv("TURSO_AUTH_TOKEN"),
	})
	if err != nil {
		return err
	}
	defer db.Close()
	repos := authstore.Repositories(db) // all eleven ports, machine identity + audit included

	// The optional collaborators. This host wires all of them, so its env keys
	// must be set (the constructors error loudly on empty secrets). To turn a
	// subsystem off, drop its Config field entirely (deny-by-absence) — the
	// Config table above says exactly what degrades.
	google, err := googleoauth.New(ctx, os.Getenv("GOOGLE_CLIENT_ID"),
		os.Getenv("GOOGLE_CLIENT_SECRET"), nil, nil)
	if err != nil {
		return err
	}
	signer, err := golangjwt.New(os.Getenv("AUTH_JWT_SECRET")) // >=32 bytes
	if err != nil {
		return err
	}
	encrypter, err := cryptids.NewAESGCM([]byte(os.Getenv("AUTH_TOKEN_ENCRYPTER_KEY")))
	if err != nil {
		return err
	}

	cfg := auth.Config{
		Hasher:               bcrypt.New(),
		Mailer:               email.NewConsole(log), // real hosts: SMTP / sendgrid
		MailFrom:             "auth@example.com",
		RequireVerifiedEmail: true, // demands a WORKING mailer — see the Config table

		Providers:         []oauth.Provider{google},
		OAuthCallbackBase: "https://app.example.com",
		RedirectAllowlist: []string{"/dashboard"},
		TokenEncrypter:    encrypter,

		TokenSigner: signer, // POST /auth/token + bearer-JWT verification

		Granter: roleGranter{}, // invitations grant through the host's model
		Logger:  log,
	}

	router := web.NewWebHandler(web.WithLogging(log))
	router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

	// Built once, mounted once (FS2): NewService is the feature's driving surface
	// — its use-case methods plus the cross-feature middleware — and authSvc.Register
	// mounts the shipped HTTP adapter over exactly that surface.
	authSvc, err := auth.NewService(repos, cfg)
	if err != nil {
		return err
	}
	if err := authSvc.Register(feature.Mount{Router: router, Logger: log}); err != nil {
		return err
	}

	// Gate anything on the resolved identity: humans-only via RequireUser,
	// any credential class (API keys and JWTs included) via RequirePrincipal.
	router.Handle("GET", "/reports", func(w http.ResponseWriter, r *http.Request) {
		p, _ := authSvc.CurrentPrincipal(r.Context())
		_, _ = w.Write([]byte("hello, " + p.Type + ":" + p.ID))
	}, authSvc.RequirePrincipal)

	return web.Run(ctx, router, web.ServerConfig{Host: "localhost", Port: "8080"}, log)
}
```

### Migrations are host-owned (and how the ledger really dedupes)

Auth ships eleven canonical migrations per dialect (`0001_users.sql` …
`0011_invitations.sql`). The scaffold model: `ExportMigrations` copies them
into the host's own migration tree ONCE; from then on the files are the
host's, applied pre-boot by the host's runner (see
`examples/cms/workshop/migrations` for the runner shape), alongside the host's
other migrations. The framework never migrates at startup.

How the ledger actually dedupes: the turso/pgxdb connectors record every
applied file in `schema_migrations` under the single ledger source
`"default"`, deduplicated by **full filename** with a checksum guard. (Design
docs speak of a "per-source ledger" — that is aspirational vocabulary; the
connector API exposes no source parameter, so do not go looking for one.) Two
practical consequences:

- **Numeric-prefix overlap across features is SAFE.** Auth's
  `0009_api_keys.sql` and cms's `0009_terms.sql` share a numeric prefix in a
  composed host — but they are distinct full filenames, so both apply and
  both are remembered.
- **Never renumber scaffolded files.** The filename IS the ledger identity: a
  renamed copy of an already-applied migration looks brand-new and re-applies.
  Extend the tree with new files after the scaffolded ones instead. Editing an
  already-applied file trips the checksum guard on the next run — also by
  design.

### Host environment keys (the proof host's conventions)

The feature itself reads NO environment — hosts map env into `Config`
explicitly. `examples/auth-cms/.env.example` establishes the conventions for
the identity knobs (all secret-free placeholders):

| key | drives | notes |
|---|---|---|
| `AUTH_JWT_SECRET` | `Config.TokenSigner` secret | ≥32 bytes; the proof host generates an EPHEMERAL per-boot key when unset (tokens don't survive restart) — never commit a real one |
| `AUTH_JWT_DISABLED` | signer-nil boot variant | `1` → JWT mode off: `/auth/token` 404, bearer JWTs never parsed |
| `AUTH_TOKEN_TTL` | `Config.TokenTTL` | Go duration; empty → 1h |
| `AUTH_TOKEN_ENCRYPTER_KEY` | `Config.TokenEncrypter` (AES-256-GCM) | exactly 32 bytes; unset → provider tokens not persisted |
| `AUTH_DEBUG` | the proof host's dev-only `GET /debug/security-events` | DEFAULT-OFF and session-gated (it dumps IP/UA/emails); host code, not feature surface |
| `OAUTH_CLIENT_ID` / `OAUTH_CLIENT_SECRET` | a real provider in `Config.Providers` | unused by the proof host's fake provider |

## Datastores — {turso, pgx} out of the box, or none at all

Both dialect stores ship and pass the same `storetest` suite (charter
checklist items 10–11; live runs recorded in NOTES.md — turso against the
remote playground, pgx against docker postgres:17). A host may also satisfy
`Repositories` itself — `examples/auth-cms/internal/authmem` is the zero-infra
proof for all eleven ports. Store conformance is env-gated: turso via
`-tags=integration` + `TURSO_*`; pgx via `POSTGRES_TEST_DSN`
(`docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17`).
Schema notes: the `sessions` table stores the service-computed hash in its
existing opaque `token` column (no v2 DDL touched it); `security_events` is
append-only; child tables carry no enforced FKs (the suite exercises them
independently).
