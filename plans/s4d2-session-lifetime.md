# S4d part 2 — Session lifetime: idle timeout, access-token ceiling, the liveness floor, external authentication age (SOC 2 readiness, slice 4d part 2)

**Status:** EXECUTED 2026-10-27 (tags: authentication v0.32.0 @ 345ef68d, #221 + #222; stores pgx v0.16.0 / turso v0.15.0 @ 1b56c5c4, #223; record PR-3). Ratified 2026-10-26 (owner: "ratified"): R-1 (b) `QuerierFrom` fix is a separate small PR outside this slice (inventory in `NEXT.md`); R-2 (a) `Live()` floor + `BundledRouteAuthLivenessFloorRelaxed`; R-3 (a) WARN >1h, `AccessTokenTTL > RefreshTTL` refuses; R-4 (a) delegated `aal`/`methods` carried to part 3; R-5 (a) per-provider `MaxAuthenticationAge` semantics documented; idle expiry on refresh keeps the pinned generic 401 (lead default, owner did not rule). Build-time owner rulings 2026-10-27: M2 (a) one age-denial event per token (#222); the CAS-mismatch sentinel stays unexported; E4 (a) the baseline sets `MaxAuthenticationAge`; migration sets are additive-only defaults (ADD COLUMN ok for opt-in features, never destructive), hosts own their schema. PR-1 passed a lead-backend review (ship-with-edits: E1, E2, L3, L4, L6 folded; L5 skipped), PR-2 a data-integration review (ship: LOW-1, LOW-3 folded). The Turso libSQL-server leg was waived (local file accepted). As-built corrections are applied below (§2 `idle_source`; Verification §1(1); the PR-1 file list; Verification §2/§3's baseline legs; Upgrade row 10). Release record: `RELEASING.md` "Session lifetime".

- **Size:** M — three PRs, two trains, three tags (authentication core; stores pgx + turso); one migration (0029, conditional);
  no sdk change, no views change. Part 3 of S4d (M7 key ring + `iss`/`aud`, M8 breach checker + lockout, and — per R-4 —
  `aal`/`methods` on delegated tokens) is a separate plan. S4c-2 (native `all` MFA) holds migration 0028.
- **Anchors.** `main` @ a9035608 (S4d part 1 RELEASED): sdk v0.20.0; authentication v0.31.0; stores pgx v0.15.0 / turso
  v0.14.0 (both pin core v0.31.0, sdk v0.20.0, pgxdb v0.14.0 / datastores-turso v0.9.0); views/goth v0.9.0 (pins core
  v0.31.0); pgxdb v0.14.0; examples pin core v0.31.0 (baseline pgx v0.15.0, auth-cms views v0.9.0); baseline ledger ends at
  0034. Sources: `roadmap.md:186-189` (M4), `review.md:265-272`, `control-map.md:18,35,136`, `RELEASING.md:3-140` (part 1
  as-built: an external admin's IdP session survives revoke-all), `s4d-session-machine-governance.md:442-446` (the handoff),
  `README.md:2658-2666` (the SLA table), `docs/guides/production-posture.md:470-487`.
- **Owner rulings (binding).** Host opt-in everywhere; a migration ships but is REQUIRED ONLY WHEN THE FEATURE IS WIRED
  (unwired + unmigrated boots; wired + unmigrated refuses naming the file); canonical sets ship final schema only; production
  WARNs over refusals unless argued; JSON contracts first-class (goldens on every new or changed route); per-module annotated
  tags MINOR under v0, explicit `GOWORK=off go get` repins, never `go work sync`; one store-repin PR per slice; authentication/
  authorization naming; the 500-line cap.
- **What `main` already does.** Sessions have a fixed 7 d horizon (`RefreshTTL`, never extended by rotation, `session.go:92`)
  and no activity timestamp (`0003_sessions.sql`; `Rotate` is a CAS UPDATE that takes no clock, `stores/pgx/sessions.go:166`).
  `AccessTokenTTL` has no upper bound (`service.go:424-431` defaults zero to 15 m and nothing else). The sensitive bundled
  reads are ALREADY `Live()` by default — `SessionHydration` (`/auth/me`), `SessionSecurityReads` (`/auth/methods`,
  `/auth/delivery/status`, `/auth/passkeys`), `UserAdministration`, `MachineLifecycle` (`routes.go:205-240`, since c3f8b4ad);
  the `/auth/sessions` page is `live` (`session_management.go:17`). The remaining M4 gap is the override seam: `protect` wraps
  every strategy in a `Stateless()` floor (`routes.go:242`), so a host `BundledRouteAuth` override such as
  `PrincipalStrategy(Accept(CredentialExternalToken))` silently lowers `/auth/me` to `LivenessOnMutation`. External tokens
  carry `Identity.AuthenticatedAt` ("auth_time, else iat", `sdk/capabilities/authentication/authentication.go:121`) and the
  engine compares it only to `ValidAfter` (`external.go:517`); nothing bounds a token's authentication age, and the production
  `RevocationFailOpen` line is a plain WARN (`external.go:229`), not a `posture_waiver`. Delegated tokens refuse `aal`/
  `methods` (`token.go:150`); `oauth2/tokens.go:38` mints the delegated session with empty `Authentication`.

## Goal
A host can bound a native session by inactivity (one store column, one CAS statement, no fleet-wide logout on enable), the
access-token window has a stated ceiling and the bundled sensitive reads cannot be lowered below `Live()` by accident, and an
external identity provider's token has a bounded authentication age — each opt-in, each named on the boot log and in the
revocation SLA table.

## Scope and non-goals
**In scope:** `SessionsConfig.IdleTimeout` + `session.ActivityRepository` + migration 0029 (FAC-5/FAC-6; CC6.1); the
`AccessTokenTTL` coherence refusal and production ceiling WARN; the `Live()` liveness floor under the sensitive bundled groups
(R-2); `ExternalProvider.MaxAuthenticationAge` + the `RevocationFailOpen` posture waiver (FAF-7/FAF-8); `session_idle_expired`;
the SLA table rows; docs and the record. **Non-goals:** `aal`/`methods` on delegated tokens (R-4, recommended part 3); the
key-ring signer, `KeyedEncrypter`, `iss`/`aud` (M7, part 3); breach checker and lockout (M8, part 3); native `all` (S4c-2,
0028); an activity stamp on `Live()` reads or on the grace lane; a maintenance idle sweep (rows die at `expires_at`, and
refresh deletes them on contact — BE); an `/auth/sessions` idle filter (a documented residual, BE); a views/goth change;
an sdk change of any kind (R-5 names the one that would be); the `QuerierFrom` fix itself (R-1).

## Decided by gate
Reviews 2026-10-26: lead-backend-engineer (BE), platform-sre (SRE). Folded unless marked.
- BE1 + SRE1 (HIGH/BLOCKER, `COALESCE(last_rotated_at, created_at)` logs out every active session older than `IdleTimeout`
  on the first refresh after the switch, and again after every off-and-on toggle, because unwired rotation never stamps) →
  **the grandfather rule (§1):** `last_rotated_at` non-NULL → judged; NULL with `rotation_count = 0` → judged on
  `created_at` (a login with no refresh is genuinely idle); NULL with `rotation_count > 0` → NOT judged until the first
  `RotateAt` stamps it (`RefreshTTL` still bounds it). Re-enabling after a gap re-judges stale stamps (at most one forced
  re-login per session); the runbook ships the reset (`UPDATE sessions SET last_rotated_at = NULL WHERE session_profile =
  'first_party'`) and an Upgrade-notes row names it.
- SRE2 (HIGH, a blind idle delete breaks the "write path is CAS, never blind" contract, `refresh.go:33-34`; two tabs → two
  events, a just-rotated session deleted) → `DeleteIfIdle(ctx, id, observedStamp)` is a conditional delete (`… WHERE id = @id
  AND last_rotated_at IS NOT DISTINCT FROM @observed AND rotation_count = @observed_count`); the event is recorded ONLY on
  rows-affected = 1; zero rows → the existing `reResolve`. At most one event per session row; no rate cap needed.
- BE2 (HIGH, an idle branch before the match switch swallows theft detection: a replayed consumed previous slot would record
  idle, never `refresh_reuse`) → idle is evaluated ONLY on `RefreshMatchCurrent` and on `RefreshMatchPrevious` with
  `!PreviousUsed`; a consumed slot goes to `reuse()` first; `reResolve` is exempt (it follows a concurrent rotation that just
  stamped).
- BE3 + SRE3 (HIGH, delegated connections are `sessions` rows rotated by `OAuth2.RotateRefresh`, which never stamps) →
  **`IdleTimeout` governs `ProfileFirstParty` only**; delegated connections keep `oauth2.Config.SessionTTL`; the SLA table
  says so.
- BE4 (MED, the Live-tier idle check is near-unreachable — a token minted at rotation expires before the idle deadline by
  the validation rule; only a grace token overshoots, by ≤ `AccessTokenTTL`, the asymmetry stateless routes already accept —
  and swapping `Get` on the hot path would leave `stepup.go:194`, `oauth2/authorization.go:134`, `oauth2/service.go:278`,
  `service.go:1082` inconsistent) → **idle is enforced at refresh only**; `ActivityRepository` shrinks to
  `GetByRefreshHashWithActivity`, `RotateAt`, `DeleteIfIdle`, `ProbeSchema`; `credentialSessionLive` is untouched.
- SRE4 (HIGH) → `RotateAt` is the existing CAS UPDATE plus `last_rotated_at = @now` (app clock) in ONE statement, replacing
  `Rotate` on the wired path only; unwired → `Rotate` unchanged and never names the column; `ConsumeGrace` never stamps; the
  idle comparison runs in Go against `s.now()` (turso stores text timestamps — never compare in SQL); the stamp rides the same
  SELECT as the row through a wired-only column list — `sessionColumns` (`sessions.go:39`, shared with `oauth2.go:184,365`)
  does not change.
- SRE9 (pool path) → `RotateAt` and `DeleteIfIdle` run on `s.db` like `Rotate` today (`pgxdb/db.go:67`), never through
  `QuerierFrom`: a refresh reached inside a host `Transact` under forced RLS would otherwise CAS-miss and burn the session as
  reuse; a code comment pins it and the storetest notes it. Battleboard's RLS scope is an open question (below).
- BE5 + SRE8 (MED, activity = the client's refresh, not the human; a background refresher defeats idle; tolerance is roughly
  `IdleTimeout − AccessTokenTTL`) → refuse `IdleTimeout <= AccessTokenTTL` in every mode (idle would always fire); WARN
  below `2 × AccessTokenTTL`; both on post-default values; README/control-map define "inactivity = no refresh rotation within
  `IdleTimeout`" and name the SPA-timer caveat; the SLA gains "after idle expiry an access JWT still passes `Stateless()`/
  safe `LiveOnMutation` reads for ≤ `AccessTokenTTL`".
- BE7 + SRE10 (refusing `AccessTokenTTL > 1 h` in production breaks boot on a minor bump; the window is already refresh-
  bounded) → production `> 1 h` is a plain WARN (`posture_warning=access_token_ttl_long`), no acknowledgement bool (R-3
  recommends it); `AccessTokenTTL > RefreshTTL` refuses in every mode (incoherent config; a BREAKING row).
- BE9 (HIGH on R-2, a `Live()` floor defeats `RevocationFailOpen` on the bundled groups: a fail-open pass returns
  `stamp=false` (`external.go:478-490`), the floor re-checks and `LivenessLive` fails closed (`external.go:547`) — a 503 on
  `/auth/me` during an IdP outage for a host that chose `OnMutation` + fail-open) → named in the BREAKING row; the hatch is
  `BrowserConfig.BundledRouteAuthLivenessFloorRelaxed` (it governs the floor, which only happens to live beside
  `BundledRouteAuth` in `BrowserConfig`, `config.go:857`); `routes.go:201-203` ("a host override owns the tier") is
  rewritten; `CredentialManagement` is in the floored set. BE's view: do it (overrides are opaque `web.Middleware`, a WARN
  cannot introspect the tier; docs-only leaves M4 open) → R-2.
- BE8 + SRE11 (HIGH, `AuthenticatedAt` is `auth_time` else `iat`; under the `iat` fallback the bound is token age, which
  `exp` already bounds — the core cannot tell which) → the field is **`MaxAuthenticationAge`** (OIDC `max_age` semantics),
  checked before `resolveSubject` like `RequireMFA` (a denied token never provisions), per request (never via the login-record
  cache), with a 60 s skew leeway; zero `AuthenticatedAt` under a bound → deny; bounded `reason` vocabulary
  `authentication_age_exceeded` | `authenticated_at_missing` with `provider`; the README table gives per-provider semantics
  (Firebase `auth_time` = original sign-in → an absolute cap; `iat`-only providers → a token-age bound). The sdk field that
  would close the residual (`Identity.AuthTimeAsserted`) is R-5. Production + no `RevocationChecker` + zero bound → WARN
  (SRE11, the owner rule).
- BE10 + SRE6 (HIGH, item 5): (a) `CreateCode` + a separate `PutCodeAuthentication` is not atomic — the only sound shape is
  one optional `CreateCodeWithAuthentication` copying from the row `CreateCode` already locks (`stores/pgx/oauth2.go:102`);
  mixed-version fleets break (every v0.31.0 verifier REFUSES the claims on delegated tokens, `token.go:150`, so verifiers must
  upgrade before issuers); `principalSet.assure` would newly ADMIT delegated tokens under `RequireMFA`; a login-time `aal2`
  goes stale for the grant's life (M7 claims design); (b) riding the `delegation` JSON does not compile (`session.Delegation`
  is compared with `==`: `FirstParty()`, `RedeemCode`, `sameOAuthCode`) → **R-4, recommended: carried to part 3 with these four
  constraints**; the table is `oauth_authorization_codes` (`0019_oauth2_sessions.sql:17`), and a second migration would need
  its own ledger copy (SRE5).
