---
title: Secrets
description: Owner-scoped secrets encrypted at rest, key rotation without downtime, and an append-only access log.
---

# Secrets

`pockets/secrets` stores secrets for an owner (a tenant, a site, whatever the
host scopes by) encrypted at rest, and records every read, write, delete and
key operation in an audit log written in the same transaction as the change.

Secrets registers no HTTP routes. Which principal may read which owner's
secrets is host policy on host routes.

## Two layers of keys

- Each owner has one **data key**. Values are sealed under it with AES-256-GCM,
  with the secret's reference as additional authenticated data, so a
  ciphertext copied to another row does not open.
- Data keys are stored **wrapped**. The bundled `vault.RingWrapper` wraps them
  with a `cryptids.KeyedEncrypter` (`cryptids.NewAESGCMRing`) over keys the
  host reads from its environment. A cloud KMS can replace it through the
  `vault.Wrapper` port.

Rotating the master key therefore rewraps one row per owner, not every
secret, and deleting an owner destroys its data key so its values are
unreadable even in backups.

## Attribution and audit

Every `Put`, `Get`, `Delete` and `DeleteOwner` takes a `vault.Source`: exactly
one actor pair or a system, with an optional reason. A read without one is
refused. Audit records carry the action, owner, name, version, key id, source
and time, never a value, and the pocket offers nothing that removes them.

## Rotation

`rotation.Service.RewrapOwnerKeys` follows a master key change: add the new
key, make it active, rewrap, drop the old key. It is idempotent and reports
owners whose key is under a wrapper key no longer held.
`rotation.Service.RotateOwnerKey` gives one owner a fresh data key and
reseals its secrets in one transaction.

## Stores

`stores/memory` is the reference, `stores/storetest.RunRepository` the
executable contract, and `stores/pgx` the PostgreSQL module with `WithSchema`,
`StatusCheck` and `ExportMigrations`.

See the [pocket README](https://github.com/gopernicus/gopernicus/tree/main/pockets/secrets)
for construction and the rotation runbook.
