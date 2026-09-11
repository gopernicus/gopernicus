# Identity and cryptography audit implementation

Root SDK update (2026-09-10): pointer/slug/ID/identity primitives now live in
root SDK. This record preserves the earlier checkpoint and its original paths;
[sdk-root-promotion.md](sdk-root-promotion.md) and AUDIT-009 give the final names.

Naming update (2026-09-10): the owner restored foundation/cryptids as the crypto
namespace ("cryptography tidbits"). This record's cryptography paths describe the
S4 checkpoint; [cryptids-name-and-sdk-shape.md](cryptids-name-and-sdk-shape.md)
records the rename. S4's other behavior/API decisions remain implemented.

Status: COMPLETE — 2026-09-09. The owner authorized the recommendations in
[S4](framework-audit-identity-cryptids.md), including cleanup. Parent:
[framework-audit.md](framework-audit.md).

## Decisions and scope

- Password policy/configuration belongs to the host. Preserve existing host
  password configuration, hasher selection, and bcrypt cost options. Bcrypt's
  72-byte algorithm limit is enforced for both hashing and verification; no new
  password complexity/minimum-length policy is introduced by SDK/integrations.
  Inspection found authentication's lengths hardcoded. Add optional
  Config.ValidatePassword func(context.Context, string) error, replacing all
  built-in length gates when set; nil preserves 15–64 code points/256 bytes.
  Apply it through the existing shared register/set/change/reset validator,
  before the independently configured breach check. Preserve callback errors
  and existing-password verification behavior. Hosts own custom bounds/policy.
  Classify bcrypt's length refusal with sdk.ErrInvalidInput so multibyte input
  outside the adapter's limit returns 400 rather than a server error; the
  integration gains an SDK requirement, not a dependency on authentication.
- Split SDK foundation/cryptids into foundation/id (Generator, GenerateFunc,
  NewGenerator, NanoID, Database, Alphabet, DefaultLength) and
  foundation/cryptography (Encrypter, AESGCM/NewAESGCM, SHA256, JWTSigner).
  Remove the old package, SDK HS256 and its error symbols, and SHA256Hasher.
  Database becomes a function. Integration module paths remain unchanged.
- Preserve default ID alphabet/length, configured generation seams, and the
  separate authentication bearer-secret generators. Reject non-ASCII custom
  alphabets. Keep identity vocabulary/resolver, remove unused ResolveAll, return
  zero/false for principals missing Type or ID, and describe endpoint-owned
  access policy and identity projection versus notification eligibility.
- Use cipher.NewGCMWithRandomNonce for AES, preserving the existing raw-base64url
  nonce/ciphertext/tag format, key bytes, empty-input behavior, and short-input
  sdk.ErrInvalidInput classification. Keep SHA-256 lowercase hex/empty error
  semantics as a function and remove stored hasher pointers in consumers.
- Retain JWTSigner as the inward port with one implementation, golang-jwt.
  Sign owns exp/iat; expiresAt is authoritative. Verify requires object claims
  and numeric exp, validates optional numeric nbf/iat (explicit null is invalid),
  applies 60 seconds of tolerance, and preserves strict encoding and algorithm
  pinning. Expiry-required is this port's contract. Use library validation plus
  small necessary checks, not another handwritten token parser.
  Verification also rejects NumericDates outside calendar years 0001–9999:
  regression tests reproduced upstream float/time overflow accepting extreme
  future nbf/iat claims. This narrow range guard follows library parsing.
- golang-jwt retains its string-key constructor and method option. Validate
  supported HS256/384/512 selection and 32/48/64-byte key sizes after options;
  uninitialized signers return errors. Preserve effective key bytes in host
  migration. Replace old-SDK crossverify tests with a genuine legacy fixture
  and independent library interoperability tests.
- Update all local callers/tests and current docs. auth-cms uses the integration
  at composition, with its direct module requirement/local replacement using
  existing example conventions. No SDK/pocket dependency gains. Preserve all
  historical plan/release records; add AUDIT-004 and an unreleased release note.

