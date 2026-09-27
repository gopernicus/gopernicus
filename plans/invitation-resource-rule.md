# Invitation resource rule (pockets/authentication)

Status: BUILT 2026-09-27 (ratified by owner "build" request) — tasks 1–8 done and verified; task 9 (release) awaits owner. Release target: `pockets/authentication/v0.15.0` (MINOR, additive).

## Problem

Invitation authorization is split across two axes today:

- create/list → host `InviteCheck` (`inbound/http/invitation.go:200`, `:229`).
- cancel/resend → inviter-only (`prepareInvitationManagement`, `invitation.go:343`:
  `prepared.InvitedBy() != caller` → 403).

Who may administer invitations is a property of the RESOURCE. With inviter-only management a
co-manager cannot clean up a colleague's invitation, and a person who lost access still controls
the invitations they sent. Hosts must not write per-invitation authorization data or reimplement
cancel/resend to fix this.

## Design

### D1. Config (additive, `InvitationsConfig`)

```go
// ResourcePermissions maps an invitable resource type to the permission a principal
// must hold ON THAT RESOURCE to administer its invitations. A type absent from the
// map is not invitable (refused before any check).
ResourcePermissions map[string]string
// Can answers "does principal hold permission on (resourceType, resourceID)?".
// The host adapts its authorizer. An error fails closed.
Can inbound.InvitationCan
```

`inbound.InvitationCan` is a named func type in `inbound/http/host_policy.go`:
`func(ctx context.Context, p sdk.Principal, permission, resourceType, resourceID string) (bool, error)`
(a func literal of the pasted signature assigns to it directly). The pocket never imports
authorization.

Inbound gets one option carrying both halves together so the adapter can't be half-wired either:

```go
type InvitationResourceRule struct {
    Permissions map[string]string
    Can         InvitationCan
}
func WithInvitationResourceRule(rule InvitationResourceRule) Option
```

`WithInvitations` copies both fields; the map is defensively cloned at construction.

**Rule-only authority:** without `InviteCheck`, the mapped permission authorizes creation of
invitations for every relation the host's `Granter` accepts, including `owner`, with any
domain-valid metadata. `Can` receives neither relation nor metadata; the pocket's shape and
non-empty-field validation does not authorize those values. Hosts that restrict which relations,
metadata routing choices, or invitees a manager may use must also wire `InviteCheck`. For example,
a manager allowed to invite members but not owners needs a create check that refuses `owner`
even after `Can` succeeds. This applies to both pending invitations and immediate direct-add.
`Granter` continues to enforce resource existence and data invariants when applying the grant;
it does not replace issuance-time authorization.

### D2. Construction contracts (`constructor.go` and `inbound/http/adapter.go`)

The root `New` uses this matrix, alongside its existing repository/dependency validation:

| Granter | ResourcePermissions | Can | InviteCheck | Result |
|---|---|---|---|---|
| set | non-empty valid map | set | any | OK — rule ON (InviteCheck, if set, is the create-only refinement) |
| set | empty/nil | nil | set | OK — rule OFF, today's behaviour exactly |
| set | empty/nil | nil | nil | `ErrInviteCheckRequired` (name kept for `errors.Is`; message reworded: "set ResourcePermissions+Can or InviteCheck") |
| any | non-empty | nil | any | NEW `ErrInvitationResourceRuleIncomplete` |
| any | empty/nil | set | any | `ErrInvitationResourceRuleIncomplete` |
| any | map with an empty key or empty permission value | set | any | `ErrInvitationResourceRuleIncomplete` (invalid entry) |
| nil | non-empty valid map | set | any | NEW `ErrInvitationResourceRuleWithoutGranter` |
| nil | empty/nil | nil | set | `ErrInviteCheckWithoutGranter` (unchanged) |
| nil | empty/nil | nil | nil | OK — invitations OFF |

Within invitation-policy validation, incomplete/invalid rules are checked before missing
invitation support: a lone map or lone `Can` without a `Granter` reports
`ErrInvitationResourceRuleIncomplete`. A complete valid rule without a `Granter` reports
`ErrInvitationResourceRuleWithoutGranter`, even when `InviteCheck` is also supplied.

