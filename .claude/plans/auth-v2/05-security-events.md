# Phase A5 — security events: the synchronous audit rail

Status: RATIFIED (cut from design §5.1 — §5.2 is DEFERRED per ratified AV10)
Executor model: opus
Depends on: A2 + A3 (this phase wires audit writes across their ops; A4's
token issuance is covered too since A4 lands before this in practice —
if A4 has not run, log the gap and wire its ops when it does).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §5.1 (the
audit rail: entity, vocabulary, synchronous never-fail writes, ratified
AV9 optionality), §5.2 ONLY as a boundary marker — **nothing from §5.2
lands here**: no `Events []events.Record` field, no appender, no outbox,
no `sdk/events` import. §9 (crud-typed List).

## Work items

1. **`logic/securityevent`** (public rim): append-only
   `SecurityEvent{ID, UserID (optional), Actor Principal (optional),
   EventType, EventStatus, Details map[string]any, IPAddress, UserAgent,
   CreatedAt}` + repository — `Create`, `List`
   (`sdk/crud.ListRequest`-typed; filterable by user/type/status/time
   window; **port doc pins `ORDER BY created_at DESC, id DESC` — the id
   tiebreak is contractual**, plan-cut amendment). No Update/Delete
   methods exist (append-only is structural).
2. **Event vocabulary constants** (design §5.1, salvaged): register /
   login / logout / password_change / password_reset / email_verified /
   oauth_login / oauth_register / oauth_link_verified / oauth_linked /
   oauth_unlinked / apikey_auth / token_issued / invitation events
   (A6 wires its own); statuses success / failure / blocked.
3. **Synchronous never-fail writes** from every sensitive `authsvc` op —
   v1's (register, login success/failure/blocked, logout, verify,
   password change/reset) + A2's OAuth ops + A3's `apikey_auth` (all
   three branches — the revoked-key `blocked` event carries
   service-account attribution, which is exactly what A3's pinned
   GetByHash contract exists to enable) + A4's token issuance. One
   private `recordSecurityEvent` helper; failures are logged at WARN and
   NEVER fail the auth flow (the design's non-negotiable). **Log + content
   hygiene (plan-cut amendment, SRE): the WARN line carries coarse fields
   only — event_type, status, error kind — never the full event; and
   `Details`/IP/UA carry identifiers and key PREFIXES only — raw API
   keys, JWTs, session tokens, passwords, and OAuth tokens NEVER land in
   audit content.** Nil `Repositories.SecurityEvents` → helper is a no-op
   (AV9: no audit trail, documented).
4. **Client-info capture (plan-cut amendment — supersedes cut refinement
   6; the identity-key precedent doesn't transfer because it is written
   AND read inside `authsvc`, while this carrier is written from
   `internal/inbound/http`):** an **EXPORTED-from-authsvc**
   `WithClientInfo(ctx, ip, ua)` setter, populated at **ONE blanket
   point** — a feature middleware applied in `http.Mount` over ALL
   routes, explicitly including unauthenticated ones (failed login,
   register, and the OAuth callback all produce audit rows). **The
   carrier is the single source of truth for IP**: login's rate-limit
   key reads its IP from the same carrier value (retire any separate
   clientIP plumbing so there is exactly one write point and one read
   path). No sdk change.
5. **`storetest` sub-runner**: Create + List (filters + the pinned
   ordering + crud pagination, incl. a same-`created_at` collision case
   asserting identical order and `NextCursor` across implementations) +
   **a `Details` round-trip case — nil, empty, and populated maps: store
   accepts all three and reads back a NON-NIL empty map for nil/empty,
   identically across backends** (plan-cut amendment; the stores persist
   `'{}'` or NULL, their choice, but the read-back contract is uniform).
   Port doc note for the store phases: the dynamic `List` filter WHERE is
   always parameterized, never concatenated. Reference in-memory impl
   sorts the full population then pages.
6. **Tests**: every sensitive op records the right type/status (spy
   repo); a failing repo does not fail login/register (the never-fail
   property, asserted); nil-repo no-op path.

Explicit non-goals (deferred rail, ratified AV10): no outbox records, no
`OutboxAppender`, no `features/events` anything. No HTTP read surface
(design §11).

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
make check
```

Boundary grep (import-anchored, plan-cut form — re-run at milestone close
per the overview, since A7a/A7b add store code after this phase):
`grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/\(sdk/events\|features/events\)' features/auth/`
→ empty (the deferred rail must leave zero trace). Rule-6 grep: the
import-anchored form from the overview.

## Real-interaction check

Standing check (a) + check (b). Audit-row visibility over real HTTP is
A9's protocol (the host wires an in-memory security-events repo + the
dev-only debug route there); this phase closes on the spy-repo test
evidence plus a log-line capture of one real WARN-on-failing-repo case
from the unit suite.

## Execution log

(append dated entries here)
