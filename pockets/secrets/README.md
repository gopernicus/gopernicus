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
- **Audit** records (`vault.AuditRecord`) carry the action, owner, name,
  version, key id, source and time; never a value. They are written in the
  same transaction as the change they describe. The pocket offers `Audit` to
  page them and nothing that removes them; retention is host policy.

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

**Owner key** (`RotateOwnerKey(ctx, owner, source)`): a fresh data key,
every secret of that owner resealed, and the key replaced, in one transaction.
Use it on a schedule or after someone with database read access leaves.

**Offboarding** (`Vault.DeleteOwner`): deletes the owner's secrets and
destroys its data key in one transaction, recording each. Backups of the
value rows are unreadable afterwards.

## Errors

`sdk.ErrNotFound` (missing secret or owner key), `sdk.ErrAlreadyExists`
(second owner key, duplicate audit id), `sdk.ErrConflict` (stale key
expectation on replace, `vault.ErrSealedValueCorrupt`, a value sealed under a
key the owner no longer holds), `sdk.ErrInvalidReference` (secret for an owner
without a key), `sdk.ErrInvalidInput` (validation, `cryptids.ErrUnknownKeyID`
when the master key a row needs was dropped). Error text never carries values
or key material.

## Stores

`stores/memory` is the reference. `stores/storetest.RunRepository` is the
executable contract every store runs, including the atomicity case (a failing
audit append leaves the change unwritten). `stores/pgx` is the PostgreSQL
module; see its README for the schema option and the live suite.