The independently usable `inbound.New` enforces the equivalent admission rules against
`InvitationService` presence (`nilDependency(cfg.Invitations)`, including typed nils), since it
receives no `Granter`. It rejects incomplete/invalid rules first, requires either a complete rule
or `InviteCheck` when a service is present, and rejects either policy when no service is present.
No service and no policy remains valid, with invitation routes unmounted. Each inbound
invitation-policy configuration failure wraps `sdk.ErrInvalidInput` with a specific diagnostic.
Root configuration sentinels remain owned by the root package; inbound must not import that
package or promise `errors.Is` matches against those sentinels. Mirror validation behavior and
precedence, not error identity or dependency names.

### D3. Enforcement (rule ON), all in `inbound/http`

One helper, `h.checkResource(ctx, principal, resourceType, resourceID) error`:

1. `permission, ok := rule.Permissions[resourceType]`; `!ok` → deny **without calling Can**.
2. `ctx.Err()` check, then `allowed, err := rule.Can(...)`; `err != nil` → return err (fails closed,
   mapped by `web.RespondJSONDomainError`; a host that returns something wrapping `sdk.ErrNotFound`
   gets a 404); `!allowed` → deny; then `ctx.Err()` again (the `checkInvite` shape).

Per action:

- **create** — after `PrepareCreate` (so the map lookup uses the trimmed `in.ResourceType`),
  exactly where `checkInvite` runs today: `checkResource`; then, if `InviteCheck` is set,
  `checkInvite` with the unchanged `InviteCheckRequest`. Deny → `sdk.ErrForbidden` (403). No row,
  no grant.
- **list** — `checkResource` on the path params; `InviteCheck` is NOT called. Deny → 403.
- **resend / cancel** — `PrepareManagement` pins the row; `checkResource` against the LOADED row's
  `ResourceType()/ResourceID()` (two new additive accessors on `PreparedManagement`). The
  `InvitedBy` comparison is skipped entirely. Deny (unmapped, or `Can` false) → **`sdk.ErrNotFound`
  (404)**, indistinguishable from an unknown invitation id — non-enumerating. The `id`/`ID()`
  target-mismatch guard stays.

Rule OFF: existing authorization decisions and HTTP outcomes remain unchanged (create/list →
`InviteCheck`; resend/cancel → inviter-only 403). Audit attribution gains the fields in D5.

Status summary, rule ON: create 403 · list 403 · resend/cancel 404 · `Can` error → its own
mapping (5xx for infra, 404 if it wraps `sdk.ErrNotFound`). Documented in README.

### D4. Accept / decline / mine — unchanged. Acceptance never runs `Can`.

### D5. Audit — acting principal

`PreparedManagement` gains `WithActor(p sdk.Principal) PreparedManagement` (returns a copy;
additive, no signature change to `Cancel`/`Resend` or the `InvitationService` interface). The HTTP
handlers always attach the authenticated user caller. This does not change the bundled routes'
user-only admission. Headless callers may attach another principal type for attribution; actor
data never authorizes the command.

In `logic/invitations/service.go`, cancel (`invitation_cancelled`) and resend
(`invitation_created`, as today — no new event type) populate the existing canonical
`securityevent.SecurityEvent.Actor` field, converting the supplied principal to
`securityevent.Principal`. The stores already persist this field as `actor_type` / `actor_id`;
do not duplicate those keys in `Details`.

| Attached actor | `SecurityEvent.Actor` | `SecurityEvent.UserID` |
|---|---|---|
| None / zero principal | Zero value | `InvitedBy` (existing headless attribution) |
| User principal | Supplied type and ID | Actor ID |
| Non-user principal, including a service account | Supplied type and ID | Empty; never put a non-user ID in `UserID` |

`Details` gains `invited_by` for both operations. Under rule OFF the HTTP actor is the inviter,
so `UserID` stays unchanged while `Actor` is now populated. Create, grant, and decline attribution
remain unchanged; avoid changing them incidentally through shared audit helpers. No store or
migration change is needed.

### D6. Projections unchanged. `InvitedBy` stays on the owner projection. Its doc comments
(`invitationResponse.InvitedBy`, README "rendering hint" paragraph) are reworded: it's the admission
key only under rule OFF.

## Tasks

1. `inbound/http/host_policy.go` — `InvitationCan`, `InvitationResourceRule`, `checkResource`;
   `options.go` `WithInvitationResourceRule`; `adapter.go`/`routes.go` plumb + validate using D2's
   service-presence checks and inbound `sdk.ErrInvalidInput` error contract. Extend the root
   `Makefile`'s `guard-inbound-authorization` (G28) to reject the new callback/rule names and
   `checkResource` under authentication logic, alongside the existing policy names.