- BE11 + SRE12 (item 6) → R-1 recommends (b); the inventory is larger than S5 recorded: SEVEN pgx authorization
  `TxFromContext` sites (`mutations.go:81`, `read_snapshot.go:19`, `operations.go:68,126`, `relationships.go:618`,
  `tuple_cache.go:33`, `writes.go:97`, `tuples.go:384`), `integrations/datastores/turso/transact.go:47` has the same
  instance-blind `QuerierFrom` plus eight turso sites, and `Transact`'s nested check (`pgxdb/transact.go:28,41`) is
  instance-blind too (a second `*DB`'s `Transact` inside the first's context is a false `ErrNestedTransact`); today a foreign
  `*DB` runs on the OTHER database's transaction connection, worse than a pool fallback. PR-3 writes the full inventory into
  `NEXT.md` so it cannot vanish again.
- BE6 + SRE15 (numbering) → core 0029 is CLAIMED here; S4c-2 holds 0028 and lands before or after (the runners dedupe per
  file, `pgxdb/migrate.go:304-318`, so order is safe); the ledger copy is the NEXT FREE number at PR-2 time (0035 if this
  slice lands first; the S4c-2 drafter must not assume contiguity); store tags are the next minor at tag time (pgx v0.16.0 /
  turso v0.15.0 if first).
