# S2 follow-ups — the Known list, cleaned up (SOC 2 readiness, slice 2 tail)

**Status:** RELEASED 2026-10-23 — four tags published annotated at their merge commits and
cold-verified on the first poll (record: RELEASING.md "S2 follow-ups"; handoff
`plans/soc2-s2-audit-completeness-handoff.md`, updated under the maintenance rule; manifest
`plans/s2-follow-ups-release-manifest.json`; this file copied by PR-4 = #161). Squash-merged to
`main` in this order: PR-2d #155 @ 39c38bff; PR-1 #156 @ f7b3ec9f (step 1: secrets
`stores/pgx/v0.2.1`); PR-3 #157 @ 9b51e10f (step 3: workshop/gopernicus v0.6.1, tagged as the
next step after the merge); PR-2b #158 @ 7e26db9e (step 2b: authentication `stores/pgx/v0.10.1`,
core pin v0.21.0; the Linux-container `make test-stores` and the branch dispatch, run
40002504439, green before the merge); PR-2a #159 @ 1cf16e8b; PR-2c #160 @ c9cc0a9b (step 2:
authentication v0.22.0, cut at the later of #155/#158/#159/#160; no store retagged). Live store
legs ran on merged `main` before every pocket and store tag. Build notes are at the end of this
file. History: RATIFIED 2026-10-23 (owner: "ratified"; rev 2 as reviewed; R-A = (a) keep the script, BENCHMARKS recipe reworded "historical, owned runner"; R-B = (a) no adoption exception). Build started from `main` @ 019e8a36 the same day; worktrees `../gopernicus-fu-<pr>`. DRAFT rev 2 — review gate run (four reviews, folded).
Rev 1 (2026-10-23, `lead-backend-engineer` consulted once, eleven points folded) is kept verbatim
beside this file as `s2-follow-ups.rev1.md`. Rev 2 (same day) re-anchors on `main` @ 019e8a36 —
the Firestore retirement is COMPLETE (PR-A #153 @ afd3dc9f, PR-B #154 @ 019e8a36), so the
"waits for PR-B" precondition is met — and folds the four reviews (`product-manager`,
`platform-sre` (two failure modes confirmed by probe, not reasoning), `architecture-steward`,
`data-integration-reviewer`) under the lead's fifteen rulings; every reviewer finding is accepted
except where a ruling decides between two reviewers, and each folded change is attributed in the
"Review gate record" under Notes. Owner direction: "unsure here, but clean it up." Parent:
`roadmap.md` (S2 RELEASED line `:292-294`, "Follow-ups:"); record: `RELEASING.md` "Audit
completeness and integrity" **Known** (`:339-359` on 019e8a36); sources: PR bodies #141, #147,
#148 (Findings 4-5), #151; `retire-firestore-stores.md` ruling 7; the `live-stores`
`postgres-turso` failure of 2026-10-23 (run 39990855657, `RotationKeepsExpiresAt`, first noted
2026-08-15); the S8 toolchain PR #124 (`live-stores.yml` `GOCACHE`). Every anchor below was
verified against 019e8a36; line numbers are anchors for the implementer to re-check, never
instructions to cut by number.

## Context

S2 shipped fourteen tags and left ten small things on the table, each recorded where it was
found: a lock-order race in the secrets pgx store (`DeleteOwner` surfaces a concurrent `PutSecret`
as `sdk.ErrInvalidReference`, not the `sdk.ErrConflict` the service retries — and, found by the
review gate, the same lock order can deadlock against a resealing `ReplaceOwnerKey`); nine
authentication denial paths that still record nothing; bridged `authorization_denied` rows with
empty `ip_address`/`user_agent` because only the authentication pocket's own routes carry the
client-info context; the R6 `errors.ErrUnsupported` probes in the authentication conformance suite
that existed only for the retired Firestore adapter; a scaffold README that still runs
`guard@v0.5.0`; a root README that says "twenty-three layering guards"; an orphaned CI helper
nothing runs; an owner rule about host handoffs that is not yet written down; and two CI items:
the `postgres-turso` job's `RotationKeepsExpiresAt` failure (a platform-dependent assertion over a
store that reports what it was given, not a Postgres flake — Design, item 9) and a leaked local
temp path in `live-stores.yml`'s `GOCACHE` override (item 10).

The review gate added one defect this train must not make worse and one it must close: the
pgx `security_events` store loses a row whenever a `TEXT` column receives invalid UTF-8 (SQLSTATE
22021, confirmed against `postgres:17`) or the JSONB `details` receives `\u0000` (22P05) — a
non-UTF-8 `User-Agent` reaches the handler unchanged (confirmed with an httptest probe), so a
crafted UA already loses failed-login rows on the pocket's own routes today, and mounting a global
client-info stamp would extend that loss to every host-route row that survives today only because
its UA is blank. Rev 2 therefore bounds every attributed value at the ONE recording site (Design,
item 2b) before it stamps host routes (item 3), and budgets the unauthenticated failure rows the new
denial paths open (Design, flood posture).

This plan is one PR train — seven PRs, four tags — sequenced after the Firestore retirement, which
is now complete. Nothing here relitigates S2: migrations are untouched, the three audit rails keep
their shape, the ONE-recorder rule holds, and Details still never carry a secret, an address, a
code or a token.

## Goal

Every S2 **Known** item is either closed by a tagged release or restated as a documented boundary;
no new migration, no port signature change, the `live-stores` dispatch is green on Linux for the
authentication pgx legs, and the four tags cold-verify on the first poll.

## Definition of Done

- `pockets/secrets/stores/pgx` `DeleteOwner` locks the owner key before it deletes anything; the
  two-connection live tests (versus `PutSecret`, versus `ReplaceOwnerKey`) wait on a
  `pg_blocking_pids` barrier, return `sdk.ErrConflict` / nil (never `sdk.ErrInvalidReference`,
  never 40P01), bite on the pre-fix code with the DSN set, and are stable at `-count=20`.
- Every path in the S2 Known list records one row through the ONE recorder with a bounded reason,
  the `assertNoSecretInDetails` sweep covers each new case, `TestReasonsAreStableWireValues` pins
  the six new reasons, and every `oauth2_grant_failed` row and the callback `unknown_provider` row
  record only under a per-IP failure-row budget (over budget: no row, a counter, one WARN per minute).
- The Recorder bounds every attributed value through ONE exported helper (NUL, invalid UTF-8 or a
  control byte ⇒ blank; UA cut at 512 bytes on a rune boundary; IP blank unless `netip.ParseAddr`
  accepts it; Details ids blank over 2048 bytes); a pgx live case records a `%00` client id, a 4 KB
  id and a `\xff` UA through the Recorder and the pgx store, each row exists with the field blank and
  `WriteFailures() == 0`.
- A denied `Require` gate on a host route produces a `security_events` row with non-empty
  `ip_address` and `user_agent` in both examples, proven by a router-level test and by run-and-look
  (including `curl -A $'\xff'` on the baseline: the row exists, UA blank); a production-mode
  `NewAuthenticator` adapter carries a real proxy posture; a row with a request id and no client
  attribution logs one production WARN per process naming `ClientInfoMiddleware`.
- `pockets/authentication/stores/storetest` has no `ErrUnsupported` probe; the five
  terminal-settlement subtests print `--- PASS` in pgx (bare + schema), turso (local libsql and
  `LocalSQLite`), `authmem` and `TestReference`.
- `README.md` says thirty-three guards; the scaffold README, the docs site and the workshop README
  name the current tool version; `.github/scripts` is resolved per R-A.
- `pockets/authentication/stores/pgx` returns the session as persisted (microsecond timestamps) at
  all four insert sites through one helper; `RotationKeepsExpiresAt` and `ActiveMintRoundTrip` use a
  750 ns fixture clock that fails against the old store on every platform and passes against the new
  one; the session port docs state the contract; a pre-merge `live-stores` dispatch on the PR-2b
  branch shows the authentication pgx bare and schema legs green on Linux with no `GOCACHE` override
  in the workflow.
- `pockets/secrets/stores/pgx/v0.2.1`, `pockets/authentication/stores/pgx/v0.10.1`,
  `pockets/authentication/v0.22.0`, `workshop/gopernicus/v0.6.1` are published annotated at their
  merge commits, cold-verified, and recorded in `RELEASING.md`; the S2 handoff is updated per the
  maintenance rule and `roadmap.md` carries it.

## Out of scope

- Any host repository. The maintenance rule (item 8) says handoffs are written and updated per
  slice and applied once the SOC 2 roadmap is complete: no host repository is touched; the in-repo
  S2 handoff IS updated per the rule (PR-4), including the `ClientInfoMiddleware` recipe. (R-B asks
  whether security/integrity patches are an exception.)
- A new migration, a store schema change, or a port signature change on any pocket. The secrets
  memory store (`pockets/secrets/stores/memory/memory.go:184-225`) holds one mutex for the whole
  `DeleteOwner`; it has no window and is not touched. The session port gains a doc sentence, not a
  signature.
- Retagging `pockets/authentication/stores/turso` (v0.9.0): its code does not change (it stores
  nine fractional digits, Design item 9). Both stores keep core v0.21.0; `stores/pgx` moves only for
  its own session fix (PR-2b). The storetest changes ride the core tag (storetest has no `go.mod`;
  it is a package of `pockets/authentication`).
- Loosening the `RotationKeepsExpiresAt` assertion, or truncating in `session.NewSession` (the
  domain clock stays nanosecond; precision is the store's fact, not the entity's). Reading `before`
  back through `Get` instead of fixing the store was rejected (Decided by the review gate, 5).
- The OAuth2 **authorize** endpoint's validation (`oauth2/authorization.go:147-165`, `readConsent`
  `:88-106`): `oauth2_grant_failed` is a token-endpoint type and a browser-lane `ErrInvalidRequest`
  renders a page under the user's session; it is not a grant and is not recorded.
- A rate limit on the OAuth callback route (`inbound/http/oauth.go:55`, no limiter today): the
  failure-row budget bounds the rows, not the requests; the route limit is filed under Known as a
  follow-up (SRE-F2).
- Fixing the stale `owner_key_destroyed` key id (the service reads `key.KeyID` outside the
  transaction, `logic/vault/service.go:160-183`; a rotation committing in between leaves the
  destruction record naming the rotated-away key): a secrets CORE change for a later minor; under
  Known (data-11).
- Applying the session-precision helper's rule to `Profile` defaulting (pgx `sessions.go:233-236`):
  the port sentence is narrowed to timestamps (data, "what as persisted covers").
- `examples/external-identity` (pins authentication v0.19.1, mounts no authorization routes): no repin.
- A `tool` pin or `guard:` target emitted by `init` (workshop v0.4.0/v0.4.1 Known, still deferred).
- Editing ratified or executed plan text, dated `RELEASING.md`/`AUDIT.md` sections, or
  `retire-firestore-stores.md`. The dated v0.18.0 cutover note at `pockets/authentication/README.md:772`
  ("Firestore store returns `errors.ErrUnsupported`") is history and stays (arch-8).

## Design

### Item 1 — secrets `DeleteOwner` locks the owner key first (store-only)

`pockets/secrets/stores/pgx/pgx.go:320-350` runs, in one transaction, `DELETE FROM secrets_values
… RETURNING name, version, key_id` (`deleteOwnerSecrets`, `:352-370`) and then `DELETE FROM
secrets_owner_keys`. A concurrent `PutSecret` (`INSERT … ON CONFLICT DO UPDATE`; its FK check takes
`FOR KEY SHARE` on the owner-key row, `migrations/0001_secrets.sql:31-32`) that commits between the
two statements makes the owner-key `DELETE` fail its FK → SQLSTATE 23503 → `pgxdb.MapError` →
`sdk.ErrInvalidReference`. Nothing is deleted (the transaction rolls back), but the service's retry
loop (`logic/vault/service.go:145-155`) retries only `sdk.ErrConflict`, so the caller sees the wrong
sentinel. **A second defect in the same order (data-10):** today's order (values, then parent) can
deadlock with a resealing `ReplaceOwnerKey` (`pgx.go:201-233`: an `UPDATE` on the owner key — `FOR NO
KEY UPDATE` strength — then the values rows). That surfaces as 40P01, which `MapError` does not map
and the service does not retry.

**Fix.** The first statement becomes `SELECT key_id FROM <secrets_owner_keys> WHERE owner_type = $1
AND owner_id = $2 FOR UPDATE` (precedent: the `FOR UPDATE` read in `ReplaceOwnerKey`, `pgx.go:211-213`),
scanned through `pgxdb.MapError` as `scanOwnerKey` does (`pgx.go:513-520`; `Tx.QueryRow` does not
map, `pgxdb/tx.go:80-82`, so a bare `Scan` would return raw `pgx.ErrNoRows`). No row →
`sdk.ErrNotFound` before any delete. `FOR UPDATE` is the only row-lock mode that conflicts with
`FOR KEY SHARE` (`FOR NO KEY UPDATE` would not: the Put's FK check would proceed and the parent
`DELETE` would still see 23503). A Put whose FK check is in flight blocks the lock, commits first,
and lands in the `DELETE … RETURNING` snapshot: the returned set differs from `expected` →
`sdk.ErrConflict` → the service relists and retries. A Put that arrives after the lock blocks on the
owner-key row until `DeleteOwner` commits, then fails its own FK — `sdk.ErrInvalidReference`,
correctly: the owner is gone. Both `DeleteOwner` and `ReplaceOwnerKey` now lock parent-then-values,
so the deadlock is gone. The outcome holds under the default **READ COMMITTED** (`pgxdb.InTx` uses
`pool.Begin`, the server default); under `default_transaction_isolation = 'repeatable read'` the
race returns 40001 instead, which `MapError` passes through and the service does not retry — under
Known (data-9). `DeleteOwner` now holds the owner-key lock for its whole transaction; there is no
`lock_timeout`, so a request-path caller should carry a deadline, as today's `DELETE` waits already
require (SRE-F17). The port contract (`logic/vault/vault.go:115-141`) is unchanged; no core tag.