2. `inbound/http/invitation.go` — D3 per action; `prepareInvitationManagement` branches on rule.
3. `logic/invitations/management.go` — `ResourceType()`, `ResourceID()`, `WithActor`; `service.go`
   — D5 canonical `SecurityEvent.Actor`, user-only `UserID`, and `invited_by` audit details.
4. `config.go`, `constructor.go`, `configuration_errors.go` — fields, `WithInvitations` copy, D2
   matrix, two new sentinels, reworded `ErrInviteCheckRequired` message.
5. Tests (new `inbound/http/invitation_resource_rule_test.go` + constructor matrix cases +
   `logic/invitations` audit case):
   - rule ON: non-inviter holder of P can create, list, resend, cancel;
   - rule ON: non-holder refused on all four — no row, no write, no delivery, token hash unchanged;
   - rule ON: original inviter who lost P refused resend/cancel (404);
   - rule ON: unmapped type refused on all four, `Can` call count 0;
   - `Can` error fails closed on all four (no state change);
   - rule ON + InviteCheck: InviteCheck called only on create, only after `Can` true (ordering
     recorded; not called when `Can` false; not called on list/resend/cancel);
   - rule ON + InviteCheck: a member-only manager passes `Can` but is refused an `owner`
     invitation by `InviteCheck`; test both pending and known-invitee direct-add paths, with no
     row, grant, or delivery on denial. Also cover a metadata-dependent policy refusal;
   - rule ON without InviteCheck: creation passes the requested relation and metadata to the
     normal pending/direct-add path, including `owner` when the test granter accepts it,
     explicitly pinning the broad authority documented in D1;
   - rule OFF + InviteCheck: existing suite passes unchanged (incl.
     `TestInvitationManagementHTTPAdmissionBeforeWritesOrDelivery`);
   - construction matrix: lone map, lone Can, neither-with-Granter, rule-without-Granter,
     InviteCheck-without-Granter, invalid map entry, and fully disabled configuration. At root
     `New`, assert the exact sentinel via `errors.Is`; at `inbound.New`, substitute service
     presence (including typed-nil absence) for Granter and assert `sdk.ErrInvalidInput` for
     rejected configurations. Cover incomplete-before-absent precedence in both constructors;
   - audit: resend/cancel by a co-manager record canonical `Actor`, actor `UserID`, and original
     `invited_by`; headless service-account actors retain their canonical type/ID with empty
     `UserID`; no-actor headless calls preserve the zero `Actor` / `InvitedBy` fallback. Cover
     rule-OFF HTTP attribution and verify create/grant/decline attribution remains unchanged.
6. README — Invitations section leads with the resource rule; `InviteCheck` documented as the
   optional create-only refinement / the legacy rule-OFF mode. State D1's rule-only authority
   explicitly, with a member-versus-owner example that retains `InviteCheck` and explains when
   metadata/invitee checks are required. Document D2's separate constructor error contracts and
   D5's canonical actor attribution. Update route bullets, status table, config table row
   (line ~1277), and "Invitation management" section.
7. Verify: `go build ./... && go vet ./... && go test ./...` in `pockets/authentication`, plus
   `examples/auth-cms` (rule OFF consumer, must be untouched), then `make guard` at the repository
   root, including the extended G28 check.
8. Real-behaviour check: drive the HTTP surface end-to-end against the in-memory fixture with a
   two-manager scenario (A invites, B resends and cancels, A-after-revocation is refused) via
   `httptest` + curl-style requests — not just unit assertions.
9. Release per RELEASING.md: PR → merge → tag `pockets/authentication/v0.15.0` → cold-verify via the
   proxy (poll `.info` before any `go mod tidy`). Stores modules untouched, no migration.

## Out of scope

- Migrating `examples/auth-cms` to the rule (it stays on InviteCheck; could be a follow-up demo).
- A dedicated `invitation_resent` event type.
- HTML (views/goth) invitation UI — none exists.

## Segovia

Pin `pockets/authentication v0.15.0`. That jump from v0.13.2 also brings v0.14.0 (browser session
recovery after access expiry — read its RELEASING.md entry before repinning).
