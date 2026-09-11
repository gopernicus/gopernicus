# Natural tuple storage upgrade

Existing Firestore databases need an explicit maintenance upgrade before this
adapter serves authorization traffic. New databases need only the current index
manifest. Constructors do not migrate data.

The upgrade removes `relationship_id` and `created_at` from relationship rows,
`created_at` from role rows, and `relationship_id` from subject claims. It deletes
the obsolete `iam_relationship_ids` collection. Relationship document IDs remain
the hash of the complete six-field tuple; the subject claim still enforces one
relation per exact subject and resource.

Relationship pagination now exposes `tuple_key ASC` instead of timestamp/ID
ordering. Internally it indexes `(tuple_key_prefix, tuple_key_suffix)` to avoid
Firestore's 1500-byte indexed-value limit. Old records lack these fields and
would be omitted by the new listing queries until backfilled. Role pagination
uses `role_key ASC`. Discard outstanding listing cursors during the upgrade.

1. Back up the authorization collections and stop every authorization reader
   and writer, including old application instances, jobs, raw-store writers,
   and administrative commands. Keep them stopped through validation and any
   repairs; an old writer can reintroduce retired metadata or missing sort keys.
2. Build the maintenance executable against this adapter version. Export the
   current `IndexesFS` using `ExportIndexes`, merge it into the host's manifest,
   deploy it, and wait for the new composite indexes to become ready. The
   exporter preserves unrelated and old entries; it does not deploy indexes.
3. Open the intended database using the Firestore connector and call:

   ```go
   if err := authorizationfirestore.UpgradeTupleStorage(ctx, db); err != nil {
       return err
   }
   ```

   Call this from a deliberate maintenance command, before constructing the
   application services. It reads by document ID in pages of 100, writes each
   changed document with its observed update-time precondition, and deletes old
   ID claims only after relationship, role, and subject-claim updates finish.
   The helper does not create a database, deploy indexes, or run at startup.
4. If interrupted, keep traffic stopped and rerun the helper from the beginning.
   The operation is idempotent. If validation fails, repair the identified
   legacy data while writers remain stopped, then rerun. In particular, opaque
   fields must be valid UTF-8, at most 256 bytes, and contain no control
   characters (including U+0001); role resource type and ID must be both present
   or both empty. Mismatched tuple/role document hashes also require repair.
   The helper does not silently reinterpret invalid identities. An unexpected
   concurrent document update fails its precondition instead of being overwritten.
5. Verify every relationship has the two derived keys, relationship and role
   rows contain no removed metadata, subject claims retain their tuple owners,
   and `iam_relationship_ids` is empty. Compare tuple and role counts with the
   backup and exercise forward/reverse listings. Start only upgraded application
   instances. Retire old timestamp/relationship-ID index entries after confirming
   that no deployed query uses them; retain unrelated host indexes.

The tuple helper preserves facts and their document identities. It does not alter
the retired mutation ledger; remove that ledger with the separate operation below. A rollback to the old adapter
requires restoring the backup and compatible indexes during another maintenance
window; the discarded surrogate metadata cannot be reconstructed exactly.

`TestTupleStorageUpgradePreservesFactsAndIsRerunnable` covers old records missing
new keys, more than one maintenance page, retained facts/claims, metadata and ID
claim removal, and repeated execution. `TestTupleStorageUpgradeRejectsInvalidLegacyKeys`
checks invalid legacy inputs. The emulator does not enforce indexes; live index
validation remains a separate deployment check.

## Removing the retired mutation protocol

This release removes `MutationID`, durable replay, expected revisions and scope
anchors. Update callers to ordinary current-state commands and `mutation.Result`.
A retry after a lost response can produce a new effect if another writer changed
state in between; event IDs provide grouping only, never durable deduplication.

Archive `iam_mutations` and `iam_scopes` first if their old contents matter to the
host. Stop every old authorization writer, then run:

```go
if err := authzfirestore.RemoveLegacyMutationStorage(ctx, db); err != nil {
    return err
}
```

This paged operation is rerunnable, removes both retired collections with
update-time preconditions, and preserves tuples, subject claims, roles and any
new audit records. Do not restart an old adapter afterward: it can recreate the
removed ledger. A partial failure identifies its document; keep writers stopped,
resolve the failure and rerun. There is no startup cleanup and no fabricated
historical audit backfill. Restore the backup to roll back to the old protocol.

## Enabling optional history

Merge `ExportAuditIndexes` into the host manifest, deploy and wait for readiness,
then construct with `WithAudit()`. Raw and trusted writes must attach an explicit
`audit.Source`; guarded service paths supply the authenticated actor. Recording
is off by default, and missing attribution with recording enabled is an error
even when a write would be a no-op. All writers must use the enabled repositories
if the host requires complete coverage; out-of-band database edits bypass audit.
The `Audit` reader remains available after recording is disabled. Hosts retain,
archive and delete history according to their own policy; no TTL is configured.