- SRE7 → 0029 opens with `SET LOCAL lock_timeout = '5s'` (the ALTER takes ACCESS EXCLUSIVE on the hottest table; each file
  runs in the runner's transaction, `pgxdb/migrate.go:323`); runbook: "on lock timeout, re-run `make migrate`"; on turso the
  ALTER briefly holds the database write lock.
- SRE10 → the Read-first grep; the `RevocationFailOpen` WARN text change gets an Upgrade-notes row (log alerts keyed on the
  old text); the floor row says "a Stateless `/auth/me` override now does one session read per call". SRE13 → boot lines
  (§5); the sdk has no metrics vocabulary — logs and events only, no counters. SRE14 → the live legs and cold-verify sets
  (Release, Verification §2); `authmem` implements `ActivityRepository` in PR-1 (the `$B` binary is authmem). BE12/BE13 →
  event plumbing (`Types()`, the type test table, the catalog) and a turso encoding round-trip (`last_rotated_at` written in
  exactly `created_at`'s text encoding). BE (side questions) → logout by refresh hash (`service.go:888`) does NOT honour idle
  (logging out a dead session keeps succeeding); `ListByUser` may list idle-dead rows (DTO doc: "may include sessions that
  will be refused at next refresh for idleness"). SRE (the generic 401) → the refresh denial stays the generic
  `ErrInvalidRefreshToken` (the pinned contract); an SPA cannot show "signed out for inactivity" — Open questions.

## Design
### 1. Idle timeout (authentication v0.32.0; stores next minor; FAC-5/FAC-6)
**Config.** `SessionsConfig.IdleTimeout time.Duration` (`env:"AUTH_IDLE_TIMEOUT"`; zero = off; mirrored in
`constructorConfig`, `WithSessions`, `authlogic.SessionsConfig`/`options.go:254`, a `config_env_test.go` row). Validation on
post-default values, every mode: `IdleTimeout < 0` or `IdleTimeout <= AccessTokenTTL` → `ErrIdleTimeoutBelowAccessTokenTTL`
(`sdk.ErrInvalidInput`); `IdleTimeout < 2 × AccessTokenTTL` → WARN `idle_granularity`; `IdleTimeout >= RefreshTTL` → WARN
`idle_never_fires`. **Wired** = `IdleTimeout > 0`: `Repositories.Sessions` must implement the optional interface, else
`ErrIdleTimeoutRequires` (names stores pgx ≥ next minor / turso ≥ next minor); then `ProbeSchema` under a 5 s deadline —
ONLY `sdk.ErrNotFound` → `ErrIdleTimeoutSchemaMissing` naming `0029_session_activity.sql`, anything else → `sdk.ErrUnavailable`
(the 0027 shape, `logic/authentication/constructor.go:334-350`). Setting the env alone flips "migration required": an env
change on an unmigrated database refuses boot (Upgrade notes).
**Port (optional interface, asserted on `Repositories.Sessions`; `session.SessionRepository` unchanged — the set-A invariant):**
```go
type Activity struct{ LastRotatedAt time.Time; Stamped bool } // Stamped=false ↔ NULL
type ActivityRepository interface { // stores pgx/turso next minor; authmem
	GetByRefreshHashWithActivity(ctx context.Context, hash string) (Session, RefreshMatch, Activity, error) // GetByRefreshHash's contract + the stamp, ONE SELECT over a wired-only column list
	RotateAt(ctx context.Context, id, expectedCurrentHash, newHash string, now time.Time) error            // Rotate's CAS + last_rotated_at = now; zero rows → ErrRotationConflict
	DeleteIfIdle(ctx context.Context, id string, observed Activity, observedRotationCount int) (bool, error) // CAS delete; false = the row moved or is gone
	ProbeSchema(ctx context.Context) error                                                                   // sdk.ErrNotFound = 0029 absent
}
```
Both store methods run on the pool path (`s.db`), never `QuerierFrom` (SRE9). `authmem` implements it (map field).
**Judgement (`refreshByHash`, wired, `sess.FirstParty()` only), after `sess.Expired` and the limiter, inside the match switch:**
`lastActivity` = `LastRotatedAt` when `Stamped`; else `CreatedAt` when `RotationCount == 0`; else NOT judged (grandfathered).
Judged and `s.now().Sub(lastActivity) >= IdleTimeout` on `RefreshMatchCurrent`, or on `RefreshMatchPrevious` with
`!PreviousUsed` → `DeleteIfIdle`; `true` → record `session_idle_expired` and return `ErrInvalidRefreshToken`; `false` →
`reResolve` as for a CAS conflict. A consumed previous slot reaches `reuse()` before any idle check; `reResolve` and `grace`
never judge or stamp; `rotate()` calls `RotateAt` when wired (identical `ErrRotationConflict` contract). Logout by refresh
hash, `Live()` reads, the step-up and OAuth2 `Get` sites are untouched.
**Migration `0029_session_activity.sql`** (pgx: `SET LOCAL lock_timeout = '5s'; ALTER TABLE sessions ADD COLUMN IF NOT EXISTS
last_rotated_at TIMESTAMPTZ;`; turso: `ALTER TABLE sessions ADD COLUMN last_rotated_at TEXT;` in `created_at`'s encoding).
NOT in either store's blanket `probeColumns` (`postgres.go:203-217`, `turso.go:165-175`; a unit test pins the exclusion);
old binaries (core v0.31.0 on stores v0.15.0/v0.14.0) keep working on a migrated table because every column list is explicit.
Ledger copy at the next free number (0035 if first), byte-identical, G34.
**Event** `session_idle_expired` (status `blocked`; Details `{session_id, last_activity (UTC RFC 3339), idle_seconds (int),
idle_timeout_seconds (int), surface: "refresh"}`; never a hash) in `Types()` (`securityevent.go:638`) and the admin listing
vocabulary. **Residuals (README):** an in-flight access JWT survives the idle deadline by ≤ `AccessTokenTTL`; a background
refresher defeats idle; `/auth/sessions` may list idle-dead rows; delegated connections are out of scope.

### 2. Access-token ceiling and the session-lifetime boot line
`AccessTokenTTL > RefreshTTL` (post-default) → `ErrAccessTokenTTLExceedsRefreshTTL` in every mode (BREAKING, incoherent
config). Production `AccessTokenTTL > 1 h` (`DefaultAccessTokenTTLCeiling`) → one WARN `posture_warning=access_token_ttl_long
access_token_ttl=<d> ceiling=1h` (R-3; no acknowledgement bool). Boot line: `auth: session lifetime access_token_ttl=<d>
refresh_ttl=<d> idle_timeout=off|<d> idle_source=config|default activity=refresh_rotation idle_scope=first_party
live_floor=enforced|relaxed` (the pocket never reads the process environment, so an env value and a literal are both
`config`; G33). *(Corrected at execution; the draft had `config|env|default`.)*

### 3. The liveness floor under the sensitive bundled groups (R-2)
`withDefaults` (`routes.go:204-256`): the `protect` floor for `SessionHydration`, `SessionSecurityReads`, `UserAdministration`,
`MachineLifecycle` and `CredentialManagement` becomes `svc.RequirePrincipal(Live())`; `OAuthLinkStart`, `CSRFBootstrap`,
`Invitations`, `BrowserAccount` keep `Stateless()`. The outer gate's read is reused (`CurrentSessionID`, `externalLiveAt`), so
the default postures cost nothing extra; an override lowered to `Stateless()`/`LiveOnMutation()` now pays one session read per
call and, for an external token under `RevocationFailOpen`, fails closed during an IdP outage on those groups (BE9). Hatch
`BrowserConfig.BundledRouteAuthLivenessFloorRelaxed bool` restores the `Stateless()` floor on all five (production
`posture_waiver=BundledRouteAuthLivenessFloorRelaxed` WARN; development a plain WARN). The delegated-token denial floor is
unchanged. `routes.go:201-203` and the `BundledRouteAuthentication` doc (`route_policy.go:24-36`) are rewritten: "a host
override owns the credential set and transports; the liveness floor of a sensitive group is `Live()` unless relaxed".

### 4. External authentication age and the fail-open waiver (FAF-7/FAF-8)
`ExternalProvider.MaxAuthenticationAge time.Duration` (zero = unbounded; negative → `sdk.ErrInvalidInput` in `validateExternal`,
`external_config.go:196`). In `Verify`, after the tenant/email/MFA gates and BEFORE `resolveSubject` (`external.go:~298`):
`AuthenticatedAt.IsZero()` → deny `reason=authenticated_at_missing`; `now − AuthenticatedAt > MaxAuthenticationAge + 60 s` →
deny `reason=authentication_age_exceeded`; both record `external_subject_denied {provider, reason}` and answer 401 (the
provider's own challenge shape), per request, never cached. Production, `Revocation == nil`, zero bound → WARN
`external_authentication_age=unbounded provider=<name> revocation_checker=none` (R-? none: the owner rule; SRE11/BE8 agree).
The production `RevocationFailOpen` line (`external.go:229`) becomes `auth: production posture requirement waived
posture_waiver=RevocationFailOpen provider=<name>` (the `checkPosture` text, `security.go:647`; the old message goes away —
Upgrade notes). Boot line per provider: `auth: external provider lifetime provider=<name> max_authentication_age=off|<d>
revocation_checker=wired|none fail_open=<bool>`. README per-provider table: Firebase (`auth_time` asserted) → an absolute
IdP-session cap, the control that bounds "the IdP session survives revoke-all"; go-oidc (`auth_time` when the IdP sends it,
else `iat`) and auth0 → a token-age bound unless the IdP asserts `auth_time` (request it with `max_age`); the sdk residual is
R-5.

### 5. Events, config mirrors, docs
New event type `session_idle_expired`; extended Details `external_subject_denied` (`reason` gains two values). Config:
`SessionsConfig.IdleTimeout`; `BrowserConfig.BundledRouteAuthLivenessFloorRelaxed`; `ExternalProvider.MaxAuthenticationAge`;
each with its three mirrors (`constructorConfig`, the group struct, the `WithX` copy; BE10 of part 1) and env rows. Errors:
`ErrIdleTimeoutRequires`, `ErrIdleTimeoutSchemaMissing`, `ErrIdleTimeoutBelowAccessTokenTTL`, `ErrAccessTokenTTLExceedsRefreshTTL`
(root re-exports in `configuration_errors.go`). Docs: README ("Session lifetime (SOC 2 S4d part 2)" beside `:2637`: the
inactivity definition, the grandfather rule, the SLA rows, the per-provider age table, the floor; the `/auth/sessions` DTO
residual), `production-posture.md:470-487` (rows: idle expiry; access ceiling; the floor; authentication age),
`keys-and-rotation.md:60` (idle beside revoke-all), `operations-runbooks.md` ("Enable an idle timeout on a live fleet"
(expected wave = 0 under the grandfather rule), "Re-enable after a gap" (the NULL reset), "0029 lock timeout → re-run"),
`SECURITY.md` rows, `control-map.md:18,35,136` (+ the event row), the docs-site pocket page ("Added by S4d part 2").

## Upgrade notes
> **Read first:** `grep -rnE 'AccessTokenTTL|AUTH_ACCESS_TOKEN_TTL|RefreshTTL|AUTH_REFRESH_TTL|BundledRouteAuth|
> WithRouteAuthentication|PrincipalStrategy\(|RevocationFailOpen|ExternalProvider\{|session\.SessionRepository' --include='*.go'`.
> **By host shape:** every host → rows 2, 3, 7; hosts overriding `BundledRouteAuth` → row 4 (the break); external-engine (IdP)
> hosts → rows 5, 6; store hosts wanting idle → rows 1, 8, 9, 11; hosts with their own `SessionRepository` → row 1 (implement
> the interface or leave idle off).

| # | Flip | What the host sees | Hatch | Module@version |
|---|---|---|---|---|
| 1 | `SessionsConfig.IdleTimeout` (`AUTH_IDLE_TIMEOUT`); `session.ActivityRepository`; `session_idle_expired` | opt-in; wired + 0029 absent → BOOT FAILURE naming 0029 (setting the env alone flips it); wired on a store without the interface → `ErrIdleTimeoutRequires`; `<= AccessTokenTTL` refuses; grandfathered rows are never judged until stamped; first-party only | leave zero | authentication@v0.32.0 |
| 2 | **BREAKING:** `AccessTokenTTL > RefreshTTL` refuses boot in every mode | an incoherent pair that booted on v0.31.0 stops; the error names both fields | fix the pair | authentication@v0.32.0 |
| 3 | production `AccessTokenTTL > 1 h` → `posture_warning=access_token_ttl_long`; the `auth: session lifetime` boot line | one WARN line; no behaviour change | — | authentication@v0.32.0 |
| 4 | **BREAKING (R-2):** the `Live()` floor under `SessionHydration`, `SessionSecurityReads`, `UserAdministration`, `MachineLifecycle`, `CredentialManagement` | an override lowered to `Stateless()`/`LiveOnMutation()` now does one session read per call (DB QPS rises) and, for external tokens under `RevocationFailOpen`, fails closed on those groups during an IdP outage; default postures unchanged | `BundledRouteAuthLivenessFloorRelaxed` (production WARN) | authentication@v0.32.0 |
| 5 | `ExternalProvider.MaxAuthenticationAge`; `external_subject_denied reason=authentication_age_exceeded\|authenticated_at_missing`; production WARN when no checker and no bound | opt-in; a bound denies tokens whose `auth_time`/`iat` is older than it — `iat`-only providers get a token-age bound | leave zero | authentication@v0.32.0 |
| 6 | the production `RevocationFailOpen` WARN becomes a `posture_waiver` line; per-provider `auth: external provider lifetime` boot line | log alerts keyed on "external provider revocation fails open on the default liveness tier" stop matching | re-key on `posture_waiver=RevocationFailOpen` | authentication@v0.32.0 |
| 7 | `IdleTimeout`/`MaxAuthenticationAge` mirrors in `.env.example`; `config_env_test.go` rows | none | — | authentication@v0.32.0 |
| 8 | **migration 0029** (`sessions.last_rotated_at`, nullable, `lock_timeout 5s`, excluded from the blanket probe), ledger copy at the next free number; `ActivityRepository` filled on both `SessionStore`s (pool path) | apply 0029 before setting row 1's env; otherwise optional; on lock timeout re-run `make migrate`; turso holds the write lock briefly | — | stores pgx@next minor, turso@next minor |
| 9 | stores repin core v0.31.0 → v0.32.0 | MVS lifts a store host's core (rows 2–6 arrive with it) | — | stores pgx@next minor, turso@next minor |
| 10 | auth-cms `authmem` implements `ActivityRepository` (`AUTH_IDLE_TIMEOUT` commented in its `.env.example`); the baseline ledger copy; **the baseline sets `MaxAuthenticationAge`** on its provider (`IDP_MAX_AUTHENTICATION_AGE`, default 12 h; E4 = (a)) and lists `AUTH_IDLE_TIMEOUT` as not read (external-only); external-identity gets `MaxAuthenticationAge` commented | example behaviour | — | examples (untagged) |
| 11 | re-enabling idle after a disabled window re-judges stale stamps | at most one forced re-login per session; the runbook's NULL reset avoids it | run the reset first | docs |

Row 2 and row 4 are the breaks; row 1/8 is the conditional boot failure. Every module ships MINOR under v0; tag messages
name rows 2, 4, 8, 9. **Not touched:** sdk, views/goth, `pockets`, workshop (no P-rule; the constructor checks are the
posture), ui/goth, any existing port's method set, `sessionColumns`.

**What each adopting shape gets (defaults = idle off, access 15 m, refresh 7 d, floor enforced on the default postures →
no visible change):**
- **gps-three-sixty** (native Google OAuth → first-party sessions, bearer SPA, pgx + goredis; single-factor acknowledged):
  rows 2, 3, 8, 9 at repin; idle and the absolute horizon are its primary session controls — recommended `AUTH_IDLE_TIMEOUT`
  between 1 h and 12 h with `AUTH_REFRESH_TTL` 24 h (apply 0029 first); note the SPA refresh timer defeats idle (BE5): refresh
  on demand, not on a timer. Rows 4–6 do not apply (no override, no external engine).
- **battleboard** (magic link + Google, `invitation_only`, pooled PG under forced RLS, SPA): as gps-three-sixty, plus SRE9 —
  `RotateAt`/`DeleteIfIdle` run on the pool path exactly as `Rotate` does today; if `sessions` is under a tenant policy,
  refresh already breaks on `main` (not a part-2 change) — Open questions.
- **gps-360-go** (old pins): nothing until it repins; then rows 2, 3 (and 8, 9 through the store); idle is opt-in.
- **examples:** auth-cms (authmem; `AUTH_IDLE_TIMEOUT` commented; `$B` exercises §1), production-baseline (ledger copy;
  the external provider SETS `MaxAuthenticationAge`, E4 = (a); no `SessionsConfig`, so idle is not wired), external-identity (rows 5, 6 only).
- **IdP hosts (external engine only):** rows 5, 6; idle does not apply (no native sessions); row 4 only under an override.

## Risks
(1) Row 4 silently changes an overridden `/auth/me` from zero reads to one per call and fails closed under fail-open during
an IdP outage; the Read-first grep, the row and the hatch are the controls. (2) The grandfather rule means a session rotated
before the switch is never idle-judged until its first post-switch refresh — at most one `AccessTokenTTL` of overshoot, and
`RefreshTTL` still bounds it; an SPA that refreshes on a timer is never idle at all (documented). (3) 0029's ALTER queues
behind long readers on the hottest auth table; `lock_timeout 5s` + re-run is the control. (4) `MaxAuthenticationAge` is a
token-age bound on `iat`-only providers and denies every user of a provider that sends neither claim (boot cannot detect
it); the per-provider table and the bounded `reason` are the controls; R-5 is the fix. (5) `QuerierFrom` stays open for any
two-database host (R-1); no adopting host has two instances; under forced RLS a tenant-less pool fallback denies on an unset
tenant only if the policy says so — recorded in `NEXT.md`. (6) Numbering moves if S4c-2 lands first (ledger, tags).

## Tasks as PRs
Worktrees `../gopernicus-s4d2-<pr>`; one merge/tag/push per command (the owner pushes tags); every PR runs with model
**opus**; live legs never touch ports 55432 or 8082; `make guard && make check` in every verify line.

- **PR-1 — authentication core + `authmem` + storetest (`authentication/s4d2-core`).** **Depends on** nothing (no sdk pin
  move). **BREAKING:** rows 2, 4. **Tag:** `pockets/authentication/v0.32.0` after a `live-stores` dispatch on `main`. **Files**
  (under `pockets/authentication/`): `logic/authentication/session/{activity.go (new: Activity, ActivityRepository),
  repository.go (doc: the optional interface), session.go (doc)}`; `logic/authentication/{refresh.go (the judgement inside the
  match switch; RotateAt when wired; DeleteIfIdle; reResolve exemption), options.go + service.go:424-431 (IdleTimeout
  resolution, the refusals and WARNs on post-default values, the ceiling WARN), constructor.go (ErrIdleTimeoutRequires, the 5 s
  probe, ErrNotFound-only mapping), external_config.go (MaxAuthenticationAge validation, the production WARN), external.go
  (the age check before resolveSubject; the two reasons; the posture_waiver line at :229), errors, securityevent/
  securityevent.go (+Types())}`; `inbound/http/{routes.go (the Live floor + hatch; the rewritten doc at :201-203),
  route_policy.go (doc), sessions.go (the /auth/sessions DTO doc), host_policy.go}`; `{config.go (three fields, three
  mirrors), configuration.go (boot lines §2, §4), constructor.go, configuration_errors.go, security.go (the waiver),
  README.md}`; `stores/storetest/{session_activity.go (new: sub-suite SessionActivity skipped when the interface is absent —
  stamp on RotateAt, no stamp on ConsumeGrace, LegacyRotatedRowGrandfathered, DeleteIfIdle CAS true/false, turso encoding
  round-trip, probe ErrNotFound on an unmigrated schema), storetest.go}`; tests: `logic/authentication/{idle_timeout_test.go
  (new: the grandfather rule, reuse precedence, first-party only, the event on rows-affected = 1 only), external_age_test.go
  (new), refresh_test.go}`, `inbound/http/{liveness_floor_test.go (new: an override lowered to Stateless now reads; the hatch;
  the fail-open 503 case), me_test.go, sessions_test.go}`, `securityevent/securityevent_test.go:135`, `config_env_test.go`,
  `configuration_hardening_test.go`, `posture_test.go`. **auth-cms (go.work CI):** `examples/auth-cms/pockets/authentication/
  outbound/authmem/{authmem.go (sessionRepo gains the four methods), authmem_test.go}`; `examples/auth-cms/cmd/server/main.go`
  (`AUTH_IDLE_TIMEOUT` passes through; nothing else), `examples/auth-cms/.env.example`;
  `examples/production-baseline/cmd/server/production_test.go` (the zero-WARN assertion admits the new
  `external_authentication_age=unbounded` WARN until PR-3 sets `MaxAuthenticationAge` on the baseline's provider and
  restores it — added at execution). **Verify:** `cd pockets/authentication
  && go build ./... && go test -race ./... && go vet ./...`; `cd examples/auth-cms && go test ./...`; `make guard`; `make check`
  (stores still build: no port method changed); Verification §1.
- **PR-2 — stores: 0029, `ActivityRepository`, repin (`authentication/s4d2-stores`).** **Depends on** tag 1. **BREAKING:**
  rows 8–9. **Tag:** `pockets/authentication/stores/pgx/<next minor>`, `…/turso/<next minor>`. **Files:** `pockets/
  authentication/stores/{pgx,turso}/{migrations/0029_session_activity.sql, session_activity.go (new: the wired-only column
  list, GetByRefreshHashWithActivity, RotateAt, DeleteIfIdle, ProbeSchema — pool path, the SRE9 comment), sessions.go
  (unchanged SQL; doc), postgres.go|turso.go (0029 named in the "never probed" doc), schema_probe_test.go (0029 absent from
  probeColumns), go.mod, go.sum (core → v0.32.0 by explicit `GOWORK=off go get`, then `GOWORK=off go mod tidy`), README.md,
  conformance tests}`; `examples/production-baseline/workshop/migrations/primary/<next free>_session_activity.sql` (G34,
  byte-identical). **Verify:** `make check`; `make guard` (G34, G35); live legs on the branch — pgx bare + `POSTGRES_TEST_
  SCHEMA=s4d2` on a fresh PG17 (`lsof` first), the turso gate on a fresh libSQL (or the recorded local-file waiver), a
  `live-stores` dispatch; the SRE14 leg list (Verification §2) recorded in the PR.
- **PR-3 — the record (`docs/s4d2-release-record`).** **Depends on** tags 1–3 cold-verified and §3. **Files:** `RELEASING.md`
  (ONE section "Session lifetime (SOC 2 slice S4d part 2)": tags, the Upgrade notes by host shape, the 0029 rule, the
  grandfather rule, the verification record, the as-built deviations); every `examples/*/go.mod` pinning a moved module
  (explicit `GOWORK=off go get`: auth-cms core v0.32.0; baseline core v0.32.0 + pgx next minor; external-identity core
  v0.32.0); `examples/production-baseline/.env.example`, `examples/external-identity/.env.example`; `SECURITY.md`;
  `workshop/documentation/docs/{pockets/authentication.md ("Added by S4d part 2"), guides/production-posture.md,
  guides/keys-and-rotation.md, guides/operations-runbooks.md}`; `control-map.md`; `roadmap.md` S4 line; `NEXT.md` (the
  R-1 deferral with the full fifteen-site inventory and the nested-check case); `plans/s4d2-session-lifetime.md` (executed
  copy). **Verify:** `make check`; `make guard`; `GOWORK=off go build ./... && go test ./...` per repinned example; `pnpm
  build` in `workshop/documentation`.

## Sequencing
```
PR-1 ─[live-stores dispatch on main]─[tag 1: authentication v0.32.0]─ PR-2 (repin + 0029 + ActivityRepository) ─[live legs]─[tag 2: pgx + turso next minor]
                                                   tags 1–2 cold-verified + §3 ─ PR-3 (record + example repins)
```
Two trains is the floor: set A holds because no existing port changes; views/goth needs no repin (the page model is
untouched; MVS lifts its core). If S4c-2 is executing concurrently, PR-2 takes the ledger number and store minors at merge
time (BE6).

## Release
| Step | Tag | After | Cold verification (MVS) |
|---|---|---|---|
| 1 | `pockets/authentication/v0.32.0` | PR-1 merge + dispatch | **set A:** core v0.32.0 beside RELEASED pgx v0.15.0 / turso v0.14.0 AND views v0.9.0 selects sdk v0.20.0 and builds; a constructor run with `IdleTimeout` set against pgx v0.15.0 → `ErrIdleTimeoutRequires` (no lockstep) |
| 2 | `…/stores/pgx/<next minor>`, `…/stores/turso/<next minor>` | PR-2 + live legs on `main` | **set B:** both stores + core v0.32.0 + pgxdb v0.14.0 + datastores-turso v0.9.0 select sdk v0.20.0 and build; a gps-three-sixty-shaped `main` (core + pgx store + goredis v0.6.0) builds; **set C:** views v0.9.0 + ui/goth v0.2.1 + rsc-qr v0.1.0 + go-webauthn v0.1.0 + set B build an auth-cms-shaped `main` (no views tag) |

**Tagging.** Three annotated, unsigned tags, each cut at its squash commit, pushed by the owner, THEN polled on `.info`;
cold-verify with `GOWORK=off`, an empty `GOMODCACHE`, `proxy.golang.org` + `sum.golang.org`, `go mod verify`, `go build
<module>/...` (the consumer pins `go 1.26.10`). Never tidy before a tag resolves; never `go work sync`. **Not tagged:** sdk,
views/goth, `pockets`, workshop, ui/goth, pgxdb (R-1 (b) is its own train), the examples.

## Verification
Real binaries in DEVELOPMENT on a FREE port (`lsof -i :8095` first; never 8082 or 55432). `$B` = a scratch auth-cms binary
(`PORT=8095 AUTH_RUNTIME_MODE=development AUTH_DEBUG=1 AUTH_ACCESS_TOKEN_TTL=1m AUTH_IDLE_TIMEOUT=3m go run ./cmd/server`,
authmem, console mailer); `$P` = production-baseline on a fresh PG17 with the baseline ledger; events read from
`GET /auth/admin/security-events` as a platform admin.
- **§1 — PR-1, the real binary over HTTP.** (1) Boot line `auth: session lifetime access_token_ttl=1m refresh_ttl=168h
  idle_timeout=3m idle_source=config activity=refresh_rotation idle_scope=first_party live_floor=enforced` plus the
  `idle_granularity` WARN (3 m < 2 × 1 m is false — use `AUTH_IDLE_TIMEOUT=90s` for a second boot to see it); a binary with
  `AUTH_IDLE_TIMEOUT=30s` refuses naming `AccessTokenTTL`; `AUTH_ACCESS_TOKEN_TTL=200h` refuses naming `RefreshTTL`;
  `AUTH_RUNTIME_MODE=production`-shaped constructor test: `AccessTokenTTL=2h` boots with `posture_warning=access_token_ttl_long`.
  (2) Sign in (cookie + bearer); refresh at 2 m → **200** (rotated, stamped); wait 3 m, refresh → **401** generic, the row
  gone, `session_idle_expired {last_activity, idle_seconds≥180, idle_timeout_seconds:180, surface:refresh}` exactly once; a
  replay of the pre-idle consumed slot on a second session → `refresh_reuse`, never `session_idle_expired`; a session
  created, never refreshed, older than 3 m → **401** on first refresh (judged on `created_at`); a delegated connection's
  refresh after 3 m → **200** (out of scope); logout by refresh token of an idle session → **204**. (3) A binary with
  `WithRouteAuthentication(SessionHydration: PrincipalStrategy())` → `/auth/me` after `RevokeUserSession` → **401** at once (the
  floor); with `BundledRouteAuthLivenessFloorRelaxed` → **200** until the token expires + the WARN; an external-token fixture
  with `RevocationFailOpen` and an outage → **503** on `/auth/me` under the floor, **200** relaxed. (4) A dev external provider
  with `MaxAuthenticationAge: 10m`: a token with `auth_time` 11 m old → **401** + `external_subject_denied reason=
  authentication_age_exceeded provider=…`; 9 m → **200**; no `auth_time`/`iat` → **401** `reason=authenticated_at_missing`; the
  boot line names `max_authentication_age=10m revocation_checker=none`; a production-shaped constructor with no checker and
  no bound logs the WARN; `RevocationFailOpen` in production logs `posture_waiver=RevocationFailOpen`. (5) No response, row
  or log line carries a token, hash or address.
- **§2 — PR-2, the store legs (SRE14).** On pgx, turso, `TestReference`, authmem: (1) unwired + unmigrated boots; (2) wired
  + unmigrated refuses naming 0029; (3) the column in a decoy schema still refuses (`schema_decoy_test.go`); (4) a probe
  permission/timeout error → `sdk.ErrUnavailable`, not schema-missing; (5) migrated + unwired: `Rotate` leaves the column
  NULL; (6) an old binary (core v0.31.0 + pgx v0.15.0 / turso v0.14.0) on a migrated schema: login and refresh work; (7) the
  grandfather rule and the `DeleteIfIdle` race (two concurrent refreshes on an idle row → one deletion, one event, one
  `reResolve`); (8) delegated rows are never judged; (9) turso `last_rotated_at` text encoding round-trips and orders with
  `created_at`; (10) 0029 under a held `ACCESS SHARE` lock → the 5 s `lock_timeout` error, re-run succeeds; *(Corrected at
  execution: the `$P`-shaped idle boot legs were dropped — production-baseline is external-only and wires no
  `SessionsConfig`, so `AUTH_IDLE_TIMEOUT` is not read there. The wired + unmigrated refusal naming 0029 is covered by
  the pgx live pocket test; `$P` was booted on a fresh PG17 with the ledger applied through 0035 instead.)*
- **§3 — from the tags (PR-3).** §1(2) against a `GOWORK=off` auth-cms binary (`go version -m`: core v0.32.0); `$P` boots
  from the tags with the ledger applied: a development-issuer token whose `auth_time` is older than the provider's
  `MaxAuthenticationAge` (+ the 60 s leeway) → **401** + `external_subject_denied reason=authentication_age_exceeded` (the
  bound the part-1 record promised). *(Corrected at execution: the draft's `$P` idle leg with `AUTH_IDLE_TIMEOUT=3m` was
  dropped — the baseline is external-only, no native sessions.)*

## Open rulings — YOUR CALL
- **R-1 — `(*pgxdb.DB).QuerierFrom` cross-instance defect** (`integrations/datastores/pgxdb/transact.go:66`; S5 R-2 deferred
  it to a plan that no longer exists). (a) Fold as its own PR in this slice (pgxdb v0.15.0: `Tx` records its owner, a foreign
  `QuerierFrom` returns an erroring Querier `ErrForeignTransaction` — never silently the pool —, instance-aware `(d *DB)
  TxFrom`, instance-aware nested check; then the same on `datastores/turso`; the fifteen authorization store sites switch when
  those stores next tag). (b) The same fix as a separate small PR outside the slice, with an owner and a date in `NEXT.md`.
  (c) Leave deferred. **Recommend (b):** a pgxdb tag would force the authorization stores to repin (breaking "one store-repin
  PR per slice") and add a third moving module to set B for a defect no adopting host has (gps-three-sixty: one `*DB`;
  battleboard: one pooled `*DB`, tenant via `set_config` inside `Transact`); the inventory is larger than S5 recorded (BE11),
  so it deserves its own review. (c) is the worst option: it already vanished once.
- **R-2 — the liveness floor (§3).** (a) Raise the floor to `Live()` on the five sensitive groups with the
  `BundledRouteAuthLivenessFloorRelaxed` hatch (BREAKING row 4; the fail-open interaction named). (b) Docs only: the
  `BundledRouteAuthentication` doc says an override must restate `Live()`. **Recommend (a):** overrides are opaque middleware, so
  no WARN can detect a lowered tier; (b) leaves M4's "Live tier default on sensitive reads" satisfiable by accident only; the
  default postures pay nothing and the hatch is one field.
- **R-3 — the access-token ceiling.** (a) Production `AccessTokenTTL > 1 h` → one WARN, no acknowledgement; `> RefreshTTL`
  refuses in every mode. (b) Production `> 1 h` refuses unless `AccessTokenTTLAcknowledged` (a `posture_waiver`).
  **Recommend (a):** the owner rule is WARN unless argued, and the argument is weak — the window is already bounded by refresh
  and by the Live tier on every sensitive bundled read; (b) turns a minor core bump into a boot outage for a host at 2 h.
- **R-4 — `aal`/`methods` on delegated tokens.** (a) Carry to part 3 with the four constraints (atomic `CreateCodeWith
  Authentication` over the locked approving row; verifiers upgrade before issuers; `assure` newly admits delegated tokens
  under `RequireMFA`; a login-time level stales for the grant's life — M7 claims design). (b) Build it here: migration 0030
  on `oauth_authorization_codes`, a second conditional probe, a second ledger copy, wired by `OAuth2Config.DelegatedAssurance`.
  **Recommend (a):** both reviewers; (b) adds a second wired-but-unmigrated state and a rollout order the slice cannot enforce,
  and the stale-claim question belongs with M7.
- **R-5 — the `iat` residual on `MaxAuthenticationAge`.** (a) Document it (per-provider table; "request `auth_time` with
  `max_age`"). (b) Add `sdk/capabilities/authentication.Identity.AuthTimeAsserted bool` (sdk v0.21.0, set by go-oidc/
  firebase/auth0) and deny `iat`-only identities under a bound — a separate sdk + three-adapter train. **Recommend (a) for this
  slice** and (b) as a part-3 item: no sdk tag here, and Firebase (the IdP shape the baseline wires) asserts `auth_time`.

## Open questions
- Battleboard: are any `authentication` tables under a tenant RLS policy, and is its `set_config` transaction-local
  (`true`)? If `sessions` is under a policy, refresh already fails on `main`; a session-level `false` leaks the GUC through
  the pool (pre-existing, not part 2). Does the policy deny on an unset tenant (which makes R-1 (b) safe for it)?
- gps-three-sixty / battleboard SPAs: refresh on a timer or on 401? A timer makes idle inert (BE5); the record should say
  which.
- Should idle expiry on refresh stay the generic 401 (the pinned contract; an SPA cannot say "signed out for inactivity"), or
  gain a distinct code? The plan keeps the generic 401; a code would be a contract change for every refresh client.
- Whether any host implements `session.SessionRepository` outside this repo (none here beyond `authmem`).

## Recommended reviews
**product-manager:** R-2 and R-4 before ratification; the host-shape defaults paragraph as the adopter-facing contract.
**platform-sre:** re-read §1's grandfather + CAS rules and the §2 leg list as drafted; the 0029 `lock_timeout` runbook.
**data-integration-reviewer:** `DeleteIfIdle`'s `IS NOT DISTINCT FROM` on both dialects; the turso text encoding; the
wired-only column list beside the unchanged `sessionColumns`. **architecture-steward:** the optional interface on
`Repositories.Sessions` vs a new slot; the floor living in `withDefaults` with the hatch on `BrowserConfig`.

## Notes
- **Part 3 handoff (M7, M8, R-4, R-5):** key-ring JWT signer with `kid` in `integrations/cryptids/golang-jwt`, `KeyedEncrypter`
  for delivery and provider-token keys, `iss`/`aud` on first-party tokens; breach checker + production WARN; escalating
  lockout; `aal`/`methods` on delegated tokens under the four constraints; `Identity.AuthTimeAsserted` (sdk minor). **S4c-2:**
  0028; its ledger copy lands after this slice's (0036+ if part 2 is first).
- **R-1 (b) spec, for its own PR:** `Tx.db *DB` set by `InTx`/`TransactSnapshot`; `QuerierFrom` on a foreign owner returns
  `foreignTxQuerier{err: ErrForeignTransaction}` (every call errors; nothing runs on the wrong connection); `(d *DB) TxFrom(ctx)
  (*Tx, bool)` returns `false` for a foreign owner; `Transact`'s nested check compares owners; `TxFromContext` kept and
  documented instance-blind; the same on `datastores/turso/transact.go:47`; the seven pgx + eight turso authorization sites
  switch to `TxFrom` when those stores next tag.
- **Follow-ups:** bounded deadlines and `ErrNotFound`-only mapping for the EXISTING 0025/0026 probes (`constructor.go:278-304`,
  still `context.Background()`); an `/auth/sessions` idle marker (a third optional method); a distinct idle refresh code if
  the owner wants it; a pre-expiry signal for idle (none exists; the event is post-hoc).
- **Cut for the cap:** per-test case lists beyond the suites; DTO tables (the goldens are the contract: `/auth/sessions`
  DTO doc only); the SQL of the turso twin beyond its encoding rule; the per-provider age table's prose (README writes it).