**Proof** is two two-connection live tests in `stores/pgx/concurrency_test.go` (package `pgx`,
beside `TestLive_ConcurrentReplaceOwnerKey`), not storetest cases: a race is not expressible
deterministically through the port, and the conformance suite is single-connection. Both use the
same harness (data-2, SRE-F4):

- Connection A opens a transaction and registers its rollback with `t.Cleanup` **after**
  `openAndMigrate` so (LIFO) it runs before the truncate cleanup — otherwise a failed assertion
  leaves A open and `TRUNCATE` (ACCESS EXCLUSIVE) hangs the package until `go test` times out.
- A goroutine calls the store under a 10 s context; the test does NOT sleep: it reads A's
  `pg_backend_pid()` and polls `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY
  (pg_blocking_pids(pid)))` under the same context until the goroutine's backend is blocked on A,
  then commits A. "Still running after 200 ms" proved nothing: on a loaded runner the goroutine may
  reach its first statement after A commits, and then the PRE-fix code also returns `ErrConflict`
  (it sees `c`, deletes three rows against two expected, rolls back) — the bite run would false-green.
  Optionally the barrier asserts which statement is blocked (post-fix: `SELECT key_id …`; pre-fix:
  `DELETE FROM … secrets_owner_keys`).
- Every SQL the harness issues goes through `repo.table(...)` (`pgx.go:182`) so the
  `POSTGRES_TEST_SCHEMA` leg hits the schema-qualified tables.

`TestLive_DeleteOwnerVersusPutSecret`: owner `race` with secrets `a`,`b`; A `BEGIN` + `INSERT INTO
<secrets_values>` for a third name `c` (holds `FOR KEY SHARE`); the goroutine calls
`repo.DeleteOwner(ctx, race, []string{"a","b"}, records)`; barrier; A `COMMIT`; the goroutine returns
`errors.Is(err, sdk.ErrConflict)` and NOT `sdk.ErrInvalidReference`; the owner key, `a`, `b`, `c`
and zero audit rows remain (`requireOwnerIntact` shape, `storetest.go:520`). A service-level
assertion follows (SRE-F4): `vault.Service.DeleteOwner` over the same repository, driven through the
same barrier, returns nil via its relist-and-retry — the end-to-end behavior the Known entry was about.
`TestLive_DeleteOwnerVersusRotation` (data-10): A `BEGIN; UPDATE <secrets_owner_keys>` (no PK
change) on `race`; the goroutine's `DeleteOwner` blocks on A (barrier); A `UPDATE <secrets_values>`
row `a`; `COMMIT`; `DeleteOwner` returns nil, never 40P01. On the pre-fix code the first harness
returns `ErrInvalidReference` and the second deadlocks (an unmapped error) — run both once against
`main` WITH `POSTGRES_TEST_DSN` set (without it `requireDSN`, `conformance_test.go:60-66`, skips and
the "failure" would be a skip — data-3a) before the fix lands to prove they bite; the output goes in
the PR. Post-fix both run at `-count=20` in the bare and schema legs.

### Item 2 — the unrecorded authentication denial paths (core MINOR)

Every row goes through the existing helpers (`logic/authentication/securityevent.go:36-77`
`recordDenial` / `recordCredentialMutationDenial`; `oauth.go:670-684` `recordOAuth`;
`oauth2/audit.go:33-69` `auditGrantFailure` / `auditReason`). Six bounded reasons are added to
`securityevent/securityevent.go:312-351` and `TestReasonsAreStableWireValues`
(`securityevent_test.go:151`): `precondition`, `claim_conflict`, `unknown_provider`,
`invalid_request`, `invalid_scope`, `invalid_target`. No new event type. The coverage, per site:

| site | row | notes |
|---|---|---|
| `SetPassword`, `password.go:57` `ErrPasswordAlreadySet` | `password_set` failure `precondition` | the account's credential state refused the op before any grant was spent |
| `SetPassword` `password.go:76`, `ChangePassword` `service.go:836` — `passwords.Change` → `sdk.ErrConflict` (`user/repository.go:74-76`) | `password_set` / `password_change` failure `revision_conflict` | existing reason; the proof is spent |
| `RemovePassword` `password.go:184` `ErrPasswordNotSet` | `password_removed` failure `precondition` | |
| `RemovePassword` `password.go:188` — `credentialRemovalProof` returns `session.ErrUserNotActive` (`:113-115`) | `password_removed` blocked `inactive` | recorded in `RemovePassword`, NOT inside the helper: it is shared with OAuth unlink (`oauth.go:500,531`) and `StartRemovePassword` (`password.go:149`) |
| `StartIdentifierChange` `identifier_management.go:149`, `RemoveIdentifier` `:303`, `SetIdentifierUses` `:345` — refused `RequireRecentAuthentication` (`ErrStepUpRequired` / `ErrDelegatedCredential`, the same pair `SetPassword` records at `password.go:66-70`) | `email_changed`/`phone_changed` (`changedEvent(kind)`), `email_removed`/`phone_removed` (`removedEvent`), `identifier_uses_changed` — failure `invalid_token` | the mutation's own type, as `password_set` does |
| `ConfirmIdentifierChange` → `applyVerifiedIdentifierChange` `:390-391` → `identifiers.ApplyVerifiedChange` returns `sdk.ErrAlreadyExists` (the claim race, by design arbitrated only at confirm) | `email_changed`/`phone_changed` failure `claim_conflict` | one new branch in `recordCredentialMutationDenial` (`securityevent.go:65-77`); unreachable from the other callers |
| `OAuthCallback` `oauth.go:294-297` unknown provider | `oauth_login` failure `unknown_provider`, no `provider` key, no user — **budgeted** (flood posture) | pass an EMPTY provider name — `recordOAuth` omits the key (`:672`); the literal `"unknown"` is forbidden because providers are keyed by the host's own `Name()` (`service.go:285-291`) and a real one could be called that. The signal: at the callback an unknown provider is a flow in flight when a provider was removed, or a forged callback — actionable (PM-7) |
| `start` `oauth.go:204-207` with `linkUserID != ""` (`StartLink`) | `oauth_linked` failure `unknown_provider`, `UserID` = the linker | authenticated, bounded by the session; not budgeted |
| `start` for a login (`StartOAuth`, unauthenticated) | **no row** | no credential presented, no actor, no provider resolved — nothing to attribute; equivalent to a 404 on any other path (R2 = (a), Decided by the review gate 10). The logic comment stays transport-free; "the start route has no rate limit (`inbound/http/oauth.go:54`)" is an inbound fact and goes in the README/RELEASING sentence only (arch-7) |
| token endpoint: `RedeemCode`, `Refresh`, `Exchange`, `Introspect`, `Revoke` returning `ErrInvalidRequest` / `ErrInvalidScope` / `ErrInvalidTarget` | `oauth2_grant_failed` failure `invalid_request` / `invalid_scope` / `invalid_target`, Details `client_id` (see bounding) — **budgeted** | `auditGrantFailure` gains the three branches; the reasons mirror the wire codes `writeOAuth2Error` emits (`inbound/http/oauth2.go:382-398`), as `invalid_grant`/`invalid_client` already do; `TestOAuth2MalformedRequestsRecordNothing` (`denial_audit_test.go:260`) becomes `…RecordInvalidRequest` |
| the confidential-client secret mismatch | `oauth2_grant_failed` failure `invalid_client` — **budgeted** | the sibling below |

**The confidential-client mismatch — a ctx-taking sibling, not an inbound hook.** The oauth2
service already holds the recorder (`oauth2/service.go:33`) and the Recorder is the single recording
site; the inbound `handlers` struct holds none, and a hook on `OAuth2Config` would move audit policy
into inbound. So: `func (s *Service) AuthenticateClientContext(ctx context.Context, clientID, secret
string) (ClientProof, error)` — the comparison of `AuthenticateClient` (`oauth2/service.go:121-131`),
plus `s.auditGrantFailure(ctx, <id>, ErrInvalidClient)` on mismatch. **Deprecation shape (arch-4,
ruling 9):** `AuthenticateClient` stays (`stores/turso/oauth2_service_test.go:191` calls it and that
store stays pinned to v0.21.0) and DELEGATES — `return s.AuthenticateClientContext(context.Background(),
clientID, secret)` — so the domain has ONE constant-time comparison site. Its `Deprecated:` text says
honestly what the Background path does: *"Deprecated: use AuthenticateClientContext. This path
records a mismatch as an oauth2_grant_failed row with no request id, IP or User-Agent, counted in one
shared failure-row bucket; it is removed in the core MINOR after `stores/turso` repins past v0.21.0."*
RELEASING names the same removal point. The handler (`inbound/http/oauth2.go:348`) switches. The
handler's pre-parse `reject()`s (`:329-346`) stay silent under the rule below.

**R1 = (a), the boundary as a RULE (PM-8, verbatim in `audit.go:29-32`, the README denial table
and RELEASING Known):** *"A row is written when the server evaluated a credential, grant, token or
parameter value and refused it. A request refused for its shape (auth-header form, credentials in
the body, encoding, a parameter the grant does not accept) writes none; it appears only in access
logs and rate-limit counters."* Under it, `scope` on `authorization_code` (not accepted → shape) and
`scope` on token-exchange (accepted, value refused → row) are consistent. The placement debt is named
under Known too (arch-6): grant validation is split across the handler (`oauth2PublicResource`,
`inbound/http/oauth2.go:220-226`, `actor_token_type`, an empty `token`) and the service (`Resource`,
`tokens.go:19,60`) — "grant validation split across handler and service", a later re-split, not an
audit gap alone.

### Item 2b — the client-attribution rule at the ONE recording site (SRE-F1, data-4/5/6, arch-5; ruling 2)

`Recorder.Record` (`securityevent/recorder.go:133-156`) stamps `IPAddress`/`UserAgent` from the
carrier verbatim (`:147-148`) and bounds only `RequestID`/`TraceID` (`correlationID`, `:207-219`:
printable ASCII, at most `MaxCorrelationIDLen` = 128 bytes, else blank). The pgx store binds the
columns as-is (`stores/pgx/security_events.go:92-93`) and PostgreSQL rejects invalid UTF-8 in `TEXT`
(22021) and `\u0000` in JSONB (22P05): the row is lost, counted as `error_kind=unknown`. The
`auditReason` truncation at `audit.go:43-49` (2048 bytes) does not help: the handler
`QueryUnescape`s the Basic username (`inbound/http/oauth2.go:340`), so `%00` reaches Details on the
public-client paths today. Turso (TEXT JSON) and memory keep a U+0000 value — a pgx-only divergence
until no producer can send one (data-6, option (a) chosen).

