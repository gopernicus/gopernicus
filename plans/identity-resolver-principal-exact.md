# identity.Resolver is principal-exact: no display-name synthesis

**Module:** `pockets/authentication` (next tag `v0.8.2`, patch)
**Status:** EXECUTED 2026-08-28 (branch `identity-resolver-principal-exact`,
awaiting owner PR/tag). Origin: gps-360-go plans/32 (the data-requests slice),
whose `requester_name` / `fulfilled_by` / `added_by` columns are stamped from
`identity.Info.DisplayName`; its owner ruled the same day that "identity
resolution is principal-exact: an existing `user:<id>` or
`service_account:<id>` resolves by that stored ID and uses its stored display
name; a registered user never derives a display name from an email/local part
or falls back to another identifier."

## Problem

`authsvc.(*Service).Resolve` looked the user up by principal ID (correct) but,
when the stored `DisplayName` was blank, projected the local part of the first
verified email (`bob@example.com` → `bob`). That is a fabricated display
identity: a consumer that stamps `Info.DisplayName` into a durable record
cannot tell a stored name from a guessed one, and a host that wants to REFUSE
an incomplete identity (403) cannot, because the Resolver hid the blank. The
sdk/foundation/identity port doc already says a Resolver "never fabricates an
Info"; a synthesized name is a fabricated field.

## Decision

- `Resolve` for `user` projects `u.DisplayName` exactly as stored; blank stays
  blank. `firstEmailLocalPart` / `emailLocalPart` are deleted.
- `Resolve` for `service_account` is unchanged (stored `Name`, no addresses).
- Identifier-value matching (an email → a user) is an admission/linking flow
  (invitation accept, OAuth link) and never enters `Resolve`; pinned by
  `TestResolveUserIsPrincipalExact` (a principal whose ID equals an email value
  is `sdk.ErrNotFound`).
- What a blank name MEANS is the consumer's decision (refuse, render the
  address, render nothing) — never the Resolver's.

## Change

- `internal/logic/authsvc/resolver.go` — fallback removed; doc rewritten.
- `internal/logic/authsvc/resolver_test.go` —
  `TestResolveUserDisplayNameFallsBackToEmailLocalPart` replaced by
  `TestResolveUserBlankDisplayNameStaysBlank` (blank name, email still
  projected); `TestResolveUserIsPrincipalExact` added.
- `authentication.go` — the public `Resolve` doc.
- No schema, no store change, no Config change, no pin moves.

## Versioning

Patch (`v0.8.2`): the port contract is unchanged and the previous behavior was
a contract violation ("never fabricates"). The one observable change — a user
with a blank display name now resolves with `DisplayName == ""` — is called out
in RELEASING so a host rendering `Info.DisplayName` unguarded can decide its own
fallback at the render site.
