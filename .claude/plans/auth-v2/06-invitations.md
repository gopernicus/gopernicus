# Phase A6 — invitations, ReBAC-decoupled

Status: RATIFIED (cut from design §6 — ratified AV4)
Executor model: opus
Depends on: A1 + A5 (grants and failures are audit-logged).
Design doc: `.claude/plans/roadmap/auth-v2-feature-design.md` §6 (the
whole section: the decoupled entity, the grant-only `Granter`, visibility
via table columns NEVER tuples, `MemberCheck` split out, deny-by-absence
routes, the pinned resolve-on-registration wiring), §2.2 (the Granter is
a consumer-declared seam — `features/authorization` does not exist yet
and NOTHING here may anticipate it), §3 (the redirect allowlist guards
destination paths).

## Work items

1. **`logic/invitation`** (public rim): `Invitation{ID, ResourceType,
   ResourceID, Relation, Identifier (email), ResolvedSubjectID,
   InvitedBy, TokenHash, AutoAccept, Status
   (pending|accepted|declined|cancelled|expired), ExpiresAt, AcceptedAt,
   CreatedAt, UpdatedAt}` — `Relation` is an opaque string the Granter
   interprets. Repository port: `Create` (uniqueness: one PENDING
   invitation per `(resource_type, resource_id, identifier, relation)` →
   `errs.ErrAlreadyExists`), `GetByTokenHash`, `Get`,
   `ListByResource` / `ListBySubject` (`sdk/crud`-typed; **port docs pin
   `ORDER BY created_at DESC, id DESC` — the id tiebreak is
   contractual**, plan-cut amendment), `UpdateStatus`; expiry surfaces
   `errs.ErrExpired` on token-hash reads past `ExpiresAt`.
2. **Seams on `auth.Config`** (cut refinement 7): `Granter` (design
   §2.2's exact shape — `Grant(ctx, resourceType, resourceID, relation,
   subjectType, subjectID) error`, grant-on-accept ONLY) and
   `MemberCheck` (optional duplicate-membership predicate; nil → no dup
   check). Nil `Granter` → the entire invitation route surface is NOT
   registered (deny-by-absence); `Granter` wired with
   `Repositories.Invitations` nil → loud `ErrInvitationRepoRequired`.
3. **`invitationsvc`** (`internal/logic/`): Create (token = 32-char
   dotless secret, SHA-256 at rest via `cryptids.SHA256Hasher`, plaintext
   only in the mail; optional `MemberCheck`; mail via `Config.Mailer` +
   the sdk/email template registry — invite-sent template), Accept
   (consume by token hash → `Grant` → mark accepted + set
   `ResolvedSubjectID`; member-added mail), Decline / Cancel / Resend
   (plain `InvitedBy == current user` ownership check — NO tuples,
   NO invitation-as-resource anything), direct-add path (known user +
   `AutoAccept` → immediate grant). Grant failures and grants are
   audit-logged via A5's helper.
4. **Resolve-on-registration** (design §6 pin): `authsvc`'s
   register/verify flow calls the narrow internal port
   `resolveInvitations(ctx, email, subjectType, subjectID) (int, error)`
   satisfied by `invitationsvc`, injected as an optional collaborator
   (nil when invitations are off); best-effort per invitation — one
   failed grant never aborts registration, each failure audit-logged.
5. **Routes** (design §6; session-gated except decline):
   `POST /auth/invitations/{resource_type}/{resource_id}`,
   `GET /auth/invitations/{resource_type}/{resource_id}`,
   `GET /auth/invitations/mine`, `POST /auth/invitations/accept`,
   `POST /auth/invitations/{id}/cancel`,
   `POST /auth/invitations/{id}/resend`,
   `POST /auth/invitations/{id}/decline` (public, rate-limited).
   Destination paths in mail guarded by A2's allowlist matcher.
6. **`storetest` sub-runner** (+ reference impl): uniqueness — incl. the
   **partial-index predicate case (plan-cut amendment): a second PENDING
   invite for the same `(resource_type, resource_id, identifier,
   relation)` → `ErrAlreadyExists`; after `UpdateStatus` moves the first
   off pending, a NEW pending invite for the same tuple SUCCEEDS** —
   token-hash lookup + expiry, status transitions, ListBySubject/
   ByResource pagination with the same-`created_at` collision case
   (identical order + `NextCursor` across implementations; reference
   sorts the full population then pages).
7. **Tests**: full Inviter flows with a fake Granter (grant called with
   exactly the invitation's tuple-shaped args), deny-by-absence
   (no-Granter → routes absent), MemberCheck dup path,
   resolve-on-registration best-effort semantics, ownership checks on
   cancel/resend.

## Acceptance

```sh
cd features/auth && go build ./... && go vet ./... && go test ./...
make check
```

Rule-6 grep INCLUDING the future module (import-anchored, plan-cut form):
`grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/features/(cms|jobs|authorization)' features/auth/`
→ empty.

## Real-interaction check

Standing check (a) + check (b). Deny-by-absence proof: boot
`examples/auth-cms` (no Granter wired yet) →
`curl -s -o /dev/null -w '%{http_code}' localhost:8082/auth/invitations/mine`
→ **404**. The wired invite→accept→grant run-and-look (toy membership
Granter) is A9's protocol.

## Execution log

(append dated entries here)