**Fix (core, PR-2a).** `securityevent` exports ONE bounded-value helper (name and signature the
implementer's) and `Record` applies it to the request id, the trace id, the IP, the UA and every
top-level string value in `Details`; `auditReason` calls the same helper (no second copy of a
security rule — arch-5) and stops truncating. The rule: a value carrying NUL, invalid UTF-8 or a
control byte is recorded BLANK, never truncated and never refused; a correlation id over
`MaxCorrelationIDLen` or a Details id over 2048 bytes is blank; the UA is CUT at 512 bytes on a rune
boundary (truncating a UA loses nothing an auditor needs, unlike an id); the IP is blank unless
`netip.ParseAddr` accepts it. The helper takes a per-field policy (lead ruling on rev 2): the
request id and the trace id KEEP S2's published rule exactly — printable ASCII only, at most 128
bytes, else blank — so no correlation-id behavior changes in this train; the UTF-8 policy applies
to the UA, the IP and the Details values only. The 200-byte `X-Request-ID` proof from S2 still
blanks, and a non-ASCII request id still blanks. Details rules for `client_id`
stay: it rides a row only when it equals `ConfidentialClientID`, otherwise the key is omitted. The
comment at `audit.go:23` ("validated identifiers") is corrected. **Proof:** unit pins beside
`TestReasonsAreStableWireValues` (each rule, each field, the rune boundary, a zero/empty value), and
a pgx live case `TestLive_RecorderBoundsClientAttribution` in `pockets/authentication/stores/pgx`
through `securityevent.NewRecorder` + the pgx `SecurityEventStore` with (i) a `%00` client id, (ii) a
4 KB client id, (iii) a `\xff` User-Agent — each row exists with that field blank and
`WriteFailures() == 0` (SRE-F7); it runs in the pgx bare and schema legs before tag 2. RELEASING names
this as fixing a pre-existing pgx row loss. (The live case needs the new core; it lands in PR-2a
AFTER tag 2b is cut — Sequencing — so the `stores/pgx` v0.10.1 tree does not carry a test its pinned
v0.21.0 core cannot pass at `GOWORK=off`.)

### Flood posture — a per-IP budget on failure rows (SRE-F2 preferred fix, PM-9, SRE-F15; ruling 3)

What one IP can drive without a budget: `introspect` is 6000/min per IP (`inbound/http/oauth2.go:259`),
the mismatch row is written before any per-client limit (`:352`), each row is a synchronous insert
bounded at 2 s that ignores client cancellation (`recorder.go:150-152`) on the host's shared pool,
and the rows are append-only (the 0022 triggers) with no purge until S3 — ~8.6 M rows/day/IP, and the
memory limiter is per instance so a fleet multiplies it. The `invalid_state` precedent on the
unlimited callback is itself the bug, not a justification.

**Fix (core, PR-2a).** The Recorder gains an option (name the implementer's; the root wires it)
carrying a `ratelimiter.Limiter` and a limit — the root passes the SAME `limiter` it already hands
`OAuth2Config.Limiter` (`constructor.go:252-254`, `:537`) and the logic service (`:525`), at
`constructor.go:458-460` — and `securityevent.Input` gains a boolean (the implementer's name; the
sense is "an unauthenticated failure row") that `auditGrantFailure` sets on EVERY `oauth2_grant_failed`
row and `recordOAuth` sets on the callback `unknown_provider` row only. `Record` with the flag set and
a budget wired asks `limiter.Allow(ctx, "securityevent:failure-row:ip:" + digest(ip), limit)` with the
IP it already reads from the carrier; over budget ⇒ no row, a `SuppressedRows()` counter (exposed as
`Components.AuditRowsSuppressed()` beside `AuditWriteFailures()`, `authentication.go:35`), and one WARN
`audit_rows_suppressed count=N` per minute on the Recorder's existing once-then-per-minute machinery
(`failed`, `:177-204`). A limiter error records the row (a limiter fault costs a row, never loses
one). The limit is ~60/min per IP; the implementer picks the exact number and states it in RELEASING.
`invalid_state` on the callback (S2's row) is not budgeted and the callback route limit is filed
under Known. Known states the worst case anyway: with the budget, ≤ 60 rows/min/IP per instance
(~86 k/day/IP/instance), each ≤ ~3 KB (UA ≤ 512 B, ids ≤ 2048 B), multiplied by instances under the
memory limiter; the risk is accepted BECAUSE hosts adopt after S3 retention (maintenance rule);
operators alert on `oauth2_grant_failed reason=invalid_client`. The baseline's audit-write observer
(`examples/production-baseline/cmd/server/main.go:274-277`, one `ErrorContext` per failure,
unthrottled — the pattern hosts copy) counts and throttles instead: first failure at ERROR with
fields, then one line per minute with the count (PR-2c); `/healthz` `detail` gains `audit_rows_suppressed`.

### Item 3 — client attribution on host routes (one exported middleware; ruling 4)

`securityevent.Recorder` reads `authlogic.ClientInfoFromContext` (`recorder.go:85-91,147-148`); the
ONLY writer is `clientInfoMiddleware(proxies)` (`inbound/http/routes.go:437-449`), applied by
`clientInfoRegistrar` (`:218`) to the authentication pocket's routes alone. Nothing is exported for a
host, so a host route gated by `authorizationhttp.Adapter.Require` — the baseline's role routes
(`examples/production-baseline/cmd/server/main.go:305`), auth-cms's machine gate and debug route
(`examples/auth-cms/cmd/server/main.go:317,420`) — records its denial through the bridge with empty
IP/UA. The same gap already affects `external_login` rows first recorded on host routes
(`logic/authentication/external.go:282-300`) and the exported `RateLimitByIP`
(`inbound/http/public_rate_limit.go:15-26`), which on a host route puts every client in one
`prefix:` bucket (today's behavior, the defect RELEASING names).

**Shape.** `func (a *Adapter) ClientInfoMiddleware() web.Middleware` in
`pockets/authentication/inbound/http` (beside `RequirePrincipal`, `principal.go:57`), returning
`clientInfoMiddleware(a.proxies)`. The proxy posture moves onto the `Adapter` (`authenticator.go:49`):
`NewAuthenticator` (`:120`) BUILDS it from its own `cfg.RuntimeMode` and logger —
`newProxyPosture(cfg.RuntimeMode, logger)` — and `New` (`adapter.go:108-116`) reuses `auth.proxies`
for `mountDeps.Proxies` (SRE-F9, arch-1: the posture is a property of the adapter's runtime mode, not
of the bundled route table; the one in-repo production caller of the narrow constructor,
`examples/external-identity/cmd/server/main.go:114`, is exactly the host whose `external_login` rows
this fixes and would otherwise never see the "TrustProxies not wired" WARN). Hosts mount it with
`router.Use(authenticationComponents.HTTP.ClientInfoMiddleware())` AFTER the `TrustProxies` `Use` and
after the adapter exists: `WebHandler.Use` rebuilds dispatch over the mux (`sdk/pkg/web/handler.go:42-50`,
registration-ordered, outermost-first), so a later `Use` covers routes already registered and runs
inside `TrustProxies`. Not in each gate chain, not on `pockets.Mount` (C3 admits ports a pocket
CONSUMES; this is one it PROVIDES), not on the registrar. Double-stamping on the authentication routes
(global + registrar) writes the same values twice and is kept: the registrar is the safety net for a
host that skips the recipe.

**Visibility (PM-6, SRE-F10).** In production the Recorder WARNs ONCE per process when it records a
row whose context carries a request id but no client attribution (an empty IP from the reader is
sufficient: a stamped request always carries at least the peer address): *"security event recorded
without client attribution; mount ClientInfoMiddleware"*. The root enables it under production
through a Recorder option (the Recorder stays mode-agnostic); background rows (no request id) are
skipped by the heuristic. The doc at `context.go:92-99` ("sets it over ALL routes") is corrected to
"the pocket's routes, and every host route behind `ClientInfoMiddleware`".

**Fallback (ruling 4, SRE-F8, PM-6).** `RateLimitByIP` and the oauth2 handler's per-IP budget
(`oauth2Limit`, `inbound/http/oauth2.go:358-363`, through `clientIPFromContext`,
`authenticator.go:95-98`) fall back to `clientIP(r)` (`routes.go:490-503`: `web.ClientIP` when
`TrustProxies` ran, else the peer address) when the carrier is empty. The logic-side per-IP budgets
(`allowAuthenticationRequest` `abuse.go:67-85`; passwordless start/redeem `passwordless.go:450-451,
507-508`; resend `resend.go:133-134`) have no request and cannot read `web.ClientIP` (G25); they run
on the pocket's own routes, which the registrar always stamps, so they reach an empty carrier only
when a host calls the service from an unstamped host route — RELEASING says so, and the production
WARN above is their signal. (SRE-F8 also listed identifier management; its budgets,
`identifier_management.go:545,552`, are per-user and per-target, not per-IP — corrected in the record.)

**Placement (arch-2).** ARCHITECTURE.md's middleware table (`:227-236`) gets one row, **pocket-owned
context stamp**, carrying four clauses: (i) not a gate — it admits every request; (ii) the pocket
EXPORTS it, only the host INSTALLS it (`router.Use` in `cmd/`, H10) — the structural guard is that
`pockets.RouteRegistrar` has only `Handle`: a pocket cannot `Use` through `Mount`, and `Mount` never
grows a `Use`; (iii) it READS `web.ClientIP` (set by `web.TrustProxies`) and never `X-Forwarded-For`,
so it is registered AFTER the `TrustProxies` `Use` and runs inside it; (iv) it carries a pocket-private
carrier — a second framework pocket needing IP/UA is a promotion question toward root `sdk` (the
three graduation gates), never an authorization→authentication import (arch-9). The `examples/README.md`
§6 bullet (after "`TrustProxies` is outermost", `:646-649`) says "after the `TrustProxies` `Use`"
explicitly — both examples happen to satisfy it (baseline `main.go:205` `Use` precedes `mount` at
`:254`; auth-cms `:239` precedes `auth.New` at `:391`) but a host that builds the adapter before its
router would silently stamp peer addresses — and adds the caveat that attribution is only as good as
`TRUSTED_PROXY_COUNT` plus an origin reachable solely through those proxies (the `X-Real-IP` fallback
and the leftmost clamp, `sdk/pkg/web/trustproxies.go:59-77`, are the spoof surface on every host
route). `docs/guides/production-posture.md:112-114` ("audit rows and per-IP budgets see the client")
is false for host routes without the middleware and gains the recipe (PM-6).

**Bridge dedupe (PM-10, ruling 13).** Once rows carry IP/UA, the examples' 60 s dedupe key
(`examples/*/pockets/audit/outbound/securityevents/bridge.go:148-159`, identical in both: type,
principal, permission, resource type, resource id) would fold a denial from a second address into
the first row. The key gains the carrier's IP as a sixth length-prefixed field (the bridge reads
`authlogic.ClientInfoFromContext(ctx)`; a host-pocket outbound importing a framework pocket's logic
rim is H6/H7-sanctioned and the bridge already imports `securityevent`). Examples only, PR-2c; the
baseline README's dedupe sentence (`:241`) says a second address within the window records its own row.

### Item 4 — the R6 probes go

`stores/storetest/invitation_lifecycle.go:152-170` (`skipUnsupportedCancelAcceptance`,
`skipUnsupportedListActive`) and their TEN call sites (`:196, :225, :245, :259, :305, :327, :367,
:368, :449, :483` — data-12) are deleted; the comment at `storetest.go:260-261` is REPLACED, not
deleted (arch-8): *"Terminal settlement is mandatory for every adapter; an `errors.ErrUnsupported`
answer fails the suite."* The helpers are unexported and nothing outside storetest uses them;
`CancelAcceptance`/`ListActiveByResource` conformance becomes mandatory for every adapter. In-repo
adapters all implement both (`stores/pgx/invitations.go:255,273`, `stores/turso/invitations.go:269,287`,
`examples/auth-cms/pockets/authentication/outbound/authmem/ports_v2.go:461,484`, and storetest's own
reference adapter `reference_test.go:1133,1156`) and already run the cases; turso also runs
`storetest.Run` hermetically (`TestConformance_LocalSQLite`, `stores/turso/oauth2_test.go:49`), so
both storetest changes run in `make check` for turso and the reference adapter;
`examples/external-identity`'s memstore runs `RunExternalSubjects` only. A third-party adapter
answering `errors.ErrUnsupported` fails the suite at v0.22.0 — the RELEASING line says so.
**Proof is positive (data-3d):** "zero `unsupported by this adapter`" would prove nothing once the only
code printing it is deleted; each run must show `--- PASS` for `…/Invitations/CancelAcceptance`,
`…/CancelAcceptanceIdempotent`, `…/CancelAcceptancePastExpiry`, `…/CancelAcceptanceMismatch` and
`…/ListActiveByResourceHidesTerminalHistory` (`storetest.go:262-268`).

### Item 9 — `RotationKeepsExpiresAt`: the store returns what it stored (pgx store PATCH; ruling 5)

**Diagnosis.** `testSessionsRotationKeepsExpiry` (`stores/storetest/storetest.go:674-693`) builds a
session from `time.Now()` (nanoseconds kept), calls `Create`, keeps `before := created.ExpiresAt`,
rotates, reads back and asserts `got.ExpiresAt.Equal(before)`. The pgx `Create`
(`stores/pgx/sessions.go:106-116`) `Exec`s the insert and **returns the caller's `sess`
unchanged**; `sessionArgs` (`:242`) sends `ExpiresAt.UTC()` with its nanoseconds and pgx encodes
`timestamptz` at microsecond precision, so `Get` scans back a value up to 999 ns earlier than
`before`. `Rotate` (`:164-181`) does not touch `expires_at`. macOS's wall clock is
microsecond-granular, so the case passes locally every time; `ubuntu-latest` reports nanoseconds, so
it fails on the `live-stores` dispatch on effectively every run. Not a Postgres flake, not a
re-stamping rotation: a platform-dependent assertion over a store that reports what it was given.
Turso is immune (`FormatTime`, nine fractional digits); `authmem` returns and reads the same value.
**The review gate widened the finding (data-1, SRE-F5):** `sessionArgs`/`sessionInsert` back FOUR
pgx insert paths and all four return their input unchanged — `SessionStore.Create` (`sessions.go:115`),
`ActiveSessionStore.CreateForActiveUser` (`user_admin.go:237-269`, the ONLY production mint path:
`createSession`, `logic/authentication/service.go:988-994`), `OAuth2Store.RedeemCode`
(`oauth2.go:125-164`, returns `proposed`) and `insertRedemptionSession` (`passwordless.go:376-387`,
the passwordless redemption). Fixing `Create` alone would make "returns the session as persisted"
false for the path production uses. (Ruling 5 names three sites; the tree has four `sessionInsert`
callers; rev 2 covers all four.)

**Fix, two halves in one PR, neither loosening the assertion.** (a) Store: ONE helper
`persisted(sess session.Session) session.Session` in `stores/pgx/sessions.go` converts `CreatedAt`,
`ExpiresAt` and `Authentication.AuthenticatedAt` to UTC truncated to the microsecond (a zero
`AuthenticatedAt` stays zero; the secrets `utc()` precedent, `pockets/secrets/stores/pgx/pgx.go:531`);
`sessionArgs` encodes FROM the helper's result and all four paths RETURN it. Pre-truncating in
`sessionArgs` is load-bearing, not cosmetic (data-7): pgx's binary encoding truncates but the text
protocol rounds (`rint`, half-to-even), so only Go-side truncation makes the stored value the same
under every `default_query_exec_mode`. A hermetic unit test pins the helper (nanoseconds % 1000 == 0;
zero stays zero). `pockets/authentication/stores/pgx/v0.10.1` PATCH; core pin stays v0.21.0.
(b) Fixture clock: `testSessionsRotationKeepsExpiry` and `testActiveMintRoundTrip`
(`storetest/useradmin.go:549-575`, which gains `created.ExpiresAt.Equal(got.ExpiresAt)`) build their
session from `time.Now().Truncate(time.Microsecond).Add(750 * time.Nanosecond)` — 750 ns, NOT 500:
500 is the rounding tie, under which a text-protocol store would pass or fail by the parity of the
microsecond digit; with 750, truncation gives …000 and rounding gives +1 µs, both differ from the
input — with a one-line comment ("a store returns the session as persisted; a sub-microsecond clock
proves it on every platform"). The assertions are `Equal`, unchanged in shape. Memory and turso
round-trip the 750 ns and pass; pgx passes only once every insert path reports stored precision.
(c) The port states the contract before conformance pins it (arch-3): `session/repository.go:55-57`
`Create` and `session/active.go:28-30` `CreateForActiveUser` each gain *"returns the session as
persisted; a store with coarser timestamp precision than the entity returns the truncated values,
and the conformance suite proves it (`RotationKeepsExpiresAt`, `ActiveMintRoundTrip`)"* — doc only,
no signature; it rides v0.22.0 with the storetest half. Both halves ship in one PR; the proof is the
LIVE leg, not `make check` (`make check` is hermetic — `TestConformance_Postgres` skips without
`POSTGRES_TEST_DSN` — so the storetest half merged alone would leave `make check` green and only the
pgx live leg red; Risk 7). A `stores/pgx` consumer at `GOWORK=off` still runs v0.21.0's suite (the
old clock), which the fixed store also passes. RELEASING carries "storetest at v0.22.0 tightens two
adapter contracts: terminal settlement is mandatory; `Create`/`CreateForActiveUser` return the
session as persisted" (PM-5, SRE-F5).

### Items 5–8 and 10 — the small ones

- **Workshop init pin → `workshop/gopernicus/v0.6.1` PATCH.** `internal/commands/templates/init/
  readme.md.tmpl:140` (`@v0.5.0 guard --posture .` → `@v0.6.0`; the "older than v0.5.0" sentence
  stays true), `workshop/documentation/docs/workshop/commands.md:175` (same), and the install line
  `workshop/gopernicus/README.md:12` (`@v0.4.0` → `@v0.6.1`, the tag this PR produces). Precedent:
  v0.4.1 "Scaffold pins" (RELEASING `:495-520`) — template pins moved, no template code changed,
  PATCH. The S2 Known said "next workshop minor (the S1 precedent)"; the PATCH supersedes that
  promise and RELEASING records the supersession (PM-13, as S2 item (l) did for Firestore). No test
  pins the template's README text. **Proxy hazard (SRE-F16):** the install line names `@v0.6.1`
  before the tag exists; a `go install` against the proxy in that window is negative-cached for ~30
  min, so the tag is pushed immediately after the squash merge as its own step, and nothing touches
  the proxy before it.
- **Guard count.** `README.md:92` and `:121` say "twenty-three layering guards"; after the
  retirement the aggregate is thirty-three (`Makefile:516` and `check.yml:2` already say so). Both
  lines → "thirty-three". No other count prose exists.
- **Orphaned script — R-A, the owner's call.** `.github/scripts/authorization_cache_verify.py` and
  `test_authorization_cache_verify.py` are the last tracked files there; nothing in
  `.github/workflows` or the `Makefile` runs them (SRE-F14: no CODEOWNERS or CodeQL fallout either).
  But the script reproduces the 109-case matrix `pockets/authorization/BENCHMARKS.md` still cites
  (`:134`, `:165`, `:232`, `:572`), and rev 1 removed only the recipe paragraph (`:155-166`), stranding
  published numbers (PM-3). Deletion was never on the S2 Known list; the retirement plan called it
  the owner's call. Rev 2 does not decide it — see Open rulings R-A.
- **Handoff maintenance rule (PM-1, ruling 11).** PR-4 edits `plans/soc2-s2-audit-completeness-handoff.md`:
  the status line's "apply in gps-360-go, Segovia and coordination-hub on each host's own schedule"
  (`:3-4`) is REPLACED by *"Maintenance rule (owner, 2026-10-23): host handoffs are written and
  updated per slice but are NOT applied to the hosts until the SOC 2 roadmap is complete; the hosts
  adopt once, at the end."*; step 2 (`:24`) → `pockets/authentication` **v0.22.0** + `stores/pgx`
  **v0.10.1**; step 4 (`:32`) → `workshop/gopernicus` **v0.6.1**; the per-host recaps (`:184-186`,
  `:207-208`) and "After the repin" (`:232`) move the same versions; `:174-175` ("Bridged rows carry
  no `ip_address` / `user_agent` yet") becomes the recipe: *"Mount
  `authenticationComponents.HTTP.ClientInfoMiddleware()` with `router.Use` after the `TrustProxies`
  `Use` and after `authentication.New`; bridged rows and host-route `external_login` rows then carry
  `ip_address`/`user_agent`, and `RateLimitByIP` keys per client."* `roadmap.md`'s S2 line gets the
  rule sentence (gitignored — a local edit, not PR content).
- **Leaked `GOCACHE` path (item 10; SRE-F13).** `.github/workflows/live-stores.yml:46-47`, a
  workflow-level `env: GOCACHE: /tmp/gopernicus-audit-s1.aMLwCQ/cache` — a `mktemp` name from the S1
  audit laptop, added by #124. Verified: no Go, Make or workflow consumer reads `GOCACHE`;
  `setup-go` caches `go env GOCACHE` and saves only on success, and the only dispatch since #124
  failed, so nothing was ever saved under the `/tmp` path; after removal `live-stores` shares
  `check.yml`'s cache key. Delete the two lines; no replacement. Rides **PR-2b** so the single
  pre-merge branch dispatch proves items 9 and 10 together. The same edit replaces the stale header
  paragraph (`:24-28`, "Secrets status as of 2026-07-07 … PENDING UPLOAD") with one sentence: no
  Turso secrets are configured (the hosted playground is gone, S2), the turso legs skip in CI and are
  proven locally against libsql — comment only.
- **Runbook drift (PM-14).** `docs/guides/operations-runbooks.md:68` ("There is no request id on a
  row today (planned, S2)") and `:70-72` ("gated by any authenticated session" — it is platform-admin
  since S2) are fixed in PR-4, no tag.

## Tasks — the PR train

Model `opus` for every task. Branch names in parentheses; every PR branches from `main` at or after
019e8a36. Verify commands run from the repo root. The live legs use a scratch PostgreSQL 17 on a free
port — `lsof -i :<port>` first, **never 55432** — and a local libsql-server for the turso
`-tags=integration` leg (`ghcr.io/tursodatabase/libsql-server`, zero mode;
`TURSO_DATABASE_URL=http://127.0.0.1:8081 TURSO_AUTH_TOKEN=local` — `TestConformance_Turso` skips
unless BOTH are set, `conformance_integration_test.go:53-57`; data-3b). Subtest selection: `-run`
matches the top-level name first — `TestConformance_Postgres`, `TestConformance_Turso`,
`TestConformance_LocalSQLite`, `TestConformance` (authmem), `TestReference` (storetest) — then
`/Sessions` or `/Invitations` (data-3b/c).

### PR-1 — secrets pgx store: `DeleteOwner` locks the owner key (`secrets/delete-owner-lock`)

- **files:** `pockets/secrets/stores/pgx/pgx.go` (`DeleteOwner` `:320-350`: the `FOR UPDATE` select
  first, scanned through `pgxdb.MapError`, `sdk.ErrNotFound` on no row, then `deleteOwnerSecrets`,
  then the owner-key delete — whose `n == 0` branch becomes unreachable and goes),
  `pockets/secrets/stores/pgx/concurrency_test.go` (`TestLive_DeleteOwnerVersusPutSecret` with the
  service-level assertion, `TestLive_DeleteOwnerVersusRotation`, the shared barrier harness —
  Design), `pockets/secrets/stores/pgx/README.md` ("Schema" paragraph `:23-27`: *"`DeleteOwner`
  locks the owner key first, under the default READ COMMITTED: a write that commits first makes the
  delete a retried conflict; a write that arrives after the delete fails because the owner no longer
  exists (`sdk.ErrInvalidReference`). The lock is held for the transaction; a request-path caller
  carries a deadline — there is no `lock_timeout`."*), `pockets/secrets/stores/pgx/pgx_test.go` only
  if a unit case needs the new `ErrNotFound` ordering.
- **behavior:** the only observable changes are the sentinel under the race (`ErrInvalidReference`
  → `ErrConflict`, which the service absorbs in its three retries), the removed deadlock against a
  resealing rotation, and an owner with no key answering `ErrNotFound` before touching
  `secrets_values` (it could not have any: the FK forbids it).
- **BREAKING:** no. Port unchanged, schema unchanged, `pockets/secrets` core untouched. `FOR
  UPDATE` needs UPDATE privilege on `secrets_owner_keys`, which the runtime role already holds for
  `ReplaceOwnerKey`.
- **escape hatch:** none needed; `git revert` of the PR.
- **tests:** the two live race tests (Design), each run once against the pre-fix code WITH the DSN
  to prove they bite; `-count=20` post-fix in both legs; the 16 storetest cases through `Open` and
  `TestLive_ConcurrentReplaceOwnerKey` still green, bare and `POSTGRES_TEST_SCHEMA` legs.
- **verify:**
  ```
  cd pockets/secrets/stores/pgx && go build ./... && go vet ./...
  # once, on main pre-fix (a worktree at 019e8a36 with only concurrency_test.go copied in) — both FAIL, never skip:
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='postgres://postgres:postgres@127.0.0.1:<free-port>/postgres?sslmode=disable' go test -count=1 -run 'TestLive_DeleteOwnerVersusPutSecret' -v ./...   # FAILS: got sdk.ErrInvalidReference
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 -run 'TestLive_DeleteOwnerVersusRotation' -v ./...    # FAILS: deadlock (40P01, unmapped)
  # on the branch:
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 ./...
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='…' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test -count=1 ./...
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=20 -run 'TestLive_DeleteOwnerVersus' ./...
  cd pockets/secrets/stores/pgx && POSTGRES_TEST_DSN='…' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test -count=20 -run 'TestLive_DeleteOwnerVersus' ./...
  make guard && make check
  ```
- **tag:** step 1 `pockets/secrets/stores/pgx/v0.2.1` after the live leg (bare + schema, the race
  tests at `-count=20`) on merged `main`. Linux evidence: the secrets leg is the last in
  `make test-stores`; it runs on Linux in PR-2b's container run only if PR-1 merged first (Sequencing).
  Otherwise tag 1 is macOS-only, and the release record says so.

### PR-2a — authentication: the denial paths, the OAuth2 sibling, the attribution and budget rules, the README table (`authentication/denial-paths-oauth2-audit`)

Independent of PR-1 and PR-3; merges AFTER tag 2b is cut (the pgx live case needs the new core —
Sequencing). PR-2c branches after this PR merges (shared `recorder.go`, `constructor.go`, README).

- **files:**
  - `pockets/authentication/logic/authentication/securityevent/securityevent.go` (six `Reason*`
    constants after `ReasonRevisionConflict` `:351`, each with the one-line doc the others carry;
    the exported bounded-value helper), `…/securityevent/recorder.go` (`Record` applies the helper
    to request id, trace id, IP, UA and Details strings; `correlationID` becomes a call to it; the
    failure-row budget option, the `Input` flag, `SuppressedRows()`, the per-minute
    `audit_rows_suppressed` WARN), `…/securityevent/securityevent_test.go`
    (`TestReasonsAreStableWireValues` `:151`; the helper pins), `…/securityevent/recorder_test.go`
    (budget: over budget no row + counter + one WARN per minute; limiter error records; flag unset
    never consults the limiter; the helper applied per field);
  - `pockets/authentication/logic/authentication/securityevent.go` (`recordCredentialMutationDenial`
    `:65-77`: `sdk.ErrAlreadyExists` → `claim_conflict`);
  - `pockets/authentication/logic/authentication/password.go` (`:57`, `:76-79`, `:184-187`, `:188-191`),
    `…/service.go` (`:836-839`), `…/identifier_management.go` (`:149-151`, `:303-305`, `:345-347`),
    `…/oauth.go` (`:204-207` with the `linkUserID` switch; `:294-297` sets the budget flag);
  - `pockets/authentication/logic/authentication/oauth2/service.go` (`AuthenticateClientContext`;
    `AuthenticateClient` delegates with `context.Background()`, `Deprecated:` text per Design),
    `…/oauth2/audit.go` (`auditGrantFailure` `:33-40` three branches + the budget flag; `auditReason`
    `:42-69` calls the helper, no truncation; comments `:23`, `:29-32` with PM-8's rule verbatim),
    `…/oauth2/denial_audit_test.go` (`:260` rewritten; new cases), `…/oauth2/service_test.go`;
  - `pockets/authentication/logic/authentication/denial_coverage_test.go` (one case per row of the
    Design table, each through `requireDenial` + `assertNoSecretInDetails`);
  - `pockets/authentication/inbound/http/oauth2.go` (`:348` → the sibling);
  - `pockets/authentication/constructor.go` (`:458-460`: the budget option with the root's `limiter`
    and the chosen limit), `pockets/authentication/authentication.go` (`AuditRowsSuppressed()` beside
    `AuditWriteFailures()` `:35`); `logic/authentication/helpers.go:22` and
    `logic/invitations/constructor.go` only if the Recorder option set they build must match;
  - `pockets/authentication/stores/pgx/security_events_live_test.go` (new:
    `TestLive_RecorderBoundsClientAttribution`, DSN-gated like `TestConformance_Postgres`);
  - `pockets/authentication/README.md` (reasons list `:3451-3457`; the denial table `:3459-3472` —
    the `SetPassword`, `ChangePassword`, `RemovePassword`, identifier and OAuth rows gain their new
    reasons, the OAuth2 row drops "A malformed request records nothing", gains the three codes, the
    PM-8 rule and what a blank/absent `client_id` means, one row for the confidential-client
    mismatch; a paragraph for the attribution rule and the failure-row budget beside the recorder
    text at `:3498`; the "no rate limit on the start route" sentence for the OAuth start).
- **behavior:** nine more denial paths record; six new bounded reasons; every attributed value is
  bounded at the recorder (a NUL, invalid UTF-8 or control byte blanks it; the UA is cut at 512
  bytes; an unparsable IP is blank; a Details id over 2048 bytes is blank — today: truncated, and a
  NUL loses the row on pgx); `oauth2_grant_failed` carries `client_id` only when it names the
  configured confidential client; `oauth2_grant_failed` rows and the callback `unknown_provider` row
  are budgeted per IP; `AuthenticateClient` is deprecated, delegating, not removed. Recording never
  fails a flow (unchanged).
- **BREAKING:** no exported signature changes. Behavior: (a) `TestOAuth2MalformedRequestsRecordNothing`'s
  contract inverts — a dashboard that assumed malformed token requests leave no row sees rows now,
  bounded by the failure-row budget. Correlation ids keep S2's rule byte-for-byte (per-field policy).
  Pre-1.0 MINOR, rides v0.22.0.
- **escape hatch:** none; the recorder fails open.
- **tests:** one failure/blocked case per Design-table row asserting type, status, reason, `UserID`
  presence or absence, plus `assertNoSecretInDetails` over every recorded row (the attempted
  password, address, code, token, client secret and a `%00`-bearing client id); OAuth2:
  `AuthenticateClientContext` mismatch → one row with no `client_id` for a foreign id and with it for
  the configured id, success → nothing, the NUL/oversize id → blank, `ErrInvalidRequest`/`Scope`/
  `Target` → the three reasons, `invalid_target` on `RedeemCode` (the old test's case) now records,
  the deprecated shell records an unattributed row; `oauth.go`: unknown provider on callback →
  `oauth_login` with no `provider` key and the budget flag, on `StartLink` → `oauth_linked` with the
  user, on `StartOAuth` → nothing; the Recorder helper and budget pins; the pgx live case.
- **verify:**
  ```
  cd pockets/authentication && go build ./... && go test ./... && go vet ./...
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 -run 'TestLive_RecorderBoundsClientAttribution' -v ./...   # three rows exist, the field blank, WriteFailures()==0
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test -count=1 -run 'TestLive_RecorderBoundsClientAttribution' -v ./...
  make guard && make check
  # run-and-look (PM-2): examples/auth-cms, port 8082, AUTH_OAUTH2_DEMO=1 AUTH_DEBUG=1 make run; then
  #   curl -si -u 'oauth-demo-mcp:wrong' -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange http://127.0.0.1:8082/oauth2/token   → 401 invalid_client
  #   curl -si -u 'someone-else:wrong'   -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange http://127.0.0.1:8082/oauth2/token   → 401 invalid_client
  #   curl -si -u 'oauth-demo-mcp%00:wrong' -d grant_type=… http://127.0.0.1:8082/oauth2/token                                              → 401; a row, client_id ABSENT, not a lost row
  #   seventy requests in a minute from one address → the 61st+ record no row; one WARN audit_rows_suppressed count=N; /healthz (if auth-cms exposes it) or the test counter shows audit_rows_suppressed
  #   the two oauth2_grant_failed rows (one WITH client_id=oauth-demo-mcp, one WITHOUT) with ip/ua and NO client_secret are proven by TestDebugSecurityEventsRequiresPlatformAdmin's
  #   in-process dump (auth-cms has no signed-in platform admin; RELEASING :355-357) — add the two-row assertion to that test.
  ```
- **tag:** rides step 2 `pockets/authentication/v0.22.0` (after PR-2a, PR-2b, PR-2c and PR-2d).

### PR-2b — authentication pgx store: every insert returns stored precision; the 750 ns fixture clock; the `GOCACHE` deletion (`authentication/session-stored-precision`)

Independent of PR-1, PR-2a, PR-2c, PR-2d and PR-3 (touches `storetest.go:674-693` and
`useradmin.go:549-575`; PR-2d touches `storetest.go:260-261` and `invitation_lifecycle.go` —
disjoint hunks). Both halves in one PR (Design, item 9). Merges FIRST among the 2x PRs (Sequencing).

- **files:** `pockets/authentication/stores/pgx/sessions.go` (`persisted()` helper; `sessionArgs`
  `:224-243` encodes from it; `Create` `:106-116` returns it; doc comment "returns the session as
  persisted — PostgreSQL keeps microseconds; Go-side truncation keeps the text protocol from
  rounding"), `…/stores/pgx/user_admin.go` (`CreateForActiveUser` `:237-269` returns `persisted(sess)`),
  `…/stores/pgx/oauth2.go` (`RedeemCode` `:125-164` returns `persisted(proposed)`),
  `…/stores/pgx/passwordless.go` (`insertRedemptionSession` `:376-387` returns `persisted(sess)`),
  `…/stores/pgx/sessions_test.go` (the hermetic helper test), `…/stores/pgx/README.md` (one sentence
  under its store/testing notes: time fields come back at microsecond precision from every insert
  path), `pockets/authentication/stores/storetest/storetest.go` (`testSessionsRotationKeepsExpiry`
  `:674-693`: the 750 ns clock + comment; assertion unchanged), `…/storetest/useradmin.go`
  (`testActiveMintRoundTrip` `:549-575`: the same clock and `created.ExpiresAt.Equal(got.ExpiresAt)`),
  `pockets/authentication/logic/authentication/session/repository.go` (`:55-57` the contract
  sentence), `…/session/active.go` (`:28-30` the same), `.github/workflows/live-stores.yml`
  (`:46-47` the `env:` block — both lines, nothing replaces them; `:24-28` the stale secrets
  paragraph → one sentence, comment only).
- **behavior:** every pgx session insert returns timestamps equal to what `Get` reads. Nothing
  else: `Rotate` never touched `expires_at`; no SQL change, no migration.
- **BREAKING:** no. A caller that compared an insert's return with its own nanosecond input (none
  in the repo; the service uses only `created.ID`) sees the stored value now. (b') The 750 ns fixture
  clock is a contract tightening for third-party adapters: one that stores coarser than nanoseconds
  but returns its input now fails `storetest.Run` on every platform — at v0.22.0, named in RELEASING
  beside the R6 line (SRE-F5, PM-5).
- **escape hatch:** n/a.
- **tests:** the two conformance cases, run once against the old store with the new clock (fail
  deterministically on macOS and Linux: `Rotate changed ExpiresAt: before=…750ns after=…000ns`) and
  then green; the hermetic helper test; a pgx live case asserting `Equal` between each insert path's
  return and `Get` for a nanosecond input (`Create`, `CreateForActiveUser`, `RedeemCode`); turso,
  `LocalSQLite` and authmem suites still green (the 750 ns round-trips there).
- **verify:**
  ```
  cd pockets/authentication/stores/pgx && go build ./... && go vet ./... && go test -count=1 -run 'TestPersisted' ./...
  # once with the OLD sessions.go and the new clock — FAILS on this laptop too:
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 -run 'TestConformance_Postgres/(Sessions/RotationKeepsExpiresAt|UserAdmin/ActiveMintRoundTrip)' -v ./...
  # on the branch:
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 ./...
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test -count=1 ./...
  cd pockets/authentication/stores/turso && TURSO_AUTH_TOKEN=local TURSO_DATABASE_URL=http://127.0.0.1:8081 go test -tags=integration -count=1 -run 'TestConformance_(Turso|LocalSQLite)/Sessions' -v ./... | tee /dev/stderr | grep -q -- '--- SKIP' && exit 1
  cd examples/auth-cms/pockets/authentication/outbound/authmem && go test -count=1 -run 'TestConformance/Sessions' ./...
  cd pockets/authentication && go test -count=1 -run 'TestReference/Sessions' ./stores/storetest/...
  grep -rn 'GOCACHE\|/tmp/' .github/workflows/                              # none
  make guard && make check
  # CI proof (ruling 7), BEFORE merge:
  #   1. Linux container: docker run --rm -v "$PWD":/src -w /src --network host golang:<go.work toolchain> sh -c 'POSTGRES_TEST_DSN=… TURSO_DATABASE_URL=http://127.0.0.1:8081 TURSO_AUTH_TOKEN=local make test-stores'
  #      (a postgres:17 and a libsql-server reachable from the container). The legs after authentication pgx — jobs, events, authorization, secrets pgx; every turso leg — run on a
  #      nanosecond clock for the first time since 2026-07-07; any failure there is its OWN finding, filed, never recorded as green.
  #   2. gh workflow run live-stores.yml --ref authentication/session-stored-precision; the gate = the authentication pgx bare AND schema legs green in the log;
  #      the log shows setup-go's cache at the default path (no /tmp/gopernicus-audit-s1 anywhere). Turso legs SKIP in CI (no secrets) — turso is proven only by step 1 and LocalSQLite.
  ```
- **tag:** step 2b `pockets/authentication/stores/pgx/v0.10.1` (PATCH; core pin v0.21.0, pgxdb
  v0.11.0 unchanged) after merge + the pgx leg on `main` (bare + schema).

### PR-2c — authentication: `ClientInfoMiddleware`, the posture on the Adapter, the fallback, the WARN, the ARCHITECTURE row, the examples (`authentication/client-info-middleware`)

Branches from `main` after PR-2a merges (shared `recorder.go`, `constructor.go`, README).

- **files:**
  - `pockets/authentication/inbound/http/authenticator.go` (`Adapter` gains `proxies *proxyPosture`;
    `NewAuthenticator` `:120` sets `newProxyPosture(cfg.RuntimeMode, logger)`; `clientIPFromContext`
    `:95-98` gains the request-aware fallback or a sibling `clientIPFor(r)`), `…/inbound/http/adapter.go`
    (`:115` `Proxies: auth.proxies`), `…/inbound/http/routes.go` (`:218` unchanged; `ClientInfoMiddleware`
    next to `clientInfoMiddleware` `:441`), `…/inbound/http/public_rate_limit.go` (`:18-20` the
    fallback to `clientIP(r)` when the carrier is empty), `…/inbound/http/oauth2.go` (`:359` the same
    fallback), `…/inbound/http/client_info_test.go` (new);
  - `pockets/authentication/logic/authentication/securityevent/recorder.go` (the production
    missing-attribution WARN, once per process, behind an option), `pockets/authentication/constructor.go`
    (enables it under `cfg.RuntimeMode == production`), `…/securityevent/recorder_test.go`;
  - `pockets/authentication/logic/authentication/context.go` (`:92-99` doc);
  - `pockets/authentication/README.md` ("The middleware surface" `:1198` gains a
    `ClientInfoMiddleware()` paragraph with the `router.Use` recipe, the ordering clause, the three
    surfaces it fixes, the fallback and the WARN);
  - `ARCHITECTURE.md:227-236` (one table row: **pocket-owned context stamp** —
    `authenticationhttp.Adapter.ClientInfoMiddleware()`, the four clauses from Design);
  - `examples/README.md:646-649` (a bullet after "`TrustProxies` is outermost": mount the
    authentication adapter's `ClientInfoMiddleware()` with `router.Use` after the `TrustProxies` `Use`
    and once the adapter exists, so audit rows and `RateLimitByIP` on host routes see the client;
    attribution is only as good as `TRUSTED_PROXY_COUNT` plus an origin reachable solely through those
    proxies; a non-rule recipe, not a P-row — `TestGuardListMatchesContract` matches only `| **Pn** |`
    rows, `guard_test.go:53`);
  - `workshop/documentation/docs/guides/production-posture.md:112-114` (the recipe; "audit rows and
    per-IP budgets see the client" holds only for the pocket's routes and host routes behind the
    middleware);
  - `examples/production-baseline/cmd/server/main.go` (`mount` `:254-…`:
    `router.Use(authenticationComponents.HTTP.ClientInfoMiddleware())` after `authentication.New`
    `:263`; the observer `:274-277` counts and throttles; `/healthz` `detail` `:360` gains
    `audit_rows_suppressed`), `examples/production-baseline/pockets/audit/outbound/securityevents/bridge.go`
    (`dedupeKey` `:148-159` + the IP; its test), `examples/production-baseline/README.md` (`:37` router
    list; "Audit completeness" `:226-245` one sentence each for the middleware, the dedupe-by-address
    and the throttled observer), a router-level test beside `cmd/server/production_test.go`;
  - `examples/auth-cms/cmd/server/main.go` (the same `Use` after `auth.New` at `:391` — mount after
    `:395`, PM-15), `examples/auth-cms/pockets/audit/outbound/securityevents/bridge.go` (the same key
    change; its test), `examples/auth-cms/cmd/server/debug_route_test.go` (the existing non-admin 403
    → one `authorization_denied` row case also asserts non-empty `ip_address` and `user_agent`).
- **behavior:** host routes behind `ClientInfoMiddleware` record IP/UA on every row and
  `RateLimitByIP` keys per client (and, when the carrier is empty, on the `TrustProxies`-resolved or
  peer address instead of one shared bucket); a production `NewAuthenticator` adapter WARNs once when
  `TrustProxies` is not wired; a production row with a request id and no attribution WARNs once per
  process; the examples' bridge dedupes per address.
- **BREAKING:** no exported signature changes; `ClientInfoMiddleware()` is additive. Behavior:
  `RateLimitByIP` and the oauth2 per-IP budget rebucket from one shared bucket to per-client ONLY
  when the middleware is mounted or the fallback applies (shared → per-IP can only admit more; no
  host newly 429s); host routes now add IP/UA — personal data — to an immutable table whose erasure
  is S3's. Rides v0.22.0.
- **escape hatch:** none; a host that does not mount `ClientInfoMiddleware` keeps today's rows
  (blank IP/UA on host routes) and sees the WARN.
- **tests:** `ClientInfoMiddleware` stamps `web.ClientIP` when `TrustProxies` ran and the peer
  address otherwise; a `NewAuthenticator`-built adapter in production WARNs once ("TrustProxies not
  wired") and returns a working middleware; `RateLimitByIP` keys per client with the carrier and per
  resolved address without it; the Recorder WARNs once in production for a request-id-bearing row
  with no attribution and never for a background row; the dedupe key differs by address; the two
  example router-level tests (a denied role request through the real router → one row with
  non-empty `ip_address` and `user_agent`); the throttled observer.
- **verify:**
  ```
  cd pockets/authentication && go build ./... && go test ./... && go vet ./...
  cd examples/auth-cms && go build ./... && go test ./... && go vet ./...
  cd examples/production-baseline && go build ./... && go test ./... && go vet ./...
  make docs-install && make docs-build
  make guard && make check
  # run-and-look (PM-2, ruling 12): examples/production-baseline against the scratch Postgres (its README :127-148 drive):
  #   make run; TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true}' | jq -r .token)
  #   curl -si -A 'probe/1.0' -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$ASSIGN"   → 403 permission_denied
  #   curl -si -A $'\xff'     -XPOST localhost:8080/authorization/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$ASSIGN"   → 403 (a second address or a new token, or wait out the 60 s window)
  #   psql: SELECT event_type, ip_address, user_agent, request_id FROM security_events WHERE event_type IN ('authorization_denied','external_subject_provisioned','external_login') ORDER BY created_at;
  #     → no blank ip_address; the probe row has user_agent='probe/1.0' and the response's X-Request-Id; the \xff row EXISTS with user_agent='' ; curl -s localhost:8080/healthz → audit_write_failures=0
  #   boot log: no "security event recorded without client attribution" line (the middleware is mounted); remove the Use line, rerun one 403 under APP_RUNTIME_MODE=production → the WARN once.
  ```
- **tag:** rides step 2 `pockets/authentication/v0.22.0`.

### PR-2d — authentication storetest: terminal settlement is mandatory (`authentication/storetest-terminal-settlement-mandatory`)

Independent of every other PR (disjoint hunks from PR-2b in `storetest.go`).

- **files:** `pockets/authentication/stores/storetest/invitation_lifecycle.go` (`:152-170` + the ten
  calls), `…/storetest/storetest.go` (`:260-261` comment replaced per arch-8).
- **behavior:** the conformance suite no longer skips terminal settlement for any adapter.
- **BREAKING:** behavior only — an adapter answering `errors.ErrUnsupported` to `CancelAcceptance`
  / `ListActiveByResource` now FAILS `storetest.Run` (no in-repo adapter does). Rides v0.22.0.
- **escape hatch:** none; a third-party adapter implements the two methods.
- **tests:** the positive `--- PASS` lines for the five terminal-settlement subtests in every run
  below; `grep -rn 'ErrUnsupported\|skipUnsupported' pockets/authentication/stores/storetest` → none.
- **verify:**
  ```
  cd pockets/authentication && go build ./... && go test ./... && go vet ./...
  PASSES='--- PASS: .*/Invitations/(CancelAcceptance|CancelAcceptanceIdempotent|CancelAcceptancePastExpiry|CancelAcceptanceMismatch|ListActiveByResourceHidesTerminalHistory) '
  cd pockets/authentication && go test -count=1 -v -run 'TestReference/Invitations' ./stores/storetest/... 2>&1 | grep -E -c "$PASSES"                                       # 5
  cd pockets/authentication/stores/turso && go test -count=1 -v -run 'TestConformance_LocalSQLite/Invitations' ./... 2>&1 | grep -E -c "$PASSES"                           # 5
  cd pockets/authentication/stores/turso && TURSO_AUTH_TOKEN=local TURSO_DATABASE_URL=http://127.0.0.1:8081 go test -tags=integration -count=1 -v -run 'TestConformance_Turso/Invitations' ./... 2>&1 | grep -E -c "$PASSES"   # 5
  cd examples/auth-cms/pockets/authentication/outbound/authmem && go test -count=1 -v -run 'TestConformance/Invitations' ./... 2>&1 | grep -E -c "$PASSES"                 # 5
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' go test -count=1 -v -run 'TestConformance_Postgres/Invitations' ./... 2>&1 | grep -E -c "$PASSES"            # 5
  cd pockets/authentication/stores/pgx && POSTGRES_TEST_DSN='…' POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test -count=1 -v -run 'TestConformance_Postgres/Invitations' ./... 2>&1 | grep -E -c "$PASSES"   # 5
  grep -rn 'ErrUnsupported\|skipUnsupported' pockets/authentication/stores/storetest   # none
  make guard && make check
  ```
- **tag:** rides step 2 `pockets/authentication/v0.22.0`.

### PR-3 — workshop: the init guard pin (`workshop/init-guard-pin`)

- **files:** `workshop/gopernicus/internal/commands/templates/init/readme.md.tmpl:140`,
  `workshop/documentation/docs/workshop/commands.md:175`, `workshop/gopernicus/README.md:12`.
- **behavior:** a freshly scaffolded host's README runs `guard@v0.6.0 --posture .` (P9 included);
  the docs site and the install line say the same. No code, no emitted `go.mod` change.
- **BREAKING:** no. PATCH by the v0.4.1 precedent; supersedes the S2 Known "next workshop minor".
- **escape hatch:** n/a.
- **tests:** the existing scaffold compile tests; `make docs-build` for the docs line.
- **verify:**
  ```
  cd workshop/gopernicus && go build ./... && go test ./... && go vet ./...
  grep -rn 'gopernicus@v0\.[0-9]*\.[0-9]*' workshop/gopernicus/internal/commands/templates workshop/gopernicus/README.md workshop/documentation/docs   # only v0.6.0 (template, docs) and v0.6.1 (install line)
  make docs-install && make docs-build
  make check
  # BEFORE writing "exits 0" as the expectation (SRE-F16): a fresh scaffold must be posture-clean at v0.6.0:
  cd "$(mktemp -d)" && GOWORK=off go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.6.0 init --db pgx --module example.com/x && GOWORK=off go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.6.0 guard --posture . ; echo "exit=$?"   # 0; exit 1 OR 2 is a failure, never noise
  ```
  Tag step (SRE-F16): the squash merge, then IMMEDIATELY the annotated tag and its push as the next
  step, then the proxy poll — nothing resolves `@v0.6.1` before the push. Cold proof after the
  tag: `GOWORK=off GOMODCACHE=$(mktemp -d) go run github.com/gopernicus/gopernicus/workshop/gopernicus@v0.6.1 init --db pgx --module example.com/x` into a scratch dir; its `README.md` carries `@v0.6.0 guard --posture .`; `go run …@v0.6.0 guard --posture .` in it exits 0.
- **tag:** step 3 `workshop/gopernicus/v0.6.1` (no live leg: no store).

### PR-4 — the record, the repins, the counts, the handoff, the runbook (`docs/s2-follow-ups-record`)

Branch from `main` after steps 1, 2b, 2 and 3 are published and cold-verified.

- **files:** `RELEASING.md` (a new section at the top, directly under the title and ABOVE the
  retirement's `## Retired: Firestore stores …` note at `:3` — newest first: `## S2 follow-ups
  (secrets stores/pgx v0.2.1; authentication v0.22.0 + stores/pgx v0.10.1; workshop/gopernicus
  v0.6.1 — published <date>)` — the tag table with commits/PRs/cold-verification (the core row names
  PR-2a, PR-2b, PR-2c and PR-2d, cut at the later merge), "what shipped" per module, the behavior
  notes: the six reasons; the attribution rule and the pre-existing pgx row loss it closes (UA
  `\xff`, NUL `client_id`) plus the request-id widening; the failure-row budget with its number and
  the worst case; `ClientInfoMiddleware` and EVERY surface whose bucketing changes (`RateLimitByIP`,
  the oauth2 per-IP budget, the logic budgets' carrier dependence), "the rebucketing happens only
  when the middleware is mounted or the fallback applies", today's shared-bucket behavior named as the
  defect, the `TRUSTED_PROXY_COUNT` caveat, "host routes now add IP/UA to an immutable table; erasure
  is S3's", the production WARN; mandatory terminal-settlement conformance; "storetest at v0.22.0
  tightens two adapter contracts"; `AuthenticateClient` deprecated with its removal point; the PM-8
  rule; the session-precision diagnosis, the four insert paths and what "returns the session as
  persisted" means for a host-written caller; the secrets lock, the removed deadlock and the
  READ COMMITTED assumption; the workshop PATCH superseding the "next minor" promise; **Known**: the
  handler-level boundary + "grant validation split across handler and service"; the callback route
  limit as a follow-up; the 40001 case under repeatable read; the stale `owner_key_destroyed` key id;
  `external_login` per-cache dedupe; whatever R-A/R-B leave open), `README.md:92,121` ("twenty-three"
  → "thirty-three"), `plans/soc2-s2-audit-completeness-handoff.md` (`:3-4` the maintenance rule
  REPLACES "apply … on each host's own schedule"; `:24`, `:32`, `:184-186`, `:207-208`, `:232` the
  versions; `:174-175` the recipe), `workshop/documentation/docs/guides/operations-runbooks.md:68,70-72`
  (the request id exists since S2; the debug route is platform-admin gated), `examples/auth-cms/go.{mod,sum}`
  (authentication v0.21.0 → v0.22.0; `GOWORK=off go mod tidy`), `examples/production-baseline/go.{mod,sum}`
  (authentication v0.22.0 AND `GOWORK=off go get github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx@v0.10.1`
  — `go.mod:11` pins it directly and `tidy` will not raise it, SRE-F11; the production reference
  carries the fix), `plans/s2-follow-ups.md` (this file, executed copy with the status line brought
  to RELEASED), `plans/s2-follow-ups-release-manifest.json` (`go mod download -json` of the four
  tags). R-A: if (a), `pockets/authorization/BENCHMARKS.md:155-166` is reworded to "historical,
  owned runner" and `.github/scripts` stays; if (b), the two files are `git rm`'d, `:155-166` goes,
  and the matrix citations (`:134`, `:165`, `:232`, `:572`) are marked historical with the archive
  ref. Local only (gitignored): this file's status line, `roadmap.md:292-294` (the rule + the four tags).
- **behavior:** none in code.
- **BREAKING:** no.
- **tests:** n/a.
- **verify:**
  ```
  grep -n 'twenty-three' README.md                                                       # none
  grep -rn -i 'layering guards' README.md Makefile .github/workflows/check.yml           # all say thirty-three
  grep -n 'Maintenance rule' plans/soc2-s2-audit-completeness-handoff.md .claude/plans/soc2-readiness/roadmap.md
  grep -n 'own schedule\|no `ip_address`\|v0.21.0\|stores/pgx` \*\*v0.10.0\|workshop/gopernicus` \*\*v0.6.0' plans/soc2-s2-audit-completeness-handoff.md   # none
  grep -n 'planned, S2\|any authenticated session' workshop/documentation/docs/guides/operations-runbooks.md   # none
  grep -rn 'GOCACHE\|/tmp/' .github/workflows/                                           # none (PR-2b)
  git ls-files .github/scripts                                                           # R-A (a): the two files; (b): empty
  for m in examples/auth-cms examples/production-baseline; do (cd $m && GOWORK=off go build ./... && go test ./...); done
  grep -n 'stores/pgx v0.10.1' examples/production-baseline/go.mod
  sed -n '1,12p' RELEASING.md     # the new section sits above the retirement note
  make check
  ```

### Sequencing

1. The retirement is complete (#153, #154); every branch starts from `main` at or after 019e8a36.
2. **PR-2b first** among the authentication PRs: it is the CI-proof PR (ruling 7) and tag 2b must
   be cut before PR-2a lands the `stores/pgx` live case that needs the new core (so the v0.10.1 tree
   has no test its pinned v0.21.0 cannot pass at `GOWORK=off`). Before merge: the Linux-container
   `make test-stores` and the branch dispatch; after merge: the pgx leg on `main`, then tag 2b.
3. PR-1 and PR-3 are independent and branch any time; PR-2d is independent of everything. PR-2a
   merges after tag 2b; PR-2c branches after PR-2a merges (shared files). Each store tag follows its
   PR's live leg on merged `main`.
4. Tags: step 1 after PR-1 (secrets pgx live leg; Linux evidence from PR-2b's container run only if
   PR-1 merged before it); step 2b after PR-2b (pgx leg + the branch dispatch); step 2 at the LATER of
   the PR-2a/2b/2c/2d merge commits (SRE-F12) after the authentication pgx (bare + schema) and turso
   (local libsql + `LocalSQLite`) legs, the five positive PASS lines in every run, and the pgx
   Recorder live case; step 3 immediately after PR-3's merge. Each tag: annotated at the squash
   commit, pushed, THEN polled on the proxy `.info` (never before the push — a miss is
   negative-cached), cold-verified from a scratch consumer (`GOWORK=off`, empty cache,
   `proxy.golang.org`, `sum.golang.org`, `go mod verify`). Run each git step alone (the classifier
   blocks merge+tag+push in one line).
5. PR-4 after all four tags resolve; one more `live-stores` dispatch on `main` afterwards is
   optional (the branch dispatch already proved the `GOCACHE` removal).

## Release — the tag train

| step | tag | after | repins |
|---|---|---|---|
| 1 | `pockets/secrets/stores/pgx/v0.2.1` (PATCH) | PR-1 on `main` + the secrets pgx live leg (bare + schema; race tests `-count=20`) | none — core `pockets/secrets` v0.2.0, pgxdb v0.11.0 unchanged |
| 2b | `pockets/authentication/stores/pgx/v0.10.1` (PATCH) | PR-2b on `main` + the authentication pgx live leg (bare + schema) + the Linux-container `make test-stores` + the pre-merge branch dispatch with the authentication pgx legs green | none — core v0.21.0, pgxdb v0.11.0 unchanged |
| 2 | `pockets/authentication/v0.22.0` (MINOR) | PR-2a, PR-2b, PR-2c AND PR-2d on `main`, cut at the later merge + authentication pgx (bare + schema) and turso (local libsql, `LocalSQLite`) legs with the five positive PASS lines + the pgx Recorder live case | none — sdk v0.16.0, `pockets` v0.1.0 unchanged; `stores/pgx` v0.10.1 / `stores/turso` v0.9.0 keep core v0.21.0; turso is NOT retagged |
| 3 | `workshop/gopernicus/v0.6.1` (PATCH) | PR-3 on `main`, tagged immediately; cold `init --db pgx` proof after the posture-clean check at v0.6.0 | none — the emitted `go.mod` pins (sdk, pgxdb, turso) unchanged |
| — | PR-4 | steps 1-3 cold-verified | `examples/auth-cms` → authentication v0.22.0; `examples/production-baseline` → authentication v0.22.0 + `stores/pgx` v0.10.1 (explicit `go get`); examples are never tagged |

Annotated tags, not signed (owner ruling 2026-10-22). MVS: no published module requires any of the
four new versions until a host repins; the examples repin in PR-4 — production-baseline TAKES
`stores/pgx` v0.10.1 (PM-12, SRE-F11). Nothing retracts, nothing moves in `go.work`.

## Risks

1. **Unauthenticated callers can still append rows, now under a budget.** Every `oauth2_grant_failed`
   row and the callback `unknown_provider` row are bounded at ~60/min per IP per instance (the memory
   limiter is per instance; a fleet multiplies); each row is a synchronous insert bounded at 2 s on
   the host's shared pool, so a flood still costs DB writes at that rate; rows are append-only with no
   purge until S3. Known states the worst case; the risk is accepted BECAUSE hosts adopt after S3
   retention; the callback route limit is a follow-up.
2. **The attribution rule changes an existing surface.** A truncated 2048-byte `client_id` records
   blank now; a UA over 512 bytes is cut; a non-ASCII printable request id is kept rather than
   blanked; a value with a NUL, invalid UTF-8 or a control byte is blank instead of losing the row on
   pgx (today's behavior on the pocket's own routes). Named in RELEASING for every consumer.
3. **Two contract tightenings for third-party adapters.** Mandatory terminal settlement and
   "returns the session as persisted" (the 750 ns clock) are learned at `storetest.Run` time, not at
   compile time. RELEASING's "storetest at v0.22.0 tightens two adapter contracts" bullet + the
   `storetest` doc comment + the port sentences.
4. **`ClientInfoMiddleware` and the fallback change per-IP bucketing on host routes** from one
   shared bucket to per-client — the intended behavior; shared → per-IP can only admit more, so no
   host newly 429s, but the implicit global cap is gone and the `X-Real-IP`/leftmost-clamp spoof
   surface now applies to every host route. RELEASING names every surface and the `TRUSTED_PROXY_COUNT`
   caveat.
5. **The race tests hold a transaction open across a goroutine.** The `pg_blocking_pids` barrier
   proves the wait, the `t.Cleanup` rollback (registered after `openAndMigrate`) keeps a failed
   assertion from hanging the truncate, the 10 s context bound turns a wrong lock order into a
   failure, and the pre-fix runs (with the DSN) prove both harnesses reach their defect.
6. **The first Linux run of the later store legs.** `make test-stores` stops at the first failing
   module; the legs after authentication pgx have not run on Linux since 2026-07-07 (secrets pgx:
   never). The container run surfaces them on our schedule; any failure is its own finding and never
   holds tag 2b, whose gate is the authentication pgx legs alone.
7. **The fixture clock and the store fix must land together — and `make check` cannot catch a
   split.** `make check` is hermetic (no DSN, pgx conformance skips); if the storetest half merged
   alone, `make check` would stay green and only the live pgx leg would go red. PR-2b carries both
   halves; the proof is the pgx leg with the new clock failing against the old `sessions.go` and
   passing against the new one, plus the branch dispatch.
8. **Anchors move between PRs** (`recorder.go`, `constructor.go`, README between PR-2a and PR-2c;
   `storetest.go` between PR-2b and PR-2d). Every edit anchors on quoted text; PR-2c branches after
   PR-2a merges.

## Verification — how the owner proves it end to end

1. **PR-1:** on `main` pre-fix WITH the DSN, `TestLive_DeleteOwnerVersusPutSecret` fails with
   `sdk.ErrInvalidReference` and `TestLive_DeleteOwnerVersusRotation` deadlocks; on the PR branch
   both pass at `-count=20` in both legs, the owner key, both secrets, the third secret and zero
   audit rows remain, and the service-level `DeleteOwner` returns nil through its retry.
2. **PR-2a:** the pgx live case shows three rows with the bounded field blank and
   `WriteFailures()==0` in the bare and schema legs; in auth-cms (port 8082, `AUTH_OAUTH2_DEMO=1
   AUTH_DEBUG=1`) `-u 'oauth-demo-mcp:wrong'` and `-u 'someone-else:wrong'` each return 401
   `invalid_client`, the in-process dump (`TestDebugSecurityEventsRequiresPlatformAdmin`, extended)
   shows one row WITH `client_id` and one WITHOUT, ip/ua, no `client_secret`; a `%00`-suffixed id
   yields a row with no `client_id`, not a lost row; the 61st mismatch in a minute records no row and
   one `audit_rows_suppressed` WARN appears.
3. **PR-2b:** with the new clock and the OLD `sessions.go`, the two cases fail on this macOS laptop
   (`before=…750ns after=…000ns`); with the fix they pass; the Linux-container `make test-stores`
   log is kept (later legs triaged separately); the branch dispatch's `postgres-turso` job shows the
   authentication pgx bare and schema legs green on `ubuntu-latest` and no `/tmp/gopernicus-audit-s1`
   in the log; turso evidence is the local libsql leg and `LocalSQLite` only (CI has no Turso secrets).
4. **PR-2c:** `make run` in the baseline against a scratch Postgres; the README drive produces
   `authorization_denied` and `external_subject_provisioned` rows with non-empty `ip_address`/
   `user_agent`, the probe row with `user_agent=probe/1.0` and the response's `X-Request-Id`; the
   `curl -A $'\xff'` row EXISTS with a blank UA and `audit_write_failures` stays 0; with the `Use`
   line removed under production, the WARN prints once.
5. **PR-2d:** the five `--- PASS` lines appear in `TestReference`, `TestConformance_LocalSQLite`,
   `TestConformance_Turso` (local libsql), `TestConformance` (authmem) and `TestConformance_Postgres`
   (bare + schema); the storetest grep for `ErrUnsupported` is empty.
6. **PR-3:** a v0.6.0 scaffold is posture-clean BEFORE the expectation is written; the tag is pushed
   as the next step after the merge; the cold `init --db pgx` README carries `@v0.6.0 guard --posture .`
   and that command exits 0 (1 or 2 is a failure).
7. **Tags:** each `.info` resolves on the first poll after the push; the scratch consumer builds
   `pockets/secrets/stores/pgx/...`, `pockets/authentication/stores/pgx/...`,
   `pockets/authentication/...` and installs the workshop binary.
8. **PR-4:** the greps above; `GOWORK=off` builds of both examples at v0.22.0 (the baseline at
   `stores/pgx` v0.10.1); the RELEASING section sits above the retirement note; the handoff carries
   the rule, the versions and the recipe; the runbook lines are current.

## Decided by the review gate

1. **PR shape (PM-4, SRE-F12, SRE-F13):** PR-2 splits into PR-2a (denial paths + OAuth2 sibling +
   attribution/budget rules + README table), PR-2c (`ClientInfoMiddleware` + ARCHITECTURE row +
   `examples/README.md` + both examples + `production-posture.md`), PR-2d (R6 deletion + storetest
   text); one core tag v0.22.0 cut at the LATER of the PR-2a/2b/2c/2d merges; PR-2b absorbs the
   `GOCACHE` deletion so one branch dispatch proves items 9 and 10. Seven PRs, four tags, ten items.
2. **Attribution rule (SRE-F1, data-4/5/6, arch-5):** ONE exported `securityevent` helper, applied
   by `Record` to request/trace ids, IP, UA and every Details string value and by `auditReason`;
   NUL/invalid UTF-8/control ⇒ blank; UA cut at 512 bytes on a rune boundary; IP blank unless
   `netip.ParseAddr` accepts; pinned by the pgx live case and `curl -A $'\xff'`; RELEASING names the
   pre-existing pgx row loss. The pgx-only NUL divergence closes at the producer (data-6 option (a)).
3. **Flood posture (SRE-F2 (a), PM-9, SRE-F15, PM-7):** a per-IP failure-row budget (~60/min)
   through the root's limiter — the one `OAuth2Config.Limiter` already holds — on every
   `oauth2_grant_failed` row and the callback `unknown_provider` row; over budget ⇒ no row, a
   counter, one WARN per minute; the baseline observer counts and throttles; Known states the worst
   case and the alert. Decided 6 of rev 1 ("no new limiter") is withdrawn.
4. **Middleware posture (arch-1, SRE-F9/F10, PM-6, arch-2, SRE-F8):** `NewAuthenticator` builds the
   posture, `New` reuses it; the production once-per-process WARN for an unattributed request-id row;
   `RateLimitByIP` and the oauth2 per-IP budget fall back to `clientIP(r)`; the logic-side budgets
   cannot (G25) and are named; the ARCHITECTURE row carries four clauses; the `examples/README.md`
   bullet says "after the `TrustProxies` `Use`"; RELEASING names every rebucketed surface, the
   mount-only condition, today's shared bucket as the defect, the `TRUSTED_PROXY_COUNT` caveat and
   the immutable-table/erasure sentence.
5. **Session precision (data-1, SRE-F5, PM-5, arch-3, data-7):** one `persisted()` helper at every
   pgx session insert site (four in the tree: `Create`, `CreateForActiveUser`, `RedeemCode`,
   `insertRedemptionSession`), pre-truncation in `sessionArgs`, a 750 ns fixture remainder, the
   `Equal` assertion in `testActiveMintRoundTrip`, the port doc sentences on `repository.go` and
   `active.go`, the "tightens two adapter contracts" bullet; `stores/pgx` v0.10.1 kept. **Rejected
   alternative:** read `before` back through `Get` in the fixture — it would save the v0.10.1 tag but
   leave every insert path returning values it did not store, and a host comparing an insert's return
   with a later read would still see the split; the honest store contract wins.
6. **Secrets lock (data-1, SRE-F4, SRE-F17, data-9/10/12):** the `pg_blocking_pids` barrier, the
   `t.Cleanup` rollback after `openAndMigrate`, `repo.table`-qualified SQL, a `MapError`'d `Scan`,
   the DSN on the pre-fix line, READ COMMITTED stated and 40001 under Known, the rotation deadlock
   test and claim, the corrected README wording, the request-path deadline note (no `lock_timeout`),
   the service-level assertion.
7. **CI proof (SRE-F3, data-4):** the Linux-container `make test-stores` before the dispatch; the
   dispatch on the PR-2b branch before merge; the 2b gate = authentication pgx bare + schema legs;
   later-leg failures are their own findings; turso is proven only locally.
8. **Proof hygiene (data-3, SRE-F6):** positive `--- PASS` lines for the five named subtests instead
   of "zero unsupported skips"; `TURSO_AUTH_TOKEN=local`, the right `-run` names, authmem's
   `TestConformance`, `TestReference`; `storetest.go:260-261` replaced per arch-8.
9. **Deprecation shape (arch-4):** `AuthenticateClient` delegates (one comparison site), its
   `Deprecated:` text says what the Background path records, the removal point is "the core MINOR
   after `stores/turso` repins past v0.21.0".
10. **R1 = (a)** with PM-8's rule verbatim in `audit.go`, the README denial table and RELEASING
    Known, AND arch-6's placement debt under Known. **R2 = (a)** with the signal rationale (PM-7)
    kept transport-free in logic (arch-7).
11. **Handoff (PM-1):** PR-4 updates the S2 handoff per the maintenance rule (versions, recipe, the
    status line replaced); the plan's host sentence reworded.
12. **Hands-on (PM-2):** the baseline for the denial/`external_login` rows; auth-cms for the OAuth2
    mismatch; the admin read by `TestDebugSecurityEventsRequiresPlatformAdmin`.
13. **Bridge dedupe (PM-10):** the key gains the IP, examples only.
14. **Small items:** the explicit `go get …/stores/pgx@v0.10.1` (SRE-F11); the workshop tag pushed
    immediately and the posture-clean precheck (SRE-F16); the v0.6.1 PATCH supersedes "next workshop
    minor" (PM-13); the runbook lines (PM-14); the auth-cms anchor after `main.go:395` (PM-15);
    counts reconciled (PM-11); "production-baseline takes" (PM-12); grep + `git ls-files` for the
    `GOCACHE`/scripts checks (SRE-F13); the stale `owner_key_destroyed` key id under Known (data-11);
    ten call sites, not eleven (data-12).
15. **Sequencing addition (rev 2):** PR-2b merges and tags first; the `stores/pgx` Recorder live case
    (PR-2a) lands after tag 2b so the v0.10.1 tree carries no test its pinned core cannot pass.

## Decided (no ruling needed)

1. Seven PRs, four tags, no retraction, no migration, no port signature change; the retirement is
   complete, so nothing waits.
2. Item 1 is store-only (`stores/pgx` PATCH); the memory store has no window; no storetest case — the
   proofs are the two barrier-driven live tests, run once pre-fix with the DSN to show they bite.
3. The confidential-client mismatch records through a ctx-taking sibling `AuthenticateClientContext`
   on the oauth2 service (the recorder's single site), not through an inbound hook.
4. The handler's shape rejects (no/malformed Basic, body credentials, bad escapes; the
   `oauth2PublicResource` scope/audience checks, `actor_token_type`, an empty `token`,
   `readOAuth2Form`) stay unrecorded under the PM-8 rule; the placement debt is named.
5. An unknown provider on the unauthenticated login-start records nothing; the callback (budgeted)
   and the session-bound link-start record `unknown_provider` with NO `provider` key — never a literal
   `"unknown"`, which a host could legitimately name a provider.
6. `client_id` rides a row only when it names the configured confidential client; every attributed
   value is bounded by the one helper, never truncated except the UA.
7. The inactive-account denial is recorded in `RemovePassword`, not in `credentialRemovalProof`
   (shared with unlink and the start).
8. `ClientInfoMiddleware()` is the name (a middleware constructor, not a getter), exported from the
   authentication pocket's `inbound/http`, mounted by hosts with `router.Use` after `TrustProxies`
   and after the adapter exists; the per-route registrar stays as the safety net; the proxy posture
   lives on the `Adapter`, built by both constructors; ARCHITECTURE.md gets the "pocket-owned context
   stamp" row.
9. The R6 probes ride the authentication MINOR; stores are not retagged.
10. The workshop pin is a PATCH (v0.4.1 precedent); the install line moves to the tag the PR produces.
11. The maintenance sentence goes on the S2 handoff and the roadmap only (owner's wording); the S1
    handoff is not edited.
12. Item 9 is a store contract, not a Postgres flake: pgx returns the session as persisted
    (microseconds) from every insert path, the fixture clock carries a fixed 750 ns remainder so the
    cases are deterministic on macOS and Linux, and the assertions are untouched. `session.NewSession`
    keeps nanoseconds; turso and memory need no change. `stores/pgx` PATCH v0.10.1; the storetest and
    port-doc halves ride the core MINOR.
13. Item 10 is a deletion: `setup-go`'s cache owns `GOCACHE`; no replacement path is set; it rides PR-2b.
14. The failure-row budget lives in the Recorder (the ONE recording site, which already reads the IP
    and owns the once-per-minute WARN machinery); producers mark the rows; the root wires the limiter.
    The oauth2 package imports no limiter and no `logic/authentication`.

## Open rulings — YOUR CALL

- **R-A — the `authorization_cache_verify.py` + BENCHMARKS deletion.** PM says CUT from this train:
  the script reproduces the 109-case matrix `BENCHMARKS.md` still cites (`:134`, `:165`, `:232`,
  `:572`), it was never on the S2 Known list, and the retirement plan called it the owner's call.
  (a) Keep the script and its test; reword `BENCHMARKS.md:155-166` to "historical, owned runner"
  (**recommend** — nothing is stranded, no tag, one paragraph). (b) Delete both files and the recipe,
  mark the matrix citations historical with the archive ref (`archive/firestore-stores-2026-10-23`
  or a new `archive/authorization-cache-verify` tag) — `.github/scripts` then disappears.
- **R-B — are security/integrity patches an exception to "hosts adopt once at the end"? (SRE-F18)**
  The pgx row-loss fix (v0.22.0) and the secrets race (v0.2.1) do not reach gps-360-go, which mounts
  OAuth2, until the roadmap ends; the eventual adoption stacks S1/S2 runtime refusals and migrations
  into one deploy. (a) No exception (**recommend**: the maintenance rule stands; the risk is named in
  Known and the S3 retention dependency is part of why the flood posture is acceptable). (b) Hosts
  take v0.22.0 + the two store patches early, as a security exception written into the rule.

## Consultation notes

`lead-backend-engineer` (2026-10-23, rev 1, read-only). Verdict: **ship with edits** — "the shapes
you chose for items 2 and 3 are right; each item has holes that would close its Known entry on paper
only." Folded in rev 1 and carried: the sibling method (not a config hook), keep + deprecate
`AuthenticateClient`; `client_id` is attacker input (`%00` → NUL → JSONB rejects → row lost) — record
it only when it equals `ConfidentialClientID`, blank over the limit; unauthenticated callers can flood
the new rows (rev 1 accepted with the `invalid_state` precedent; rev 2 budgets them — SRE-F2);
malformed requests half-covered → R1; reasons mirror the wire codes; unknown provider keyed by
`linkUserID`, no literal `"unknown"`; the inactive denial in `RemovePassword`; `router.Use` after the
adapter exists, never via `pockets.Mount`; the posture on the Adapter; ARCHITECTURE row;
`context.go` doc; double-stamping fine; items 1 and 4 as sketched. No lead was re-consulted for rev 2
(the four reviews and the lead's rulings supersede).

## Recommended reviews

- `product-manager` — the R-A/R-B framing, the seven-PR shape, the handoff edits, the host-facing
  wording of the RELEASING section and the Known boundary.
- `platform-sre` — the budget shape in the Recorder, the Linux-container + branch-dispatch proof,
  the `NewAuthenticator` posture and the WARN, the tag train at the later merge, the explicit `go get`,
  the `live-stores.yml` header edit.
- `architecture-steward` — the ARCHITECTURE.md row's four clauses, the budget option on the Recorder
  (logic → `sdk/capabilities/ratelimiter`), the `Input` flag, the delegating deprecated shell and its
  removal point, the bridge's `ClientInfoFromContext` import in a host pocket.
- `data-integration-reviewer` — the barrier harness and the rotation deadlock test, the
  `MapError`'d `Scan`, the `persisted()` helper at four sites with pre-truncation, the 750 ns clock,
  the positive-PASS proofs, the `%00`/4 KB/`\xff` pgx live case.

## Notes

### Review gate record (rev 2, 2026-10-23)

Each folded change, attributed. "Accepted" means folded as asked; "ruled" means the lead's ruling
chose between reviewers.

- **product-manager** — PM-1 handoff update (accepted; ruling 11; rev 2 also found the per-host
  recap lines `:184-186`, `:207-208`, `:232` that name the same versions); PM-2 hands-on checks
  (accepted; ruling 12); PM-3 script deletion (ruled → R-A, owner); PM-4 PR split (accepted; ruling 1);
  PM-5 second contract tightening + rejected read-back alternative (accepted; ruling 5); PM-6
  production WARN, `production-posture.md` gap, `RateLimitByIP` fallback and RELEASING wording
  (accepted; ruling 4); PM-7 R2 rationale by signal and the callback row under Risk 1 (accepted;
  ruling 10 — the callback row is now budgeted); PM-8 the boundary as a rule (accepted verbatim;
  ruling 10); PM-9 the flood number (accepted; ruling 3 — now stated WITH the budget); PM-10 dedupe
  key gains IP (accepted; ruling 13); PM-11 counts (accepted); PM-12 "production-baseline takes"
  (accepted); PM-13 supersession recorded (accepted); PM-14 runbook drift (accepted, PR-4); PM-15
  auth-cms anchor `:395` (accepted).
- **platform-sre** — F1 attribution rule at the one site (accepted; ruling 2); F2 flood posture
  (ruled: the preferred fix (a), the per-IP budget, plus the stated worst case and the throttled
  observer from (b); ruling 3); F3 Linux proof and the 2b gate (accepted; ruling 7); F4 barrier,
  cleanup, service-level assertion (accepted; ruling 6); F5 helper at every insert site (accepted;
  ruling 5 — four sites in the tree, flagged); F6 Risk 7 reworded (accepted); F7 pgx live case +
  `\xff` run-and-look (accepted; ruling 2); F8 every rebucketed surface, the proxy caveat, the
  personal-data sentence (accepted; ruling 4 — with one correction: identifier management's budgets
  are per-user/per-target, `identifier_management.go:545,552`, not per-IP; the logic-side budgets
  cannot fall back under G25 and are named instead); F9 `NewAuthenticator` posture (accepted; ruling
  4); F10 the WARN (accepted; ruling 4; the empty-IP heuristic, no reader signature change); F11
  explicit `go get` (accepted); F12 core tag at the later merge (accepted; ruling 1); F13 `GOCACHE`
  deletion moved to PR-2b + grep/`git ls-files` checks + the header refresh (accepted); F14 scripts
  removal clean (info; R-A); F15 callback row only under the budget (accepted; ruling 3); F16 tag
  push immediately, posture-clean precheck, exit 2 is a failure (accepted); F17 README wording and
  the deadline note (accepted; ruling 6); F18 (ruled → R-B, owner). The "2am" scenario is answered by
  rulings 2 and 3 together.
- **architecture-steward** — arch-1 `NewAuthenticator` builds the posture (accepted; ruling 4);
  arch-2 the four clauses and "after the `TrustProxies` `Use`" (accepted verbatim; ruling 4); arch-3
  the port sentence (accepted; ruling 5 — on both `repository.go` and `active.go`, data's addition);
  arch-4 delegating shell + removal point (accepted; ruling 9); arch-5 one helper exported from
  `securityevent` (accepted; ruling 2); arch-6 R1 split along the hexagon (ruled: (a) across the
  board WITH the placement debt named under Known; ruling 10); arch-7 R2 (accepted; the inbound fact
  moves to README/RELEASING); arch-8 the storetest text replaced, `README.md:772` left as history
  (accepted); arch-9 the promotion watch item (recorded here: a second framework pocket needing
  IP/UA goes through the three graduation gates toward root `sdk`, never an authorization→
  authentication import). Guard impact as the steward assessed: no sdk change, no new pocket edge,
  G25 holds (the sibling takes `context.Context` only; the budget option imports
  `sdk/capabilities/ratelimiter`, a capability, from logic — allowed), P2 sees only the first `.Use(`,
  the §6 bullet is not a P-row.
- **data-integration-reviewer** — 1 three insert sites (accepted; ruling 5; four in the tree); 2
  the barrier harness, `t.Cleanup` order, `repo.table` (accepted; ruling 6); 3 the false-green verify
  lines (accepted; ruling 8 — every command corrected); 4 the `live-stores` proof (accepted; ruling
  7); 5 the UA bound (accepted; ruling 2 — 512 bytes on a rune boundary); 6 the NUL divergence
  (ruled: option (a), the Recorder rule covers Details; ruling 2); 7 the 750 ns remainder and
  load-bearing pre-truncation (accepted; ruling 5); 8 Risk 7 (accepted); 9 READ COMMITTED + 40001
  under Known (accepted; ruling 6 — no `SET TRANSACTION`); 10 the rotation deadlock test and claim
  (accepted; ruling 6); 11 the stale `owner_key_destroyed` key id (accepted → Known; ruling 14); 12
  nits — ten call sites, `MapError`'d `Scan`, README overclaim, re-anchor on #154 (all accepted).
  Mapping gaps: the port sentence on both repositories (accepted); "as persisted" narrowed to
  timestamps, `Profile` defaulting out of scope (accepted); error kind for NUL losses (moot once no
  producer can send one — noted).

### Other notes

- The S2 Known list (RELEASING `:339-359`) maps: the secrets race → PR-1; the denial paths, the
  OAuth2 mismatch and the bridged rows (PR-6 finding 4) → PR-2a/2c; the retirement's ruling 7 →
  PR-2d; the `guard@v0.5.0` pin → PR-3; `external_login` per-cache dedupe is NOT in scope (a design
  property, not a defect) and stays under Known. The S2 run-and-look notes about `append_only=
  unenforced` lines and the owner refusal naming the first store are observations, not defects.
- `oauth2/audit.go:29-32` says a malformed request "records nothing" — after PR-2a it carries the
  PM-8 rule and states what records and what does not, in one place.
- The retirement plan's A-4 grep allowlists `storetest.go:260-261` and `invitation_lifecycle.go:152-153`
  (the R6 comments). After PR-2d those lines are gone or replaced and the allowlist entries are unused.
- `live-stores.yml` is 149 lines on `main`; the `GOCACHE` override sits at `:46-47` between
  `permissions:` and `jobs:`; the stale secrets paragraph at `:24-28`. The 2026-08-15 memory note
  called the session case a "known postgres-turso CI precision flake"; it was a deterministic Linux
  failure masked by macOS's microsecond clock.
- `plans/s2-follow-ups.md` is the executed-copy path PR-4 writes; this file stays in
  `.claude/plans/soc2-readiness/` as the plan of record for the session (gitignored), with
  `s2-follow-ups.rev1.md` beside it.
- The pgx `stores/pgx` tree on `main` after PR-2a carries one DSN-gated test
  (`TestLive_RecorderBoundsClientAttribution`) that needs core ≥ v0.22.0; it runs in the workspace
  legs and is not in the v0.10.1 tag; the next `stores/pgx` tag repins past v0.22.0 anyway.

### Build notes (2026-10-23)

Recorded by PR-4; the design text above is unchanged.

- PR-2d's verify line `grep -rn 'ErrUnsupported\|skipUnsupported' pockets/authentication/stores/storetest   # none`
  should read "only the rule comment": the replaced comment at `storetest.go:260-261` names
  `errors.ErrUnsupported` by design, so the grep finds exactly that line.
- BSD grep (macOS) reads a pattern that begins with `--` as an option: the `--- PASS` / `--- SKIP`
  greps in the verify blocks need `-e` before the pattern (`grep -E -c -e "$PASSES"`).
- The storetest subtest is `ActiveSessionMint/ActiveUserRoundTrip`, not
  `UserAdmin/ActiveMintRoundTrip` (Design item 9, the PR-2b files and its `-run` pattern).
- Correlation ids did not widen: under the per-field ruling in Design item 2b the request and
  trace ids keep S2's rule byte for byte, so Risk 2's "a non-ASCII printable request id is kept"
  and PR-4's "plus the request-id widening" describe nothing that shipped.
- Tag 1 has no Linux run: PR-2b's branch was cut from `019e8a36`, before #156 merged, so the
  Linux-container `make test-stores` ran the pre-fix secrets store. The secrets race tests ran on
  macOS only (bare and schema, `-count=20`); the next `live-stores` run on `main` is their first
  Linux run.
- PR-4's verify line `GOWORK=off go build ./...` fails in `examples/auth-cms` before and after
  the repin: it pins `pockets/cms` v0.3.0 while `cmd/server/main.go` sets `cms.Config.RuntimeMode`
  (cms v0.4.0, S1). Both examples build, test and vet in workspace mode;
  `examples/production-baseline` also builds and tests at `GOWORK=off`. The cms pin is outside
  this train ("nothing else moves").
- Known items found in the build (RELEASING "S2 follow-ups", Known): the turso store's schema
  probe test drops the shared `schema_migrations` ledger (one libSQL server per full run); the
  authentication pgx upgrade tests need a fresh database.
