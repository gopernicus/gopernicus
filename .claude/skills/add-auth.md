---
name: add-auth
description: Interactive workflow to add relationship-based authorization (ReBAC) to an entity
---

# Add Authorization Workflow

You are guiding the user through adding relationship-based authorization (ReBAC) to an existing entity. This configures who can do what via relations and permissions in bridge.yml. Move through each phase one at a time.

## Phase 1: Understand the Resource

Start by asking:

- Which entity are you adding authorization to? (e.g., projects, documents, invoices)
- Is it scoped to a parent resource? (e.g., tenant-scoped via tenant_id FK, or standalone)
- Who interacts with this resource? Think about roles: owner, member, viewer, manager, admin

Read the entity's existing bridge.yml to understand current routes and middleware.

## Phase 2: Design Relations

Relations define who can be associated with a resource. Walk through:

- Ask: Who creates this resource? That is typically the "owner" relation.
- Ask: Is it scoped to a tenant? If so, add a "tenant(tenant)" relation.
- Ask: Are there other roles? (member, viewer, manager, editor)
- Ask: Should any roles come through group membership? (e.g., viewer(user, service_account, group#member))

Write the auth_relations block and show it:

```yaml
auth_relations:
  - tenant(tenant)
  - owner(user, service_account)
  - member(user, service_account)
```

Confirm before proceeding.

## Phase 3: Design Permissions

Permissions map actions to relations. For each CRUD operation plus any custom actions:

- Ask: Who can list? (typically anyone with read on the parent tenant)
- Ask: Who can create? (typically anyone with manage on the parent tenant)
- Ask: Who can read? (owner, member, or anyone with read on the tenant)
- Ask: Who can update? (owner, or anyone with manage on the tenant)
- Ask: Who can delete? (owner, or anyone with manage on the tenant)
- Ask: Any custom permissions? (e.g., "audit", "transfer", "archive")

Explain the syntax as you go:
- `|` means OR — any of these relations grants the permission
- `->` means inheritance — checks permission on the parent resource
- `authenticated` means any authenticated caller

Write the auth_permissions block and show it:

```yaml
auth_permissions:
  - list(tenant->list)
  - create(tenant->manage)
  - read(owner|member|tenant->read)
  - update(owner|tenant->manage)
  - delete(owner|tenant->manage)
```

Confirm before proceeding.

## Phase 4: Configure Route Middleware

For each route in bridge.yml, add the appropriate authorize middleware:

- **List routes**: Use `authorize: prefilter(tenant:tenant_id, read)` for tenant-scoped, or `authorize: prefilter(read)` for standalone
- **Get/Update/Delete routes**: Use `authorize: check(read)`, `authorize: check(update)`, etc.
- **Create routes**: Use `authorize: check(create)` — this checks against the parent resource

Ask about each route: "For the [method] [path] route, should we check [permission]?"

Also ask: "Should any routes include `with_permissions` middleware to return the caller's permissions in the response?"

## Phase 5: Configure auth_create

For the create route, relationship tuples need to be written automatically:

- The creator becomes the owner: `<entity>:{<entity>_id}#owner@{=subject}`
- If tenant-scoped, link the tenant: `<entity>:{<entity>_id}#tenant@tenant:{tenant_id}`

Show the auth_create block and explain the tuple syntax. Confirm.

## Phase 6: Apply and Generate

1. Write the complete bridge.yml changes (show the full file diff)
2. Confirm with the user
3. Run: `gopernicus generate <domain>`
4. Run: `go build ./...` to verify

## Phase 7: Review Generated Schema

1. Read the generated_authschema.go file in the bridge composite directory
2. Walk through it with the user — confirm relations and permissions match intent
3. Check authschema.go (bootstrap file) — ask if any custom relations or permissions are needed beyond what bridge.yml can express

## Phase 8: Verify

1. Confirm the build passes
2. Summarize: what relations exist, what permissions are enforced, on which routes
3. Suggest testing: "Create a resource, verify the creator has owner access, verify a non-owner gets 403"

## Reference

See workshop/documentation/gopernicus/guides/adding-auth-to-entity.md for the full guide.
