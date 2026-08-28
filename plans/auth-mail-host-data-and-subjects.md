# Auth mail — host-owned DATA and SUBJECTS, not only bodies

**Feature:** `features/authentication` maintenance release from `v0.4.2`, then
forward-ported to `pockets/authentication` main
**Status:** PROPOSED — consumer ruling 2026-08-28: "hosts must be able to
override templates AND data; upstream provides the transport and a sensible
default". Prior evidence: the same gap was flagged by the consumer on 2026-08-17.
**Evidence consumer:** coordination-hub, currently pinned to
`features/authentication v0.4.2` + `sdk v0.4.1`. Its campaign invitation mails
"You were invited to campaign 0XN93b25ZNbHw8tCqWyfJ as contributor". Its domain
is evidence only; the surface below is generic.

## Problem

`Config.EmailContentTemplates` / `EmailLayouts` let a host replace the markup
of every delivery purpose, but the DATA those templates render is built by the
feature's callers and is closed:

- invitation → `{ResourceType, ResourceID, Relation, Link}`
  (`invitationsvc/service.go` `sendInviteSent`); member-added the same.
- Subjects and SMS bodies are the unexported `specs` table in
  `delivery/router.go` — not overridable.
- The sdk email renderer accepts no FuncMap, so a template cannot fetch data.
- The accept secret never leaves the feature, so a host cannot send the mail
  itself.

Net: a host can restyle "You were invited to campaign <id>" but cannot say
which campaign. Any host with resources that have names hits this on day one.

## Locked safety and lifecycle rules

1. `Secret`, `Link`, and `Subject` are framework-owned reserved fields. The
   host hook never receives `Secret`; it cannot add or replace any reserved
   field. The renderer inserts the original secret and subject only after the
   hook succeeds. This keeps the rendered credential identical to the secret
   stored in the sealed envelope and prevents a secret from entering a subject.
2. The hook receives a fresh map and returns a fresh map of additions and
   replacements. Nested `Metadata` is a defensive copy. The hook may run
   concurrently and must not retain or mutate input after return.
3. Hook error behavior follows the caller that renders:
   - invitation create/resend: render aborts before enqueue; create may already
     have persisted the invitation, and the caller receives the error so resend
     can recover;
   - member-added: the grant already committed, so failure remains best-effort
     and is logged;
   - opaque password-reset/passwordless/verification starts: the request was
     already accepted and queued; a worker-side hook error follows the existing
     bounded initializer retry/dead-letter policy and is never reported to the
     original caller.
   README/godoc must not claim universally that "nothing is queued" or "the
   caller sees it".
4. A rendered email subject must be non-empty and contain no CR or LF. Reject it
   during `Router.Render` before an envelope is queued. This release keeps the
   protection inside authentication so the `v0.4.3` backport can retain its
   existing `sdk v0.4.1` dependency.

## Proposal

1. Add `Config.DeliveryData DeliveryDataHook`:

   ```go
   // DeliveryDataHook enriches the feature-built, secret-free data for one
   // delivery render. Input is a fresh defensive map. The returned fields are
   // merged before Secret and Subject are inserted. Secret, Link, and Subject
   // are reserved and rejected if returned.
   //
   // The hook may be called concurrently. See Config godoc for synchronous,
   // best-effort, and worker-side error behavior.
   type DeliveryDataHook func(ctx context.Context, purpose string, data map[string]any) (map[string]any, error)
   ```

   Thread it as `delivery.Deps.DataHook` and call it at one site in
   `Router.Render`. The input may include `Link` for read-only context, but the
   renderer snapshots and restores it so the return value cannot replace it.
2. Add `Config.EmailSubjects map[string]string` and
   `Config.SMSBodies map[string]string`: purpose → Go `text/template` source,
   overriding `specs` per purpose and parsed at `NewRouter`.
   - reject unknown purpose keys at construction;
   - reject empty/whitespace-only sources;
   - reject SMS overrides for a purpose whose core spec has no SMS rail (an
     override customizes an existing rail; it does not enable a new kind);
   - parse with missing-key errors enabled, so data-contract mistakes fail the
     render rather than shipping `<no value>`.
3. Export every purpose name from the public `auth` package
   (`PurposeInvitation`, `PurposeMemberAdded`, … = `delivery.Purpose*`) so hosts
   can key override maps and switch in the hook without string literals.
4. Enrich invitation/member-added defaults:
   - both: `ResourceType`, `ResourceID`, `ResourceName` (empty default),
     `Relation`, `RelationLabel` (empty default), `ResourceKind` (empty default),
     `InvitedBy`, `InviterName` (empty default), `Metadata`, `OperationID`, and
     `Link`;
   - pending invitation: `InvitationID` and `OperationID` are the persisted
     invitation ID;
   - accepted invitation member-added: same;
   - direct-add member-added: there is no invitation row, so `InvitationID` is
     empty and `OperationID` is the already-minted grant operation ID.

   Thread the member-added context through `sendMemberAdded`; today its
   signature cannot supply these fields. Clone `Metadata` before exposing it.
   The bundled invitation/member-added email and SMS bodies render
   `{{or .ResourceName .ResourceID}}`, preserving today's output when the hook
   is nil while making a name-only enrichment immediately useful.

## Acceptance

- Nil hook + empty maps → existing rendered output remains byte-for-byte
  unchanged.
- Hook sees the expected public purpose and secret-free defensive data, may add
  or replace non-reserved fields, and may run concurrently without shared-map
  races.
- Returning `Secret`, `Link`, or `Subject` fails render with a typed invalid-input
  error; no overridden credential reaches a body, SMS, subject, or envelope.
- Hook error behavior is proven separately for invitation create/resend,
  member-added best-effort delivery, and an opaque worker-initialized flow.
- Subject/SMS override precedence is proven; unknown purpose, empty source,
  unsupported SMS rail, parse failure, missing execution data, empty rendered
  subject, and CR/LF rendered subject all fail as specified.
- Direct-add and accepted-invitation member-added tests pin `OperationID`,
  `InvitationID`, `InvitedBy`, and defensive `Metadata` semantics.
- README documents the per-purpose data table, reserved fields, concurrency and
  error behavior, plus one neutral lookup example that sets `ResourceName`.

## Release and forward port

1. Create a maintenance branch from the immutable
   `features/authentication/v0.4.2` tag and implement this contract without an
   sdk or store-module upgrade.
2. Tag that commit `features/authentication/v0.4.3`. Coordination Hub upgrades
   only `features/authentication v0.4.2` → `v0.4.3`; its `sdk v0.4.1` and
   `features/authentication/stores/pgx v0.3.0` pins remain unchanged.
3. Forward-port the same public contract, safety rules, and tests as a separate
   adapted commit on `pockets/authentication` main. It cannot literally be the
   same commit because the module and paths were renamed.
4. Include the forward port in the next normal pocket release after current
   `pockets/authentication/v0.8.0` (`v0.8.1` if cut independently, otherwise the
   next planned larger tag). Do not delay the coordination-hub backport on that
   release train.
