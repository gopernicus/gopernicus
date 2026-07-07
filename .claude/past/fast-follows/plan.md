# fast-follows — complete the original-repo port (backends for every sdk port)

Status: EXECUTED + gated 2026-07-06 (NOTES.md 2026-07-06 entry #3; fresh
`make check` green at 26 modules, go.work↔Makefile 26/26, guards clean).
RATIFIED in-discussion (jrazmi, 2026-07-06): "yeah let's go ahead and
add in statuscheck, AddAcronym? And i would love to get all of our previous
infra/sdk into this new gopernicus. let's keep going on 2." — item 2 being
the fast-follow queue: otel exporters, gcs/s3 filestorage, sendgrid,
golang-jwt. Module count 21 → 26.

## Scope

- **task-0 (quality-of-life pair):** (a) `goredis.StatusCheck` — pgx-parity
  health check (read integrations/datastores/pgx for the exact shape/name).
  (b) `sdk/conversion` acronym customization via an immutable **`Caser`**
  type (`NewCaser(WithAcronyms("K8S", ...))`, methods mirroring the package
  funcs; package funcs stay as the default caser) — restores the AddAcronym
  capability WITHOUT the old package-mutable global (D-2 recorded it as a
  data race; the Caser seam was the reserved design).
- **task-1 `integrations/tracing/otel`:** implements `sdk/tracing.Tracer`
  over OpenTelemetry. ONE module for the otel family (R-KV1: the family is
  one coherent dependency); exporter chosen by config — stdout
  (stdouttrace), OTLP/gRPC (otlptracegrpc), or caller-supplied
  TracerProvider. Salvage: old `telemetry/` (shapes only — it is
  otel-aliased) + `infrastructure/tracing/{stdouttrace,otlptrace}`.
- **task-2 `integrations/filestorage/gcs`:** `filestorage.Storer` +
  `ResumableUploader` + `SignedURLer` over cloud.google.com/go/storage.
  Salvage old `infrastructure/storage/gcs`. Conformance: sdk
  `filestoragetest` env-gated (fake-gcs-server docker or GCS creds).
- **task-3 `integrations/filestorage/s3`:** same ports over aws-sdk-go-v2
  service/s3 (S3-compatible incl. MinIO/DO Spaces, per the old adapter).
  Salvage old `infrastructure/storage/s3`. Live leg vs dockered MinIO.
- **task-4 `integrations/email/sendgrid`:** `email.Sender` over
  sendgrid-go. Salvage old `sendgridemailer`. Hermetic tests via endpoint
  override/httptest.
- **task-5 `integrations/cryptids/golang-jwt`:** `cryptids.JWTSigner` over
  golang-jwt/jwt/v5. Salvage old `infrastructure/cryptids/golangjwt`.
  (jrazmi has now committed to golang-jwt — supersedes the "port-only, no
  impl" note from sdk-parity task-6.)
- **task-6 (main session):** register all five modules in go.work + Makefile
  MODULES in one pass; docs sync (ARCHITECTURE tree + count 26, README list,
  RELEASING enumeration, sdk/README rows gain their external backends,
  NOTES entry); memory.
- **task-7:** final verifier gate (fresh make check, 26 modules, grep/count
  agreement, hermetic skips loud).

## Build isolation rule (this milestone's parallelization key)

Module-creating agents (tasks 1–5) MUST NOT touch go.work or the Makefile —
the main session registers all five at task-6. Until then each module
verifies standalone with `GOWORK=off` from inside its own directory (the
bcrypt-pattern `replace gopernicus/sdk => ../../../sdk` resolves without the
workspace).

## Conventions (established, hold for every module)

Exactly one external library-family per module; bcrypt-pattern go.mod; loud
env-gated live legs, hermetic `make check`; README first paragraph states
what is wrapped; no features/* imports; never abbreviate
authentication/authorization; connectors named for the package (R-KV2).

## After this milestone (the honest remainder of the original repo)

- `throttler` (blocking token bucket; old redis impl) — needs an sdk port
  decision first (waiting-limiter vs rejecting ratelimiter); flag to jrazmi.
- `sqlitelimiter` (durable single-instance limiter) — weak old usage;
  recommend skip unless a consumer appears.
- Ruled dead / not infra: httpc (dead in original), database/crud DSL
  (superseded by hand-written SQL ruling), authorization+invitations
  (auth v2+ features), events outbox/SSE gateway (events-v1 phase 3+).
