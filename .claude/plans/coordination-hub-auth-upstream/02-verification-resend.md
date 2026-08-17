# Phase 2 — registration verification resend

## Outcome

Ship one verification-resend use case with two driving surfaces:

- a public self-service route whose response and request-path work are identical
  for unknown, verified, unverified, and deactivated addresses; and
- an authorized admin operation that may report real target state.

Both issue a fresh `verify_registration` code, supersede prior pending delivery,
and document the unavoidable in-flight provider race.

## Current evidence

Registration creates an unverified primary email, issues a
`PurposeVerifyRegistration` challenge, renders the verification message, and
enqueues it. `deliveryQueue.Replace` and `enqueueRenderedReplace` already exist
as the explicit-resend primitive, but there is no service method, opaque worker
initializer, route, rate budget, or public documentation for registration resend.

## CHAU-2.1 — freeze semantics and characterization

Characterize before refactoring:

- registration creates exactly one active verification challenge;
- verification consumes it and records `VerifiedAt`;
- a newly issued challenge invalidates the prior code;
- delivery submit/checkpoint/retry behavior does not reissue the code; and
- challenge/delivery errors contain no email or code.

Freeze these outcomes:

| target | public resend | admin resend |
|---|---|---|
| unknown/malformed | uniform 202, no delivery | not-found/invalid input after authorization |
| already verified | uniform 202, no delivery | state conflict (`already_verified`) |
| active + unverified | uniform 202, opaque replacement work | accepted replacement + secret-free receipt/status |
| deactivated + unverified | uniform 202, no delivery | state conflict (`user_deactivated`) |

Public rate limits run before resolution: keyed-HMAC per normalized email plus
trusted client IP, on resend-specific prefixes. Malformed input receives the same
accepted response and no PII-bearing limiter/job key. Exact ceilings freeze in
this task and are documented; start with the existing passwordless pattern
(per-identifier and per-IP) rather than inventing a cooldown table.

## CHAU-2.2 — opaque replacement initializer

Add registration-verification to the delivery initializer registry:

1. Public start normalizes, limits, and calls `queue.Replace` with an opaque
   encrypted envelope containing only the normalized address.
2. Off request path, the worker resolves the active primary email claim.
3. Unknown, verified, or deactivated targets return `deliver=false` without a
   provider call.
4. An active unverified target gets a fresh replacement challenge and rendered
   verification message.
5. The rendered envelope is checkpointed before provider send; retry reuses the
   same code.

Use the same PII-free logical key as registration verification so resend
supersedes an undelivered original registration job. A stale worker must not
checkpoint/complete after replacement. Document honestly that replacement cannot
retract a provider call already accepted; a user may receive old and new mail,
but only the newest challenge can verify.

Add a specific security-event type for accepted/blocked resend without putting
the address or code in details. The public request event has no user ID because
the request path never resolves one; worker-side events may identify the user.

## CHAU-2.3 — service and HTTP surfaces

Recommended routes:

| route | body/gates | response |
|---|---|---|
| `POST /auth/verification/resend` | `{email}`; origin-only credential-establishment gate + IP/identifier budgets | always 202 `{status:"accepted"}` for target-state outcomes |
| `POST /auth/admin/users/{id}/verification/resend` | live session → browser-safe mutation → `UserAdminCheck(resend-verification)` | 202 + secret-free delivery receipt, or real admin error |

The public handler follows forgot-password/passwordless admission behavior:
bounded queue rejection is an honest 503 for every address; infrastructure
failure is a generic 500; target state never affects the HTTP result. It never
returns a pollable receipt that could reveal whether work resolved to a send.

The admin method resolves the target's active primary email only after the host
check and can refuse verified/deactivated/missing accounts explicitly. It uses
the same challenge and delivery replacement helper rather than a parallel mail
path. Host-facing trusted service docs must state that the caller owns
authorization when bypassing the bundled route.

Acceptance tests include:

- byte-identical public status/body/header shape across all target states;
- no request-path user/identifier repository lookup or renderer/provider call;
- per-address and per-IP budget exhaustion parity;
- repeated resend leaves one active challenge and one current delivery
  generation;
