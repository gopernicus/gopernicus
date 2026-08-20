# Invitation metadata — host key/values that ride an invitation to the Grant seam

**Feature:** `features/authentication`
**Status:** proposal, for owner review (patch-tag vs. batch — see the last section)
**Motivating consumer:** coordination-hub firm-aware vendor onboarding (below), but
the framework surface is generic host metadata.

## Problem

The `Granter` seam is the one ReBAC-decoupled hook a host implements — called on
accept, direct-add, and resolve-on-registration — and it is where a host turns an
accepted invitation into whatever "membership" means in its world. But an
invitation can only carry what the feature models: `{ResourceType, ResourceID,
Relation, Identifier, …}`. A host that needs to route *additional* information from
invite-time to grant-time has no channel for it.

Concretely (coordination-hub): a vendor person should be attached to their **firm**
(`vendor_org`) as part of accepting a campaign invitation. Today the host can only
attach them in a separate admin step *after* they exist and verify, because the
"which firm" fact cannot ride the invitation to the `Grant` call. The host has a
seam to *act* on acceptance and nothing to act *with*.

## Proposal

Add an **opt-in, host-defined `Metadata map[string]string`** to an invitation:
supplied at create, persisted, and echoed into `GrantInput` on every grant path.
The feature never interprets it — it is opaque routing data the host round-trips
through its own seam.

> Named **`Metadata`**, deliberately NOT `Context`, to avoid any confusion with
> Go's `context.Context` that threads every call here.

Empty metadata preserves today's grant semantics, so this is additive for hosts
that do not opt in. The new public field is still visible to compile-time clients
that inspect `GrantInput` or `InviteCheckRequest`.

### Design points

- **Opaque and host-owned.** The feature validates only shape/size, never keys or
  values. It is not a general document store — it is small routing data (a firm id,
  a plan tier), so bound it. The initial limits are **32 entries**, **64 bytes per
  key**, **256 bytes per value**, and **4 KiB total encoded metadata**. Limits are
  measured in UTF-8 bytes; empty keys are rejected, values may be empty, and invalid
  UTF-8 is rejected. Every violation wraps `sdk.ErrInvalidInput`.
- **Untrusted authorization input.** Metadata is supplied by the inviter and is
  never an authorization claim by itself. The create-time `InviteCheck` must receive
  the parsed metadata so a host can authorize the complete invitation, including
  fields such as `vendor_org_id`; the `Granter` must validate it again when applying
  any security-sensitive side effect. The plan therefore adds `Metadata` to
  `InviteCheckRequest` as well as `GrantInput`.
- **Round-trips unchanged.** What the host set at create is exactly what its
  `Granter` receives logically — no key/value normalization, merging, or ordering
  contract. The feature copies maps at API/domain/store boundaries so callers or
  Granters cannot mutate persisted or subsequently delivered state.
- **Canonical empty value.** Nil and empty input both persist as JSON `{}` and are
  delivered as an empty map. The store must not marshal a nil map as JSON `null`,
  because an explicit `NULL` JSON value bypasses the SQL column default.
- **All grant paths.** Accept, resolve-on-registration, and direct-add all deliver
  it, so a host does not have to special-case how a person arrived.
- **Not exposed to the invitee.** Metadata is issuer→host routing; it should not
  appear in the "my invitations" view a recipient reads. (Confirm against the
  `Mine` DTO.)

## Touch points (paths under `features/authentication/`)

Traced against `main` @ `8baece8`.

1. **Domain** — `domain/invitation/invitation.go`
   - Add `Metadata map[string]string` to `Invitation` (struct at line 49).
   - Thread through construction: prefer a `NewWithMetadata(...)` (or a functional
     option) over a new positional param on `New` (line 75) to avoid churning every
     existing caller. Validate bounds here.
   - Centralize the limits and validation in a reusable domain helper so both
     `NewWithMetadata` and service-level direct-add validation use the same rules.
2. **Stores** — both `stores/pgx/` and `stores/turso/`
   - New migration `stores/pgx/migrations/0016_invitation_metadata.sql`:
     `ALTER TABLE invitations ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'`.
   - Add the matching `stores/turso/migrations/0016_invitation_metadata.sql`, using
     the Turso/libSQL column representation (`TEXT NOT NULL DEFAULT '{}'`). The two
     migration trees must retain the same filename set.
   - Insert/scan the column in both invitation stores. Encode nil/empty maps as `{}`
     and decode `{}` as an empty map; malformed stored JSON is a store error.
   - Update both migration inventories, parity checks, schema probes, and store
     conformance tests. **This migration is the reason this is more than a trivial
     patch** — hosts copy it into their own ordered migration ledger.
