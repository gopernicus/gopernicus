# Secrets pocket: owner-scoped encrypted secrets with key rotation and audit

Status: RATIFIED — 2026-10-16 ("yes, two layers, ratify and build"). Owner
decided §6.1 reads require a Source and §6.2 two layers; §6.3–§6.6 take the
recommendations (64 KiB, `secrets`, `List` unaudited, one `id:hex` list).
Work on branch `secrets-pocket` from `main` 8a18f0c4. S1–S3 BUILT AND
VERIFIED 2026-10-16 (see §9); S4 release and S5 Venona pending.
Requester: venona-project plan 02 S2.2 (tenant-held Cloudflare/GitHub/DO
credentials). Prior art: Segovia v1 `tenant_secrets` (2026-05-07), built on the
old framework; design lineage recorded in §2.

## 0. Why a pocket, and why now

Three hosts need the same thing: Venona (per-tenant hosting credentials),
Segovia (per-tenant Slack and datasource tokens, v1 store orphaned when v2 moved
to this framework) and gps-360-go (today a single-key TS `secretBox`). Each
needs encryption at rest, a rotation procedure that does not require downtime,
and an access record that answers "who read which secret, when" — the SOC 2
evidence an auditor asks for under CC6.1 (encryption, key management) and CC7.2
(monitoring and logging). None of it is domain-specific, so none of it belongs
in a host.

Nothing in the current framework does this. `sdk/pkg/cryptids.AESGCM` is one
key with no key id, so a stored ciphertext cannot be told apart from one made
with a retired key, and rotation means a flag day. The only ring-shaped type is
`protection.HMACKeyRing` in authentication, and it signs, not encrypts.
Segovia v1 had a rotatable ring and a rotation command, but a single encryption
layer (every row re-encrypted to rotate the master) and no audit at all. This
plan takes Segovia's ring and wire format, adds the missing layer and the audit
log, and lands it where every host can mount it.

## 1. Scope

In:

1. `sdk/pkg/cryptids`: a key ring and a rotatable AEAD encrypter whose
   ciphertext names the key that produced it.
2. `pockets/secrets`: owner-scoped secret storage with per-owner data keys
   wrapped by a master ring (envelope encryption), an append-only audit log,
   rotation of both layers, a memory store, a conformance suite, and a pgx store
   module with canonical migrations.

Out, and why:

- An HTTP adapter. Which principal may read a tenant's Cloudflare token is host
  policy on host routes (Venona checks `tenant.manage`; Segovia will check its
  own). The pocket exposes services; `inbound/http` arrives with the first host
  that wants bundled routes (deny-by-absence, pockets charter §1 tier 1).
- A cloud KMS wrapper. The port is here; the implementation is a later
  `integrations/` module (§3.4).
- Moving authentication's `TokenEncrypter`/`DeliveryEncrypter` onto the ring.
  They take `cryptids.Encrypter`; the new type satisfies it, so hosts can pass it
  today. Changing authentication's defaults is a separate release.
- Secret metadata (kind, verified permissions, last used). That is the host's
  record; Venona keeps `tenant_credentials` and stores only the value here.

## 2. Design lineage: what Segovia v1 got right and what is changed

Kept, as proven in production for five months:

- A ring of named 32-byte AES-256-GCM keys plus one active id, validated eagerly
  at boot (empty ring, missing active, duplicate id, wrong length all refuse).
- Ciphertext carries a scheme tag and the key id, so decrypt looks the key up by
  what the row says, and rotation can skip rows already on the active key.
- Rotation is add key → flip active → re-encrypt rows in idempotent pages →
  drop old key. Rows under an unknown key are counted and reported, never
  silently skipped.

Changed:

| Segovia v1 | here | why |
|---|---|---|
| secrets encrypted directly under the master ring | per-owner data key (DEK) wrapped by the master ring; secrets sealed under the DEK | master rotation rewraps one row per owner instead of every secret; an owner's whole key material can be destroyed on offboarding (crypto-shred) |
| `tenant_id` column | `owner_type` + `owner_id` | the pocket cannot know about tenants; Venona uses `tenant`, a host may use `site` or `org` |
| no audit | append-only `secrets_audit`, written in the same transaction as the action it records; reads included | the SOC gap; the only reason to prefer this over a bare encrypter |
| `backend` discriminator (`db`, `gcp_kms`, `gcp_secret_manager`) in the row | a `Wrapper` port chosen at construction; the wrapped DEK row records the wrapper's key id | the backend decision is a host wiring decision, not per row; one wrapper per deployment keeps rotation reasoning simple |
| base64 key material in env | hex, via `environment.Secret` | matches every other framework key (`openssl rand -hex 32`) |
| string secrets | `[]byte` | tokens are text today; private keys (GitHub App PEM) are bytes tomorrow |

## 3. Design

### 3.1 `sdk/pkg/cryptids`: key ring and keyed encrypter

```go
// KeyRing names the keys a KeyedEncrypter may use. Active encrypts; every key
// decrypts. Retired keys are removed from the ring only after nothing still
// references them.
type KeyRing struct {
    Active string
    Keys   map[string][]byte // each exactly 32 bytes
}

// ParseKeyRing reads "id:hex,id:hex" and the active id. Used by hosts with
// environment variables such as APP_SECRETS_KEYS / APP_SECRETS_ACTIVE_KEY.
func ParseKeyRing(keys, active string) (KeyRing, error)

// KeyedEncrypter is an Encrypter whose ciphertext records which key sealed it.
type KeyedEncrypter interface {
    Encrypter
    ActiveKeyID() string
    KeyIDOf(ciphertext string) (string, error)
}

func NewAESGCMRing(ring KeyRing) (*AESGCMRing, error)
```

Wire format: `v1.<key_id>.<base64url(nonce||ciphertext||tag)>`. The payload
segment is exactly today's `AESGCM` output, so the implementation reuses
`cipher.NewGCMWithRandomNonce` and one `AESGCM` per key. Key ids match
`^[a-z0-9][a-z0-9_-]{0,31}$` so the format is unambiguous.

Errors, all matching `sdk.ErrInvalidInput` unless noted: `ErrKeyRingEmpty`,
`ErrKeyRingActiveMissing`, `ErrKeyMalformed`, `ErrUnknownScheme`,
`ErrUnknownKeyID`, `ErrCiphertextMalformed`. A failed GCM open returns
`ErrCiphertextAuthFailed`, matching `sdk.ErrConflict` (the row and the key
disagree). Error text never includes key material or plaintext.

`Encrypter` and `AESGCM` are unchanged. This is a MINOR sdk release.

### 3.2 `pockets/secrets`: layout

```
secrets.go               New / Components (vault + rotation), Option, Repositories
logic/vault/             Owner, Ref, Secret, Source; Service (Put/Get/Delete/List);
                         Repository and Wrapper ports; audit record types
logic/rotation/          Service: RewrapOwnerKeys, RotateOwnerKey, Report
stores/memory/           stdlib-only Repository
stores/storetest/        RunRepository conformance suite
stores/pgx/              separate module: Repository, WithSchema, StatusCheck,
                         ExportMigrations, MigrationsFS, migrations/
```

The pocket is a module over sdk (`pockets/secrets` requires sdk ≥ v0.13.0, the
release carrying §3.1). `stores/pgx` requires pgxdb and pgx, never imported by
the core. Logic imports only the stdlib and sdk.

### 3.3 Vocabulary

```go
type Owner struct{ Type, ID string }   // e.g. {tenant, gps}
type Ref   struct{ Owner Owner; Name string } // Name e.g. cloudflare.api_token
type Secret struct {
    Ref       Ref
    Version   int       // increments on every Put
    KeyID     string    // the owner key version that sealed it
    CreatedAt time.Time
    UpdatedAt time.Time
}
type Source struct{ ActorType, ActorID, System, Reason string }
```