## Preconditions and ownership

Branch/base: firestore-authentication / 82aa19f7. Prior S2/S3 audit code/docs
and concurrent Firestore store changes are dirty; preserve them. SDK cryptids,
identity, and related integration code are clean at start. Go 1.26.1; no root
go.mod. Formatter: /Users/jrazmi/go/bin/goimports. Go cache:
/tmp/gopernicus-audit-s1.aMLwCQ/cache. No real dotenv/credentials, consumer edits,
live databases/migrations, generated source edits, release tags, or module-path
renames. Reference consumers remain read-only.

The owner's approval supersedes the old blanket stdlib-default prescription:
SDK stdlib-only does not require maintaining a custom JWT implementation.
Update canonical architecture wording to capture that decision.

## Tasks

1. **SDK and caller migration (parent):** implement id/cryptography/identity,
   focused regressions including legacy AES compatibility; migrate SDK consumers,
   pockets/stores/examples and google-uuid imports without changing domain logic.
   Use a mechanical qualified-symbol migration, then inspect shadowing and remove
   hasher fields. Do not edit the implementer's two integration directories.
2. **JWT/bcrypt integrations (named implementer):** own only
   integrations/cryptids/{golang-jwt,bcrypt}, including tests/fixture. Follow the
   above token contract, preserve host-configurable bcrypt cost, verify algorithm
   limits consistently, and test malformed claims, time tolerance, algorithm/key
   boundaries, zero value, and stored token compatibility. Parent creates SDK port.
3. **Contract review (named lead-backend-engineer):** read-only check that actual
   password policy is host configurable and the proposed migration preserves
   inward boundaries. Flag necessary corrections, no separate pocket redesign.
4. **Documentation and runtime proof (parent):** current architecture/SDK/pocket/
   example docs, AUDIT-004 with full API/key-byte/token-policy migrations, release
   link, shared handoff. Exercise the real auth-cms sign/verify middleware path
   with dummy data/local HTTP, no datastore; preserve encryption/digest formats.
5. **Final review/checks:** named platform-sre read-only reviews completed token
   implementation/fixture and migration docs; parent runs SDK and affected module
   build/test/vet, full make check after service preflight, docs build, formatter
   and diff checks. Do not change generated artifacts to hide drift. Record any
   sandbox listener reruns and skipped live/consumer checks precisely.
6. **Verification-discovered jobs defect (named implementer):** the full gate
   exposed a pre-existing memstore replacement-order defect; a 50-run targeted
   reproduction also failed. Random job IDs are independent of S4's ID package.
   Equal/reversed wall-clock timestamps allow GetLatestByKey to return an older
   superseded generation. Keep its timestamp/ID read contract, but ensure fresh
   same-key insertion timestamps strictly increase under the existing mutex.
   Own only pockets/jobs/memstore/fenced.go and a focused regression test; no
   sleeps, public clock/config abstraction, SQL/schema changes, or weakened
   assertions. Test deliberately reversed timestamps/ID order and replacement
   conformance, including race checks. Carry datastore precision and durable
   generation ordering into the later jobs audit.

## Execution record

Completed all six tasks. The named lead-backend-engineer reviewed host policy
ownership/error classification, the implementer completed the JWT/bcrypt and
bounded jobs corrections, and platform-sre reviewed the final adapters and
AUDIT-004 without material findings. The parent implemented SDK/caller changes,
password policy, runtime proof, and documentation.

### Result and compatibility

- All selected SDK package/API changes and local callers are migrated. SDK still
  has no module requirements; pocket cores still require only SDK. Published
  integration paths remain unchanged. No handwritten SDK JWT implementation or
  stateless hasher object remains.
- Host ValidatePassword replaces hardcoded creation limits; nil preserves the
  defaults. Existing-password verification bypasses creation policy. Breach
  checking, hasher choice, and bcrypt cost remain independently host configured.
