# pockets/secrets — owner-scoped encrypted secrets with rotation and audit

A pluggable, datastore-free secrets pocket: values encrypted at rest under a
per-owner data key, data keys wrapped by a rotatable master key ring, every
read, write, delete and key operation recorded in an append-only audit log in
the same transaction as the change. Design of record:
[plans/secrets-pocket.md](../../plans/secrets-pocket.md).

## Layout

```
secrets.go, options.go  optional New / Components assembly
logic/vault/            Owner, Ref, Source, Secret; Service (Put/Get/Delete/
                        DeleteOwner/List/Audit); Repository and Wrapper ports;
                        RingWrapper, Seal/Open
logic/rotation/         Service: RewrapOwnerKeys, RotateOwnerKey, Report
stores/memory/          stdlib-only Repository
stores/storetest/       shared conformance suite
stores/pgx/             independent PostgreSQL module and migration
```

## The model

- An **Owner** is `{Type, ID}`, for example `{tenant, gps}`. The pocket does
  not know what a tenant is; the host does.
- A **Ref** is an owner plus a name, for example `cloudflare.api_token`.
  Values are opaque bytes up to `vault.MaxValueBytes` (64 KiB).
- Each owner has one **data key**. Values are sealed under it with AES-256-GCM
  and the Ref as additional authenticated data, so a ciphertext copied to
  another row does not open. The data key is stored **wrapped** by the
  `Wrapper`; the bundled `vault.RingWrapper` wraps with a
  `cryptids.KeyedEncrypter`, normally `cryptids.NewAESGCMRing` over keys the
  host reads from its environment.
- Every call that touches a value takes a **Source**: exactly one actor pair
  (`ActorType`, `ActorID`) or a `System`, plus an optional `Reason`. Actor
  ids take whatever shape the host's authentication issues (mixed case,
  ULIDs, UUIDs, `svc:deploy`, an email address); they are stored, not parsed. A read
  without one is refused with `vault.ErrSourceRequired`. `List` reveals names
  only and takes no Source.