`Owner.Type`, `Owner.ID` and `Name` match `^[a-z0-9][a-z0-9._-]{0,127}$`;
`sdk.ErrInvalidInput` otherwise. Values are opaque `[]byte`, nonempty, ≤ 64 KiB
(owner call: the ceiling). `Source` is the authorization pocket's audit source
shape copied, not imported: pockets do not depend on each other. Validation
matches: either an actor pair or a system, never both nor neither; reason ≤ 512.

### 3.4 Ports

```go
// Wrapper protects owner data keys. The bundled implementation wraps with a
// cryptids.KeyedEncrypter (the master ring held in the host's environment).
// A cloud KMS implementation lands in integrations/ later; both record a
// key id so a rewrap can tell what each row is under.
type Wrapper interface {
    Wrap(ctx context.Context, dek []byte) (blob string, keyID string, err error)
    Unwrap(ctx context.Context, blob string) ([]byte, error)
    ActiveKeyID() string
}

// Repository persists owner keys, sealed secrets and the audit log. Every
// mutating method takes the audit records to append and writes them in the
// same transaction; a store that cannot do that atomically does not conform.
type Repository interface {
    GetOwnerKey(ctx, Owner) (OwnerKey, error)                  // sdk.ErrNotFound
    CreateOwnerKey(ctx, OwnerKey, []AuditRecord) error         // sdk.ErrAlreadyExists
    ReplaceOwnerKey(ctx, OwnerKey, expectedKeyID string, resealed []SealedSecret, []AuditRecord) error // sdk.ErrConflict
    ListOwnerKeys(ctx, afterCursor string, limit int) ([]OwnerKey, string, error)
    GetSecret(ctx, Ref, []AuditRecord) (SealedSecret, error)   // the read IS audited
    PutSecret(ctx, SealedSecret, []AuditRecord) (version int, err error)
    DeleteSecret(ctx, Ref, []AuditRecord) error                // sdk.ErrNotFound
    ListSecrets(ctx, Owner) ([]Secret, error)                  // metadata only
    ListAudit(ctx, AuditFilter) ([]AuditRecord, string, error)
}
```

`OwnerKey{Owner, KeyID string, WrappedDEK string, WrapperKeyID string, CreatedAt,
RotatedAt}`. `KeyID` is the owner key version (`k1`, `k2`, …, generated by the
service); `WrapperKeyID` is what the master ring reports.

### 3.5 Vault service behavior

- **Put(ctx, Ref, value, Source)**: validate; load or create the owner key
  (first Put for an owner generates a random 32-byte DEK, wraps it, and records
  `owner_key_created`); seal `value` under the DEK with AES-GCM, with the
  canonical `Ref` string as additional authenticated data so a ciphertext moved
  to another row fails to open; persist; record `secret_put` with the new
  version. Returns the new `Secret`.
- **Get(ctx, Ref, Source)**: validate; unwrap the owner key; open; record
  `secret_read`. Missing → `sdk.ErrNotFound`. A read with no `Source` in the
  arguments is refused with `sdk.ErrInvalidInput`: unattributed access is the
  thing the log exists to prevent. (Owner call, §6.)
- **Delete(ctx, Ref, Source)**: record `secret_deleted`.
- **DeleteOwner(ctx, Owner, Source)**: deletes every secret and the owner key
  in one transaction, records `owner_key_destroyed`. Crypto-shredding: even a
  backup of the secrets rows is unreadable afterwards.
- **List(ctx, Owner)**: metadata; not audited (it reveals names, not values —
  owner call if names are sensitive in some host).
- Plaintext never reaches a log line, an error string or an audit row. Tests
  assert this with a sentinel value and a capturing logger.

### 3.6 Audit

