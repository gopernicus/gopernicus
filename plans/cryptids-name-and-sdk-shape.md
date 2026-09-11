# Cryptids name and SDK package shape

Status: COMPLETE — 2026-09-10. The owner approved the root-SDK shape;
implementation and final names are in [sdk-root-promotion.md](sdk-root-promotion.md).
Parent: [framework-audit.md](framework-audit.md).

## Owner direction and scope

Restore `sdk/foundation/cryptids` as the crypto package name. The owner's intended
meaning is "cryptography tidbits"; retain the playful name as part of the
framework's vocabulary. This supersedes S4's naming choice, not its correctness
fixes or API simplifications. Keep ID generation separate during this rename.

The owner also asks whether id/identity should merge, or whether validation,
pointer, slug, identity, id and environment should move into root sdk. Assess
this as a design question, including actual dependencies, initialization and
namespace tradeoffs. Do not implement the broader relocation before agreeing on
its shape. Existing architecture rules are reviewable, not a veto on the owner.

## Preconditions and tasks

- Branch: firestore-authentication; preserve all existing audit/Firestore changes.
  Source/document snapshot: /tmp/gopernicus-cryptids-rename-baseline.json.
  No consumer edits, module/version pins, generated edits, commits or releases.
- Mechanically rename the crypto directory, package declarations, imports and
  qualified references. No crypto behavior or wire/persisted format changes.
- Update current docs, AUDIT-004's final migration destination, an explicit
  AUDIT-008 follow-up for checkouts using cryptography, and release/handoff notes.
  Preserve historical plan paths and annotate their superseded naming decision.
- Read-only named backend review and local dependency/size evidence inform the
  package-shape recommendation. Disposable experiments stay in /tmp.
- Format migrated Go files with /Users/jrazmi/go/bin/goimports; run SDK and
  affected module build/test/vet via make check, plus docs-build. Existing tests
  cover a name-only change; no new behavioral tests are required. Record skips.

## Result

Restored foundation/cryptids and migrated all current Go callers, tests and
canonical docs. The package comment records "cryptography tidbits". The temporary
cryptography directory is gone; there is no compatibility forwarding layer. The
empty SHA256 error now says cryptids, retaining sdk.ErrInvalidInput. All crypto
algorithms, key handling, persisted formats and other S4 simplifications remain.
AUDIT-004 shows the final namespace; AUDIT-008 describes the temporary-name
migration. S4 plan filenames/historical inventories are preserved and annotated.

## Approved SDK shape (proposal checkpoint)

The owner's small-package concern is reasonable. Module download size is not a
reason for this fragmentation: all these packages already live in the SDK module.
Public naming and dependency/initialization cost are the useful criteria. A
package can earn its place through a coherent vocabulary, not only line count.

Parent recommendation:

| Area | Proposed home / illustrative API |
|---|---|
| pointer | root sdk.Deref, sdk.DerefOr |
| slug generation | root sdk.Slugify |
| ID generation | root sdk.IDGenerator, sdk.IDGenerateFunc and explicitly named generation strategies/defaults |
| identity vocabulary/context | root sdk.Principal, sdk.WithPrincipal, sdk.PrincipalFromContext, sdk.IdentityInfo, sdk.IdentityAddress, sdk.IdentityResolver |
| validation checks | keep foundation/validation: validation.Email/Min/Slug convey that these check input; shared Violation/ValidationError already live in root |
| environment configuration | keep foundation/environment: loading, tags, mode and secrets form a coherent configuration API; environment.LoadPath/Mode/Secret carry useful context |
| crypto | keep foundation/cryptids, with the owner-selected name |

Do not merge entity-ID generation into an identity subsystem: it creates keys
for arbitrary entities and need not know anything about caller identity. Both
can instead be independent root files with explicit public names. The root
identity accessor would match RequestIDFromContext naming; private context keys
must not collide. Physical promotion is needed for validation if chosen later:
a root forwarding import would cycle because validation already imports root.

Named backend read-only review agrees on pointer and optional slug promotion,
and on retaining validation/environment; it prefers retaining id/identity as
separate named packages for cohesion. The parent favors promoting both with
explicit ID/Identity names to meet the owner's reduced-fragmentation goal. This
is an API design judgment, not a correctness requirement. The public name list
above records the proposal checkpoint. The owner subsequently approved this
shape; sdk-root-promotion.md defines and implements the exact public names.

### Concrete size/dependency evidence

AST inventory: current root has 26 exported top-level symbols (methods excluded).
The six packages add 66: validation 30, environment 16, identity 10, id 7,
pointer 2, slug 1. All are stdlib-only. Root currently imports context/errors/
strconv/time. Promotion adds dependencies including crypto/rand, regexp, net/mail,
net/url, os and reflection. Existing package initialization includes validation's
two regexes and ID's default generator construction; import does not itself read
environment files or generate IDs.

Disposable physical-promotion probe, Go 1.26.1 darwin/arm64, stripped binaries:

| Shape | Small executable | HTTP executable |
|---|---:|---:|
| Current root | 1,641,490 bytes | 3,599,298 bytes |
| Root + pointer/slug/id/identity | 1,825,586 (+184,096) | 3,599,378 (+80) |
| Root + all six | 2,252,850 (+611,360) | 3,803,010 (+203,712) |