- JWT strict claims/time/method/key behavior includes the extreme-NumericDate
  regression discovered during review. The legacy fixture was produced by the
  actual old SDK before deletion, not by a replacement implementation. Its
  provenance and disposable key are in the integration's testdata/README.md.
- Existing ID shape, SHA-256 digest encoding, and bidirectional AES envelope
  compatibility are preserved. Integration swaps must preserve actual key bytes.
- Auth-cms adds a direct golang-jwt requirement/local replacement and two jwt/v5
  go.sum entries. GOWORK=off go mod tidy also updates recorded SDK v0.5.0→v0.7.1
  and jobs v0.3.0→v0.5.0 requirements already selected by the existing dependency
  graph. No consumer upgrade or release occurred. New coordinated release
  versions/requirements still need selection; old SDK tags lack the new packages.
- The full gate exposed a pre-existing jobs memstore ordering defect. Equal or
  reversed timestamps plus random ID tie-breaking could select a superseded
  generation. Eight insertion lines enforce increasing same-key timestamps under
  the existing mutex; no public clock abstraction or schema change. Deterministic
  regressions failed before the fix and passed afterward. This bounded correction
  does not complete the jobs audit.
- Mechanical migration initially added unused id imports to generated pgx/Turso
  store templates because their only id.Database references were comments.
  The scaffold compile gate caught this; both unused imports were removed.

### Verification — passed

All Go commands used GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache and Go 1.26.1.
The final full gate required approved loopback access for httptest listeners;
no credentials or external datastore endpoints were supplied.

- From root: `make check`. Passed all **42 modules**, each with build/test/vet;
  eight integration-tag vet legs, three integration/live-tag compile-only vet
  legs, all 23 architecture guards, generated pocket compilation, and generated
  template/asset drift checks. Final log: /tmp/gopernicus-s4-make-check.log.
- From root: `make docs-build` (repository pnpm typecheck/build). Passed on final
  canonical docs/template content. Log: /tmp/gopernicus-s4-docs-build.log. The
  optional Docusaurus update check printed a nonfatal local config permission
  notice; the typecheck and site build succeeded.
- From integrations/cryptids/golang-jwt and integrations/cryptids/bcrypt:
  `go build ./...`, `go test -count=1 ./...`, `go vet ./...` after final adapter
  changes. JWT tests cover all supported methods, key sizes, zero/nil signers,
  caller method mutation, malformed/object/time claims, strict encoding, clock
  tolerance, extreme dates, legacy fixture, and upstream interoperability.
  Bcrypt covers ASCII/multibyte 72/73-byte boundaries and error classification.
- From pockets/authentication: fresh authsvc/invitationsvc/delivery suites passed;
  final full gate rechecked the complete pocket. Host-policy tests cover callback
  context, replacement of all default gates, all four setting flows, unchanged
  callback errors, independent breach checks, and existing Login/IssueToken.
- From examples/auth-cms:
  `go test -count=1 -run TestHostPasswordPolicyAndJWTOverHTTP ./cmd/server`.
  Passed with real local HTTP, public Config wiring, in-memory repositories,
  actual bcrypt/golang-jwt, and disposable data. A host-approved password below
  default length registers (201), obtains a token (200), and accesses the
  identity-protected route (204); altered token gets 401; host refusal and a
  host-approved 76-byte Unicode password rejected by bcrypt both get 400.
- From integrations/cryptids/golang-jwt:
  `go run /tmp/gopernicus-s4-migration.go`. Passed public replacement API proof:
  database/custom ID seam, non-ASCII refusal, principal validation, known SHA-256
  digest, captured old JWT with preserved key bytes, and bcrypt invalid-input
  classification. SDK tests additionally prove legacy/new AES envelopes in both
  directions, wrong keys, tampered tags, and short-envelope classifications.