3. **Service** — `internal/logic/invitationsvc/service.go`
   - `CreateInput` (line 203) gains `Metadata map[string]string`; `Create` (line
     372) validates it before identifier normalization, user lookup, or membership
     checks, then copies it into the pending invitation or direct-add grant.
   - `GrantInput` (line 119) gains `Metadata map[string]string`.
   - The private `grant(...)` helper (line 882) gains a `metadata` argument and puts
     it on `GrantInput`. Its three call sites pass the right source:
     - line 442 — **direct-add**: from `in.Metadata` (`CreateInput`).
     - line 525 — **accept**: from `inv.Metadata` (the stored row).
     - line 695 — **resolve-on-registration**: from `inv.Metadata`.
   - Copy metadata before passing it to the host and document that `Granter` must
     treat it as untrusted input and return an error for invalid or unauthorized
     host-side routing.
4. **Inbound** — `internal/inbound/authentication/invitation.go`
   - `createInvitationRequest` (line 41) gains an optional `metadata` object
     (`map[string]string`); the `Create` call (line 141) forwards it.
   - Pass the same metadata to `InviteCheckRequest` before calling the service, so
     issuance authorization covers the complete request.
   - Use the bounded, strict JSON decoder for this route (or add an equivalent
     route-specific body limit) so an oversized object is rejected before decoding
     an unbounded map. The service/domain limits remain authoritative.
5. **Facade / docs** — `authentication.go` (the `GrantInput` alias already
   re-exports), `InviteCheckRequest` documentation, the invitation route payload,
   and the `GrantInput` block in `features/authentication/README.md`. Also update
   the migration section from `0001–0015` to include `0016`.

## Backward compatibility

Every new field is optional; an omitted `metadata` persists `{}` and reaches the
`Granter` as an empty map. Existing grant behavior is unchanged when the map is
empty. Adding a field to the public `InviteCheckRequest` is source-compatible for
keyed struct literals and function implementations, but hosts using unkeyed
composite literals must switch to keyed literals. The migration is additive with a
default and applies cleanly to a populated `invitations` table; both dialects must
ship the migration before a binary that selects the new store code is deployed.

## Testing

- Domain: `New`/`NewWithMetadata` round-trips a map; over-size / too-many-entries
  reject with `sdk.ErrInvalidInput`; cover empty keys, byte limits, invalid UTF-8,
  nil/empty canonicalization, and direct-add validation before lookup/check side
  effects.
- Store: insert-then-read preserves the map in both pgx and Turso; nil/empty stores
  `{}`; a legacy row (pre-migration default) reads as empty; malformed stored JSON
  fails safely.
- Schema: migration inventory/parity, column probes, and populated-table upgrade
  coverage pass for both dialects.
- Service: a fake `Granter` asserts it receives the exact logical metadata on each
  of the three paths (accept, direct-add, resolve), receives a defensive copy, and
  sees empty metadata for legacy rows.
- Authorization: `InviteCheck` receives metadata and can reject an unauthorized
  routing value before persistence or direct-add.
- Inbound: a create request with a `metadata` object reaches both `InviteCheck` and
  `CreateInput`; absent is empty; oversized bodies and oversized metadata are
  rejected; create/resource-list/mine/resend responses never contain metadata.

## Consumer (coordination-hub, after a tag lands — not part of this repo)

- Console "invite a vendor person" sets `metadata: {"vendor_org_id": "<org>"}`.
- The host's `InviteCheck` first verifies that the inviter may use the selected firm
  for the campaign. Its invitation `Bridge.Grant` treats the value as untrusted,
  revalidates the firm/campaign invariant, and if present attaches the firm after
  applying the campaign relation — idempotent, because the firm assignment moves/
  no-ops and grants already replay idempotently.

## For the owner: patch-tag vs. batch

This is the one decision blocking implementation. Because it adds a **store
migration**, it is larger than the usual patch-tag, and coordination-hub is pinned
to the current released auth line on purpose (its `main` adoption is a separate,
larger batch). So:

- **Patch-tag** this feature onto the pinned line (cherry-pick, tag, the host
  adopts just this) — keeps the host on its pinned line; or
- **Land on `main`** as part of the batch and bring the whole adoption forward.

I have not tagged or vendored anything. Once you rule, I implement against that
decision. If patch-tag: the migration number and any store-schema drift between the
pinned line and `main` need reconciling — flag if the pinned line's
`invitations` table differs.