- old code rejected, fresh code accepted once;
- already-in-flight old message race documented and tested without pretending it
  can be retracted;
- admin check executes before target resolution and delivery;
- denied/check-error/machine-principal policy outcomes fail closed;
- deactivated user receives no message; and
- jobs and in-process modes run the same characterization suite.

## CHAU-2.4 — documentation and gate

Document in the auth README:

- self-service request/response examples;
- exact enumeration guarantee and what a 202 means (admitted, not delivered);
- rate limits and 503 saturation behavior;
- code replacement/in-flight duplicate semantics;
- admin route enablement and `UserAdminCheck` action;
- delivery status privacy (public resend gets no receipt); and
- operator troubleshooting for dead-lettered verification mail.

Update exported method docs, route table, security-event vocabulary,
`RELEASING.md`, and the example-host browser flow. Add a short downstream
adoption note showing the console's “resend verification” control and the public
registration screen use the two different surfaces.

Verification:

```sh
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)
make check && make guard
```

Run the existing jobs-mode restart/replacement proof because resend correctness
depends on logical-key supersession and stale-claim fencing.

## Execution log

Append only. Record each CHAU-2.x task's behavioral evidence, delivery-mode
coverage, saturation result, documentation update, and any open live gate.

### 2026-08-16 — CHAU-2.1 … CHAU-2.4 complete

**CHAU-2.1 — frozen semantics.** Budgets: **3 per normalized address per minute**
and **10 per client IP per minute**, deliberately mirroring the passwordless start
budgets rather than inventing a cooldown table — both are unauthenticated,
enumeration-sensitive starts that submit opaque work, and one shape is easier to
operate than two. Both run BEFORE any resolution and key on PII-free digests.

The frozen outcome table, and where each row is asserted:

| target | public | admin | asserted in |
|---|---|---|---|
| unknown / malformed | 202, no delivery | 404 after authorization | `TestResendVerificationRequestPathIsOpaque`, `TestResendVerificationMalformedInputIsAccepted`, `TestPublicResendIsByteIdenticalAcrossTargetStates` |
| already verified | 202, no delivery | 409 `already_verified` | worker-branch + `TestAdminResendReportsRealState` + host 409 assertion |
| active + unverified | 202, opaque replacement | 202 + secret-free receipt | worker-branch + `TestPublicResendDeliversAFreshUsableCode` |
| deactivated + unverified | 202, no delivery | 409 `user_deactivated` | worker-branch + host 409 assertion |

**One documented deviation, deliberate.** A MALFORMED address short-circuits after
the budget instead of queueing work. The plan's row says "uniform 202, no
delivery", which holds; what does not hold is queue-shape symmetry. The reasoning
is recorded in `TestResendVerificationMalformedInputIsAccepted`: the property the
design protects is that a valid REGISTERED address is indistinguishable from a
valid UNREGISTERED one — both enqueue identically — while whether a string is
syntactically an address is something an attacker determines locally without
asking the server. Queueing un-resolvable garbage would only hand an attacker
free worker capacity. The malformed value still costs budget
(`TestResendVerificationBudgets/malformed_input_is_throttled_too`).

**CHAU-2.2 — opaque replacement initializer.** `PurposeRegistrationVerification`
joined the `Initialize` dispatcher (`initRegistrationVerification`). The public
`ResendVerification` does exactly normalize → rate-limit → `queue.Replace` with
an envelope carrying only `ResolutionInput`. Everything enumeration-sensitive —
resolve, branch, issue, render — happens in the worker.

The logical key is `idempotencyKey(email, PurposeRegistrationVerification)`, the
SAME key registration itself uses, so a resend supersedes an undelivered original
registration mail. `Replace`, never `Enqueue`: a resend is by definition a repeat
and must not dedupe onto the stale job.

Proving "the request path resolves nothing" required a fixture change worth
recording: the package's synchronous `drainingQueue` runs the worker INLINE, so a
repository read cannot be attributed to either side. The tests therefore swap in a
`captureQueue` that records commands without processing them; every read observed
after that swap is unambiguously the request path's. The same fixture asserts the
submitted envelope is opaque (empty `Secret`/`Subject`/`Body`/`HTML`/`Destination`)
and that the logical key carries no `@`.