- From pockets/jobs:
  `go test -count=50 -run '^Test(FencedQueue|WorkProtocolReplaceConformance)' ./memstore`;
  `go test -race -count=1 -run '^Test(FencedQueue|WorkProtocol)' ./memstore`;
  `go build ./...`; `go vet ./...`. All passed after deterministic fail-before/
  pass-after proof. Unrelated keys and empty-key behavior are also covered.
- From workshop/gopernicus:
  `go test -count=1 -run '^TestScaffoldPocketStoresCompile$' ./internal/commands`.
  Passed after removing both unused imports; final make check rechecked every
  scaffold test.
- From examples/auth-cms: `GOWORK=off go mod tidy` passed using local replacements.
- `/Users/jrazmi/go/bin/goimports -l` on all existing S4 Go files returned no
  files. Root `git diff --check` passed. Generated *_templ.go and UI dist/manifest
  diffs remained empty. Current source/template searches found no old SDK
  cryptids imports; historical records and legacy migration explanations remain.

### Resolved failures and limits

The first full-gate jobs failure is retained in
/tmp/gopernicus-s4-make-check-first-failure.log; a separate 50-run targeted
conformance command also reproduced it. The second
full-gate template import failure is retained in
/tmp/gopernicus-s4-make-check-template-failure.log. Both are resolved in the final
passing gate. No unresolved check failure or approval block remains.

POSTGRES_TEST_DSN, POSTGRES_DSN, DATABASE_URL, TURSO_DATABASE_URL,
TURSO_AUTH_TOKEN, FIRESTORE_EMULATOR_HOST, FIRESTORE_PROJECT_ID, and
GOOGLE_CLOUD_PROJECT were unset during preflight. Live store suites were skipped;
integration/live tags were compiled/vetted, not executed. No live providers,
production data, consumer builds, browser/UI checks, or 32-bit execution. Race
coverage was targeted to the corrected jobs concurrency behavior. This is not
an authentication pocket-wide or third-party dependency security audit.

Branch stayed firestore-authentication; HEAD advanced independently from
82aa19f7 at start to 6807ed06 at verification. Concurrent Firestore changes and
all earlier audit edits were preserved. This task made no commits/tags and did
not edit consumer apps or generated sources.

### Follow-ups and next step

Continue with S5 foundation/crud, including whether its name, package boundary,
and generic APIs earn their place. Carry S4 notification eligibility versus
identity projection, issuer/audience profiles, and development digest privacy
wording into authentication/consumer review. Carry durable jobs adapter timestamp
precision and generation ordering into the jobs audit; no SQL fix is included.
AUDIT-004 contains the standalone breaking-change and key-byte migration guide.

### Exact S4 changed-file inventory

These 164 paths include moved/deleted files and mechanical import/comment changes;
they do not claim ownership of every dirty file. Four authentication Firestore
files and SCHEMA.md overlap concurrent work; the S4 contribution there is only
identifier/digest API spelling. Shared files may also retain S2/S3 edits, whose
inventories remain in their own implementation records. No generated files are
part of this list.

<details>
<summary>S4 file paths</summary>