- **Audit** records (`vault.AuditRecord`) carry the action, outcome, owner,
  name, version, key id, source, time and correlation ids; never a value.
  They are written in the same transaction as the change they describe. The
  pocket offers `Audit` to page them and nothing that removes them; retention
  is host policy. See [Audit](#audit).

## Construction

```go
ring, err := cryptids.ParseKeyRing(os.Getenv("APP_SECRETS_KEYS"), os.Getenv("APP_SECRETS_ACTIVE_KEY"))
master, err := cryptids.NewAESGCMRing(ring)
wrapper, err := vault.NewRingWrapper(master)
components, err := secrets.New(secrets.Repositories{Secrets: repo}, secrets.WithWrapper(wrapper))

meta, err := components.Vault.Put(ctx, vault.Ref{Owner: tenant, Name: "cloudflare.api_token"}, token,
    vault.Source{ActorType: "user", ActorID: userID, Reason: "pasted in settings"})
value, err := components.Vault.Get(ctx, ref, vault.Source{System: "deploy"})
```

`vault.NewService(repo, wrapper)` and `rotation.NewService(repo, wrapper)`
build the same services directly. `New` starts nothing and registers no
routes: which principal may read which owner's secrets is host policy on host
routes.

| Configuration | Behavior |
|---|---|
| `Repositories.Secrets` | required: `vault.ErrRepositoryRequired` |
| `WithWrapper` | required: `vault.ErrWrapperRequired`; there is no safe default without key material |
| `WithClock` | nil selects `time.Now` in UTC |
| missing environment keys | host policy; the pocket never generates a master key |

## Rotation

Two procedures, both library calls for the host's own command.

**Master key** (`APP_SECRETS_KEYS` changed):

1. Add the new key to the ring; keep the old one active. Deploy.
2. Make the new key active. Deploy. New owner keys wrap under it; old ones still unwrap.
3. `components.Rotation.RewrapOwnerKeys(ctx, source, rotation.RewrapOptions{})`
   rewraps every owner key not already under the active key. Secrets rows are
   untouched. It is idempotent; `Report.Failed` lists owners whose key is under
   a wrapper key no longer in the ring. `DryRun` counts without writing.
4. Remove the old key from the ring once `Failed` is empty.
5. Retire it everywhere else too (old `.env` files, secret-manager versions,
   backups of the environment): a backup of `secrets_owner_keys` taken before the
   rewrap stays readable for as long as that key exists anywhere.

**Owner key** (`RotateOwnerKey(ctx, owner, source)`): a fresh data key,
every secret of that owner resealed, and the key replaced, in one transaction.
Use it on a schedule or after someone with database read access leaves.

**Offboarding** (`Vault.DeleteOwner`): deletes the owner's secrets and
destroys its data key in one transaction, recording each. Backups of the
value rows alone are unreadable afterwards. A backup that also contains
`secrets_owner_keys` remains readable until every ring key it was wrapped under
is retired, so an offboarding that must defeat backups is followed by the
master-key rotation above, through step 5.

## Audit

| Record | What it says |
|---|---|
| `secret_read` | one per `Get`, appended after the value is opened, with the version and key id read; a failed append fails the read, so no value leaves without its record |
| `Outcome` | `ok`, or `denied` on a `secret_read` the vault could not open for the caller: the owner key is under a wrapper key the ring no longer holds (`Unwrap` returned `sdk.ErrInvalidInput`), the value is sealed under a key the owner no longer holds, or the value fails authentication. It is not an authorization verdict; the vault has none and the host authorizes before calling. The roadmap's `secret_read_denied` is this outcome value, not an action |
| no record | a missing secret or owner key (`sdk.ErrNotFound`), or a wrapper error that is not a refusal (an unreachable key service): the vault learned nothing about the secret |
| `secret_deleted` | the version and key id the store removed, not the ones the caller listed; `DeleteOwner` deletes exactly the secrets it listed, and relists and retries up to three times when they changed (`sdk.ErrConflict` after that) |
| `RequestID`, `TraceID` | stamped by `vault.Correlate` from `sdk.RequestIDFromContext` and `sdk.TraceIDFromContext` on every record; an id over 128 bytes or outside printable ASCII is blanked, never truncated and never refused |
| `AuditFilter.Outcome` | `Audit(ctx, vault.AuditFilter{Outcome: vault.OutcomeDenied}, …)` pages the denials |

## Errors

`sdk.ErrNotFound` (missing secret or owner key), `sdk.ErrAlreadyExists`
(second owner key, duplicate audit id), `sdk.ErrConflict` (stale key
expectation on replace, `vault.ErrSealedValueCorrupt`, a value sealed under a
key the owner no longer holds, an owner whose secrets kept changing under
`DeleteOwner`), `sdk.ErrInvalidReference` (secret for an owner
without a key), `sdk.ErrInvalidInput` (validation, `cryptids.ErrUnknownKeyID`
when the master key a row needs was dropped). Error text never carries values
or key material.

## Stores

`stores/memory` is the reference. `stores/storetest.RunRepository` is the
executable contract every store runs, including the atomicity cases (a failing
audit append leaves the change unwritten; one refused record in an
`AppendAudit` batch writes none of it). `stores/pgx` is the PostgreSQL module;
see its README for the schema option and the live suite. It requires
PostgreSQL 14 or later and ships three migrations:

- `0002_secrets_audit_integrity.sql` is **required**: `pgx.Open` refuses a
  database without it, naming the file. It adds the outcome and correlation
  columns, plus `seq` and `recorded_at` for operators.
- `0003_secrets_audit_immutable.sql` is **default-on**: triggers that refuse
  `UPDATE`, `DELETE` and `TRUNCATE` on `secrets_audit` to every role but the
  table owner's members. A host that declines them deletes the file from its
  ledger before applying.

Construct the store with `pgx.Open(ctx, db, opts...)`, which also judges the
runtime role's posture on `secrets_audit` under the database's `RuntimeMode`:
in production it refuses a role that can modify the table or cannot append
to it, including one without `USAGE` on `secrets_audit_seq`. The two-role
setup, with that sequence grant, is in
[database-posture.md](../../workshop/documentation/docs/guides/database-posture.md).