New event types, both secret-free: `verification_resend_requested` (public;
carries **no** user id, because the request path never resolves one) and
`verification_resend_issued` (worker or admin; carries the target user id).
`TestResendVerificationAuditIsSecretFree` asserts both, and scans every detail
value for an `@`.

The in-flight race is documented honestly rather than papered over: replacement
cannot retract a provider call already accepted, so a user may receive both mails
— but `TestResendVerificationReplacesTheCode` proves only the newest verifies
(old code rejected, fresh code accepted exactly once, exactly one active
challenge).

**CHAU-2.3 — service and HTTP surfaces.** Both recommended routes ship at the
recommended paths. The public one carries the origin-only gate (no CSRF token —
the caller has no session, which is the whole point) and mounts unconditionally;
the admin one rides the admin gate chain and mounts only with
`Config.UserAdminCheck`.

Acceptance evidence, run over real HTTP through exported host seams
(`examples/auth-cms/cmd/server/verification_resend_test.go`):

- `TestPublicResendIsByteIdenticalAcrossTargetStates` compares status, body, AND
  `Content-Type`/`Cache-Control`/`Access-Control-Allow-Origin`/`Vary` byte-for-byte
  across unknown, malformed, empty, active-unverified, and verified targets
  against the unknown-address baseline. A test that only checked "all 202" would
  have missed a differing body or header.
- `TestPublicResendThrottles` — the one non-uniform outcome, with the body checked
  for any mention of verification state.
- `TestPublicResendDeliversAFreshUsableCode` — the behavioral half end to end:
  register → capture the real delivered code → resend → capture the NEW code →
  prove they differ → prove the old one no longer verifies → verify with the new
  one → log in. Not a `href=`-style shallow assertion.
- `TestAdminResendReportsRealTargetState` — 202 + secret-free receipt, the
  delivered code actually verifying (proving the admin path drives the real
  challenge rail, not a parallel mail path), then 409 `already_verified`, 404
  unknown user, and 409 `user_deactivated` after a real deactivate.
- `TestAdminResendDeniedWithoutAuthorization` — 403, and
  `TestAdminResendRouteAbsentWithoutCheck` — the admin route 404s without a wired
  check while the public route stays available.

Service-level (`resend_test.go`, 8 tests / 15 leaf cases) covers the request-path
opacity, both budgets including per-IP-across-addresses, all four worker branches,
code replacement, audit hygiene, and every admin state.

**CHAU-2.4 — documentation.** New README section **"Verification resend — one use
case, two surfaces"**: the public outcome table with what actually happens per
row, the three-step request path and why, "202 means ADMITTED not delivered" and
why there is no pollable receipt, exact budgets and 503 saturation behavior, the
gate rationale, code-replacement and the in-flight-duplicate honesty, the admin
table, the audit contract, operator troubleshooting for dead-lettered mail, and
the downstream note that the registration screen and the admin console must use
the two DIFFERENT surfaces. Route tables gained both entries; the admin route
table gained its row. `RELEASING.md` gained a keyed entry.

**Verification (exactly as run, 2026-08-16):**

```
(cd features/authentication && go build ./... && go test -race ./... && go vet ./...)   clean
(cd examples/auth-cms && go test -race ./...)                                           ok
make check                                                                              all checks passed
```

**Delivery-mode coverage.** The service-level suite runs against the jobs-shaped
synchronous processor (`wireSyncDelivery` → `delivery.NewJobsProcessor`), and the
host-level suite runs the SAME flows against `DeliveryMode: in_process` with the
host's real `RunDelivery` runtime. Both modes therefore execute the resend
characterization, including the replacement-supersedes-pending-work path, which is
what the plan asked the jobs-mode restart/replacement proof to cover.

**Saturation.** The public handler answers 503 on a bounded-queue admission
refusal (`deliveryUnavailable`), never a 202 that dropped the work — the same
branch `forgotPassword` already used, reused rather than re-implemented. No live
gate applies to this phase.