The small program prints sdk.ErrInvalidInput; the HTTP one registers a ServeMux
handler and prints the mux type, without listening. No promoted helper is called;
existing initializer dependencies explain why unused functions do not imply zero
cost. Go linker reachability starts with main and initialization
(https://go.dev/src/cmd/link/internal/ld/deadcode.go). This is not a startup
benchmark or a GPS360 binary estimate. A host already using these features can
have much less incremental cost. Size is not the main argument for keeping the
packages apart.

Probe source/inventory/commands/results are in /tmp/gopernicus-sdk-shape-probe/
README.md, inventory.go, inventory.json, sizes.json, baseline/, common/, promoted/
and cmd/. No prototype source was applied to root SDK. Reproduce from that temp
module with GOWORK=off and the usual GOCACHE:
`go build -trimpath -ldflags='-s -w' -o ./bin/ ./cmd/...`; every binary executed
successfully. A first setup used a relative source path from the wrong directory;
corrected to the absolute SDK path before the successful probe builds.

## Verification

- Formatter: goimports on 32 migrated Go paths; final scoped git diff --check PASS.
- `GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache make check` with authorized
  loopback access: PASS across all 42 modules (go vet ./..., go build ./...,
  go test ./...), template/asset checks, scaffold tests, tagged compile-only vet
  and architecture guards. Log: /tmp/gopernicus-cryptids-make-check.log.
- Final diagnostic-prefix adjustment: `go test ./sdk/foundation/cryptids -count=1`
  with the same GOCACHE PASS. No additional behavioral tests were needed for the
  mechanical rename.
- `make docs-build` (pnpm typecheck/build): PASS. Log:
  /tmp/gopernicus-cryptids-docs-build.log. Optional Docusaurus update notification
  reports a local config warning after the successful build, as in prior passes.
- No old import path or package qualifier remains in current Go/template source.
  Historical plans and AUDIT-008 deliberately name the temporary package.
- No consumer build/upgrade, live PostgreSQL/Turso/Firestore test, release or
  deployment. Existing live suites remain env-gated. No new behavior requires a
  live backend for this rename; published module coordination remains future work.

No unresolved verification failure. Cryptids naming is settled. Root SDK shape is
implemented in sdk-root-promotion.md; S8 cacher/ratelimiter/events is next.

## Changed-file inventory

Compared against /tmp/gopernicus-cryptids-rename-baseline.json, plus newly created
files for this task. Includes both sides of the six-file directory rename. Preserve
pre-existing audit/concurrent changes; the whole HEAD diff is not this rename.

50 paths:

- `ARCHITECTURE.md`
- `AUDIT.md`
- `RELEASING.md`
- `examples/auth-cms/cmd/server/demo.go`
- `examples/auth-cms/cmd/server/jobs_delivery_live_test.go`
- `examples/auth-cms/cmd/server/jobs_delivery_proof_test.go`
- `examples/auth-cms/cmd/server/jobs_delivery_retry_test.go`
- `integrations/cryptids/golang-jwt/README.md`
- `integrations/cryptids/golang-jwt/golangjwt.go`
- `plans/cryptids-name-and-sdk-shape.md`
- `plans/framework-audit-identity-cryptids.md`
- `plans/framework-audit.md`
- `plans/identity-cryptography-cleanup.md`
- `pockets/authentication/README.md`
- `pockets/authentication/authentication.go`
- `pockets/authentication/internal/inbound/authentication/token_test.go`
- `pockets/authentication/internal/logic/authsvc/delivery.go`
- `pockets/authentication/internal/logic/authsvc/machine.go`
- `pockets/authentication/internal/logic/authsvc/oauth_test.go`
- `pockets/authentication/internal/logic/authsvc/service.go`
- `pockets/authentication/internal/logic/authsvc/token_test.go`
- `pockets/authentication/internal/logic/delivery/command/codec.go`
- `pockets/authentication/internal/logic/delivery/command/codec_test.go`
- `pockets/authentication/internal/logic/delivery/command/engine.go`
- `pockets/authentication/internal/logic/delivery/commandcodec.go`
- `pockets/authentication/internal/logic/delivery/dispatcher_test.go`
- `pockets/authentication/internal/logic/delivery/jobsprocessor.go`
- `pockets/authentication/internal/logic/delivery/processor_char_test.go`
- `pockets/authentication/internal/logic/delivery/service.go`
- `pockets/authentication/internal/logic/delivery/service_test.go`
- `pockets/authentication/internal/logic/invitationsvc/service.go`
- `pockets/authentication/internal/logic/invitationsvc/service_test.go`
- `pockets/authentication/security_hmac.go`
- `pockets/authentication/security_test.go`
- `sdk/README.md`
- `sdk/foundation/cryptids/aesgcm.go`
- `sdk/foundation/cryptids/aesgcm_test.go`
- `sdk/foundation/cryptids/cryptids.go`
- `sdk/foundation/cryptids/jwt.go`
- `sdk/foundation/cryptids/sha256.go`
- `sdk/foundation/cryptids/sha256_test.go`
- `sdk/foundation/cryptography/aesgcm.go`
- `sdk/foundation/cryptography/aesgcm_test.go`
- `sdk/foundation/cryptography/cryptography.go`
- `sdk/foundation/cryptography/jwt.go`
- `sdk/foundation/cryptography/sha256.go`
- `sdk/foundation/cryptography/sha256_test.go`
- `sdk/foundation/environment/secret.go`
- `workshop/documentation/docs/integrations/catalog.md`
- `workshop/documentation/docs/sdk/foundation.md`