One append-only table. A record: `id` (ULID-shaped, store-ordered),
`occurred_at`, `action`, `owner_type`, `owner_id`, `name` (null for owner-key
actions), `version`, `key_id`, `source_actor_type`, `source_actor_id`,
`source_system`, `source_reason`. Actions: `owner_key_created`,
`owner_key_rewrapped`, `owner_key_rotated`, `owner_key_destroyed`,
`secret_put`, `secret_read`, `secret_deleted`.

Audit rows are written by the same statement batch as the change they
describe. The pgx store uses one transaction; the memory store one mutex. There
is no delete or update on the audit table and the pgx migration grants none
(`REVOKE UPDATE, DELETE` is left to the host's role setup; the README says so).
Retention is host policy; the pocket ships `ListAudit` with filters (owner,
name, action, time range, source) and a cursor, nothing that removes rows.

### 3.7 Rotation service

Two procedures, both library functions a host wires into its own command (the
jobs pocket precedent: no CLI in the pocket).

**Master rotation** (the wrapper's key changed):

1. Host adds the new key to its ring, keeps the old active. Deploy.
2. Host flips active to the new key. Deploy. New owner keys wrap under it;
   existing owner keys still unwrap.
3. `rotation.RewrapOwnerKeys(ctx, Source, opts{Page, DryRun})` pages
   `ListOwnerKeys`, skips rows whose `WrapperKeyID` is already active, for the
   rest unwraps and rewraps the DEK and calls `ReplaceOwnerKey` with the
   expected current key id (so a concurrent rotation of the same owner is a
   `sdk.ErrConflict`, retried once, then reported). Secrets rows are untouched:
   this is N owners, not N secrets. Returns `Report{Scanned, Rewrapped,
   Skipped, Failed []Owner}`; unknown-key failures are reported, not fatal.
4. Host removes the old key from the ring once `Report.Failed` is empty.

**Owner key rotation** (an owner's DEK is replaced, e.g. after offboarding an
operator who had database read access, or on a schedule):

- `rotation.RotateOwnerKey(ctx, Owner, Source)`: generate a new DEK, reseal
  every secret of that owner under it, replace the owner key, all in one
  `ReplaceOwnerKey` call. Records `owner_key_rotated`.

### 3.8 pgx store

Tables, all under the configured schema (`WithSchema(pgxdb.Schema)`, Postgres
only, same as jobs): `secrets_owner_keys` (PK owner_type, owner_id),
`secrets_values` (PK owner_type, owner_id, name; FK to owner keys, no cascade —
`DeleteOwner` is explicit), `secrets_audit` (PK id; index on (owner_type,
owner_id, occurred_at)). Ciphertext columns are `TEXT` in the §3.1 wire
format. `StatusCheck` probes all three. `ExportMigrations` copies
`migrations/0001_secrets.sql`. Error mapping via `pgxdb.MapError`.

Venona would run this stream under `iam` alongside authentication, or under
`public`; the plan does not decide (host call; `workshop/migrations` already
handles per-schema streams).

### 3.9 Host wiring (what Venona's `cmd/venona-server` would do)

```go
ring, err := cryptids.ParseKeyRing(os.Getenv("VENONA_SECRETS_KEYS"), os.Getenv("VENONA_SECRETS_ACTIVE_KEY"))
master, err := cryptids.NewAESGCMRing(ring)
repo := secretspgx.NewRepository(db, secretspgx.WithSchema(iam))
vault, err := secrets.New(secrets.Repositories{Secrets: repo}, secrets.WithWrapper(secrets.NewRingWrapper(master)))
// vault.Vault.Put / Get / Delete; vault.Rotation.RewrapOwnerKeys
```

Development with no keys: the host generates an ephemeral ring with a warning,
as `keyFromEnv` does today for the authentication keys. Production refuses.
That is host policy and stays in the host.

## 4. SOC 2 mapping (evidence, not a certification)

| control area | what the pocket provides | what stays with the host |
|---|---|---|
| CC6.1 encryption at rest | AES-256-GCM, per-owner keys, master-wrapped; AAD binds a value to its row | key material kept out of the database (env or KMS); database and backup encryption |
| CC6.1 key management | named key versions; documented two-layer rotation; `Report` as a rotation artifact; crypto-shred on owner removal | rotation schedule; who holds the ring; running `RewrapOwnerKeys` |
| CC6.3 least privilege | reads require attribution; services, not rows, are the API | which principals may call `Get`, enforced on host routes |
| CC7.2 monitoring/logging | append-only audit of every put/read/delete/rotation with actor and reason, same transaction | retention, export to a SIEM, alerting on `secret_read` volume |

## 5. Stages

| stage | deliverable | verification |
|---|---|---|
| S0 | this plan ratified; open calls in §6 decided | owner |
| S1 | sdk: `KeyRing`, `ParseKeyRing`, `KeyedEncrypter`, `AESGCMRing`, errors, docs in `sdk/pkg/cryptids` | unit tests: round trip per key, decrypt under a retired-but-present key, unknown key, tamper, malformed, key id regex, `Encrypter` satisfaction; `go vet`; sdk MINOR tag |
| S2 | `pockets/secrets` core: `logic/vault`, `logic/rotation`, root `New`, `stores/memory`, `stores/storetest`, README | conformance suite green on memory; plaintext-leak test with a capturing logger; audit atomicity test (failing write leaves no audit row, failing audit leaves no write); rotation tests for both layers including the conflict path |
| S3 | `stores/pgx` module: repository, migration, `WithSchema`, `StatusCheck`, export, README | conformance suite green on local PostgreSQL 17; schema guard test; `FOR UPDATE` on owner key during `ReplaceOwnerKey` proven with two concurrent rotators |
| S4 | `examples/secrets-minimal` (memory) or a section in `examples/auth-cms`; RELEASING.md entry; release manifest; tags `pockets/secrets/v0.1.0`, `pockets/secrets/stores/pgx/v0.1.0` | cold download with `GOWORK=off` builds and tests; AUDIT.md unchanged (no breaking change) |
| S5 | Venona S2.2 consumes it (in venona-project, its own plan slice) | the Cloudflare-token flow end to end: paste → verify → store → worker reads with `Source{System: "deploy"}` → audit shows the read |

Each stage is its own PR. S1 can ship alone; S2 and S3 may share a PR.

## 6. Owner calls

1. **Reads require a `Source`** (§3.5), or allow unattributed reads with a
   logged warning. Recommendation: require. It costs each caller one struct
   literal and is the point of the audit.
2. **Per-owner data keys** (§2), or Segovia's single layer. Recommendation:
   two layers. The cost is one more table and about 150 lines; the gain is
   cheap master rotation and crypto-shred.
3. **Value ceiling** 64 KiB (fits a PEM private key with room), or smaller.
4. **Pocket name** `secrets` (what it stores) versus `vault` (what it does).
   Recommendation: `secrets`; `vault` names a product.
5. **`List` unaudited** (§3.5), or audited as `secret_listed`.
6. **Ring env format** `id:hex,id:hex` + active id via `ParseKeyRing`, or two
   variables per key. Recommendation: the single list; it matches Segovia's
   operator docs with base64 swapped for hex.

## 7. Preconditions and constraints

Repository `/Users/jrazmi/code/gopernicus-ecosystem/gopernicus`, HEAD 5dc0f731
(2026-10-14) on `docs/tuplecache-key-on-binding-release`, 5 dirty paths that are
not this work. Work branches from `main` after that release lands. Go 1.26.1.
Latest tags at planning: sdk v0.12.0, pgxdb v0.9.0, jobs stores/pgx v0.6.0,
authorization stores/pgx v0.18.0. Local PostgreSQL 17 for store tests.

Rules in force: pockets charter (`pockets/README.md`: explicit dependencies,
no `init()` registration, logic imports only sdk and itself, driver stores in
separate modules, memory + storetest in core). Variables and constants at the
top of files, then interfaces. No generated code is involved. Never "authn" or
"authz" in names. Nothing here touches authentication, authorization, jobs or
any host; Segovia v1 is read-only reference.

## 8. Build notes (S1–S3, 2026-10-16)

Deviations from §3, each deliberate:

- **`Repository.ListSealedSecrets`** was added. `RotateOwnerKey` needs every
  ciphertext of an owner without recording N `secret_read` rows for one key
  change; the rotation records `owner_key_rotated` instead. The vault service
  never exposes it.
- **`PutSecret` takes one `AuditRecord`, not a slice**, because only the store
  knows the version it assigns; it writes that version into the record. The
  other mutations take `[]AuditRecord` (a `DeleteOwner` records one row per
  secret plus the key).
- **`GetSecret` records nothing for a missing secret.** The read did not
  happen; `sdk.ErrNotFound` says so.
- **Owner key ids** are `k_<16 hex>`, generated by the service, not
  sequential, so two racing first-Puts cannot collide on an id.
- **Collation.** The pgx store orders and compares cursors under
  `COLLATE "C"` so pages match the memory store byte for byte regardless of
  the database's default collation. Cursors are the last owner's
  `type:id` or the last audit record's id.
- **Concurrency.** `ReplaceOwnerKey` is a conditional `UPDATE ... WHERE
  key_id = expected` rather than `SELECT ... FOR UPDATE` then update; it is
  one statement and the proof (`TestLive_ConcurrentReplaceOwnerKey`, 16
  racers) shows one winner, fifteen `sdk.ErrConflict`, one audit row.
- **Pre-tag module pins.** `pockets/secrets/go.mod` and
  `stores/pgx/go.mod` carry relative `replace` directives (sdk, and the
  pocket) because the sdk key ring and the pocket are unreleased. S4 drops
  them when the tags exist, as the G5 guard text permits.
- `rotation.Report.Failed` collects owners failing with `ErrInvalidInput`,
  `ErrConflict` (after one retry) or `ErrNotFound`; any other error aborts the
  run, since it is the database, not a key, that is wrong.

## 9. Verification record

| what | result |
|---|---|
| `sdk`: `go vet ./...`, `go test ./...` | pass (cryptids key ring: rotation, unknown key, tamper, malformed, no material in errors) |
| `pockets/secrets`: vet, test | pass: vault (audited put/get/delete, refused unattributed read, AAD binding, shred, validation, dropped master reported, no plaintext in logs), rotation (master procedure steps 1–4, dry run, idempotent rerun, unknown-wrapper report, owner key rotation, atomic failure), storetest on memory |
| `stores/pgx` hermetic | export test, bare-table guard |
| `stores/pgx` live, PostgreSQL 17 (`postgres:17-alpine`, disposable) | `RunRepository` pass bare and under `POSTGRES_TEST_SCHEMA=secrets_test`; `TestLive_StatusCheck`; `TestLive_ConcurrentReplaceOwnerKey` |
| `make guard` (28 guards) | exit 0 after registering the two modules in `MODULES`, `STORE_MODULES` and the G5 pocket list |

## 10. References

- Segovia v1: `v1/infrastructure/secrets/envelope/{keyring,envelope}.go`,
  `v1/core/secrets/{secrets,store}.go`,
  `v1/app/tooling/commands/rotatesecrets.go`,
  `v1/workshop/migrations/primary/20260507120000_tenant_secrets.sql`,
  `v1/workshop/documentation/deployment/slack-installations.md` §"AES keyring".
- Framework: `sdk/pkg/cryptids/{cryptids,aesgcm}.go`,
  `sdk/pkg/environment/secret.go`,
  `pockets/authentication/logic/authentication/protection/hmac.go`
  (`HMACKeyRing`), `pockets/authorization/logic/audit/audit.go` (`Source`),
  `pockets/jobs` (layout, `stores/pgx` surface, `storetest`).
- Consumer: venona-project `plans/hosting/02-management-app.md` D4, D5, S2.2.