- `ARCHITECTURE.md`
- `AUDIT.md`
- `RELEASING.md`
- `examples/auth-cms/README.md`
- `examples/auth-cms/cmd/server/demo.go`
- `examples/auth-cms/cmd/server/identity_crypto_test.go`
- `examples/auth-cms/cmd/server/jobs_delivery_live_test.go`
- `examples/auth-cms/cmd/server/jobs_delivery_proof_test.go`
- `examples/auth-cms/cmd/server/jobs_delivery_retry_test.go`
- `examples/auth-cms/cmd/server/main.go`
- `examples/auth-cms/go.mod`
- `examples/auth-cms/go.sum`
- `examples/auth-cms/internal/authmem/authmem.go`
- `examples/auth-cms/internal/memstore/memstore.go`
- `examples/minimal/cmd/server/main.go`
- `examples/minimal/internal/memstore/memstore.go`
- `integrations/cryptids/bcrypt/README.md`
- `integrations/cryptids/bcrypt/bcrypt.go`
- `integrations/cryptids/bcrypt/bcrypt_test.go`
- `integrations/cryptids/bcrypt/go.mod`
- `integrations/cryptids/golang-jwt/README.md`
- `integrations/cryptids/golang-jwt/crossverify_test.go`
- `integrations/cryptids/golang-jwt/golangjwt.go`
- `integrations/cryptids/golang-jwt/golangjwt_test.go`
- `integrations/cryptids/golang-jwt/testdata/README.md`
- `integrations/cryptids/golang-jwt/testdata/legacy-hs256.jwt`
- `integrations/cryptids/google-uuid/README.md`
- `integrations/cryptids/google-uuid/googleuuid.go`
- `integrations/cryptids/google-uuid/googleuuid_test.go`
- `plans/framework-audit-identity-cryptids.md`
- `plans/framework-audit.md`
- `plans/identity-cryptography-cleanup.md`
- `pockets/README.md`
- `pockets/authentication/README.md`
- `pockets/authentication/authentication.go`
- `pockets/authentication/domain/apikey/apikey.go`
- `pockets/authentication/domain/apikey/apikey_test.go`
- `pockets/authentication/domain/identifier/identifier.go`
- `pockets/authentication/domain/identifier/identifier_test.go`
- `pockets/authentication/domain/invitation/invitation.go`
- `pockets/authentication/domain/invitation/invitation_test.go`
- `pockets/authentication/domain/oauthstate/oauthstate.go`
- `pockets/authentication/domain/securityevent/securityevent.go`
- `pockets/authentication/domain/serviceaccount/serviceaccount.go`
- `pockets/authentication/domain/serviceaccount/serviceaccount_test.go`
- `pockets/authentication/domain/session/session.go`
- `pockets/authentication/domain/user/user.go`
- `pockets/authentication/internal/inbound/authentication/token_test.go`
- `pockets/authentication/internal/logic/authsvc/delivery.go`
- `pockets/authentication/internal/logic/authsvc/machine.go`
- `pockets/authentication/internal/logic/authsvc/oauth.go`
- `pockets/authentication/internal/logic/authsvc/oauth_test.go`
- `pockets/authentication/internal/logic/authsvc/password_policy_test.go`
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
- `pockets/authentication/invitation_lookup_test.go`
- `pockets/authentication/security_hmac.go`
- `pockets/authentication/security_test.go`
- `pockets/authentication/stores/firestore/SCHEMA.md`
- `pockets/authentication/stores/firestore/fixtures_test.go`
- `pockets/authentication/stores/firestore/identifiers_doc.go`
- `pockets/authentication/stores/firestore/users.go`
- `pockets/authentication/stores/firestore/users_doc.go`
- `pockets/authentication/stores/pgx/api_keys.go`
- `pockets/authentication/stores/pgx/invitations.go`
- `pockets/authentication/stores/pgx/security_events.go`
- `pockets/authentication/stores/pgx/service_accounts.go`
- `pockets/authentication/stores/turso/api_keys.go`
- `pockets/authentication/stores/turso/invitations.go`
- `pockets/authentication/stores/turso/security_events.go`
- `pockets/authentication/stores/turso/service_accounts.go`
- `pockets/authentication/storetest/storetest.go`
- `pockets/authorization/README.md`
- `pockets/authorization/authorization.go`
- `pockets/authorization/domain/relationship/relationship.go`
- `pockets/authorization/internal/logic/authorizersvc/batch_reader_test.go`
- `pockets/authorization/internal/logic/authorizersvc/batch_reason_test.go`
- `pockets/authorization/internal/logic/authorizersvc/explain_test.go`
- `pockets/authorization/internal/logic/authorizersvc/limits_test.go`
- `pockets/authorization/internal/logic/authorizersvc/lookup_test.go`
- `pockets/authorization/internal/logic/authorizersvc/principalref_test.go`
- `pockets/authorization/internal/logic/authorizersvc/service.go`
- `pockets/authorization/internal/logic/authorizersvc/service_test.go`
- `pockets/authorization/memstore/memstore.go`
- `pockets/authorization/memstore/memstore_test.go`
- `pockets/authorization/stores/pgx/README.md`
- `pockets/authorization/stores/turso/README.md`
- `pockets/authorization/storetest/storetest.go`
- `pockets/cms/cms.go`
- `pockets/cms/domain/content/entry.go`
- `pockets/cms/domain/content/registry_test.go`
- `pockets/cms/domain/media/asset.go`
- `pockets/cms/domain/menus/menu.go`
- `pockets/cms/domain/messaging/inquiry.go`
- `pockets/cms/domain/taxonomy/term.go`
- `pockets/cms/internal/logic/entrysvc/service.go`
- `pockets/cms/internal/logic/entrysvc/service_test.go`
- `pockets/cms/internal/logic/mediasvc/media_test.go`
- `pockets/cms/internal/logic/mediasvc/service.go`
- `pockets/cms/internal/logic/menussvc/menus_test.go`
- `pockets/cms/internal/logic/menussvc/service.go`
- `pockets/cms/internal/logic/messagingsvc/messaging_test.go`
- `pockets/cms/internal/logic/messagingsvc/service.go`
- `pockets/cms/internal/logic/taxonomysvc/service.go`
- `pockets/cms/internal/logic/taxonomysvc/taxonomy_test.go`
- `pockets/cms/stores/pgx/assets.go`
- `pockets/cms/stores/pgx/entries.go`
- `pockets/cms/stores/pgx/inquiries.go`
- `pockets/cms/stores/pgx/menus.go`
- `pockets/cms/stores/pgx/terms.go`
- `pockets/cms/stores/turso/assets.go`
- `pockets/cms/stores/turso/entries.go`
- `pockets/cms/stores/turso/inquiries.go`
- `pockets/cms/stores/turso/menus.go`
- `pockets/cms/stores/turso/terms.go`
- `pockets/cms/storetest/storetest.go`
- `pockets/jobs/memstore/fenced.go`
- `pockets/jobs/memstore/fenced_timestamps_test.go`
- `sdk/README.md`
- `sdk/capabilities/events/events.go`
- `sdk/foundation/cryptids/aesgcm.go`
- `sdk/foundation/cryptids/aesgcm_test.go`
- `sdk/foundation/cryptids/cryptids.go`
- `sdk/foundation/cryptids/hasher.go`
- `sdk/foundation/cryptids/hasher_test.go`
- `sdk/foundation/cryptids/hs256.go`
- `sdk/foundation/cryptids/hs256_test.go`
- `sdk/foundation/cryptids/id.go`
- `sdk/foundation/cryptids/id_test.go`
- `sdk/foundation/cryptids/jwt.go`
- `sdk/foundation/cryptography/aesgcm.go`
- `sdk/foundation/cryptography/aesgcm_test.go`
- `sdk/foundation/cryptography/cryptography.go`
- `sdk/foundation/cryptography/jwt.go`
- `sdk/foundation/cryptography/sha256.go`
- `sdk/foundation/cryptography/sha256_test.go`
- `sdk/foundation/environment/secret.go`
- `sdk/foundation/id/id.go`
- `sdk/foundation/id/id_test.go`
- `sdk/foundation/identity/identity.go`
- `sdk/foundation/identity/identity_test.go`
- `workshop/documentation/docs/guides/create-pocket.md`
- `workshop/documentation/docs/integrations/catalog.md`
- `workshop/documentation/docs/sdk/foundation.md`
- `workshop/gopernicus/internal/commands/templates/pocket/entity.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/memstore.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/service.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/socket.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/migration.sql.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/stores/pgx/store.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/stores/turso/migration.sql.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/stores/turso/store.go.tmpl`
- `workshop/gopernicus/internal/commands/templates/pocket/storetest.go.tmpl`

</details>
