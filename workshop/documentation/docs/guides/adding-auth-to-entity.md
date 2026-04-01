---
sidebar_position: 3
title: Adding Authorization to an Entity
---

# Adding Authorization to an Entity

Step-by-step guide for adding relationship-based authorization (ReBAC) to a repository entity using `bridge.yml` configuration.

Authorization in gopernicus is declared in `bridge.yml` via `auth_relations`, `auth_permissions`, and route-level `authorize` middleware. The code generator reads this configuration and produces an authorization schema (`generated_authschema.go` in the bridge composite), relationship creation logic, and middleware wiring in the bridge layer.

This walkthrough uses a `projects` table with tenant scoping as a running example.

---

## Prerequisites

- Entity already generated (see [Adding a New Entity](adding-new-entity.md))
- Familiarity with ReBAC concepts: resources, relations, and permissions

---

## Concepts

**Relations** define who can be associated with a resource (e.g., `owner`, `member`, `tenant`). Each relation specifies which subject types are allowed.

**Permissions** define what actions are allowed and which relations grant them. Permissions can reference direct relations or inherit through parent resources.

**Authorization modes** on routes control how permission checks happen:
- `prefilter` -- filters list results to only resources the caller has access to
- `check` -- verifies permission on a specific resource by ID (for Get, Update, Delete)
- `postfilter` -- filters results after query execution (less common)

---

## Step 1: Add auth_relations to bridge.yml

Open the bridge package's `bridge.yml` file. Add `auth_relations` that describe who can interact with this resource.

**Tenant-scoped entity** (has a `tenant_id` FK):

When you scaffold with `gopernicus new repo`, the generator detects the `tenant_id` FK and produces these automatically in `bridge.yml`:

```yaml
auth_relations:
  - tenant(tenant)
  - owner(user, service_account)
```

**Entity without a parent FK**:

```yaml
auth_relations:
  - owner(user, service_account)
```

The syntax for relations is:

```
relation_name(subject_type1, subject_type2, ...)
```

Subject types reference other resource types in your authorization schema (e.g., `user`, `service_account`, `tenant`). Add custom relations as needed:

```yaml
auth_relations:
  - owner(user, service_account)
  - manager(user, service_account)
  - member(user, service_account)
  - viewer(user, service_account, group#member)
```

The `group#member` syntax means "any subject that has the `member` relation on a group" -- this enables indirect/nested authorization through group membership.

---

## Step 2: Add auth_permissions to bridge.yml

Permissions map actions to one or more relations. Add `auth_permissions` to `bridge.yml`:

```yaml
auth_permissions:
  - list(tenant->list)
  - create(tenant->manage)
  - read(owner|manager|tenant->read)
  - update(owner|manager|tenant->manage)
  - delete(owner|tenant->manage)
  - manage(owner|manager|tenant->manage)
```

Use `|` (pipe) for OR -- any of the listed relations grants the permission. Use `->` for inheritance through a parent relation:

In `read(owner|manager|tenant->read)`:
- `owner` -- direct: the caller has the `owner` relation on this resource
- `manager` -- direct: the caller has the `manager` relation on this resource
- `tenant->read` -- inherited: the caller has `read` permission on the parent `tenant` resource

The special relation `authenticated` means any authenticated caller (no specific relationship required).

---

## Step 3: Add authorize Middleware to Routes

Each route in `bridge.yml` can have `authorize` middleware in its middleware array. Three modes are available:

### prefilter (for List queries)

Prefilter asks the authorization system "which resources of this type can the caller access?" and passes the allowed IDs into the query's `AuthorizedIDs` filter field:

```yaml
routes:
  list:
    method: GET
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - authorize: prefilter(tenant:tenant_id, read)
```

The `tenant:tenant_id` syntax tells the prefilter to scope the check to the tenant specified by the `tenant_id` path parameter. For non-tenant-scoped entities, omit the scope:

```yaml
    middleware:
      - authenticate
      - authorize: prefilter(read)
```

### check (for single-resource operations)

Check verifies that the caller has a specific permission on the resource identified by the path parameter:

```yaml
routes:
  get:
    method: GET
    path: /tenants/{tenant_id}/projects/{project_id}
    middleware:
      - authenticate
      - authorize: check(read)
```

### postfilter (for queries returning multiple results)

Postfilter runs the query first, then filters results based on authorization. Use this when prefilter is not possible (e.g., complex joins):

```yaml
    middleware:
      - authorize: postfilter(read)
```

Prefer `prefilter` over `postfilter` when possible -- it is more efficient because the database only returns authorized rows.

---

## Step 4: Add auth.create Configuration for Create Routes

When a resource is created, you typically need to establish authorization relationships automatically. Add `auth_create` entries to the create route in `bridge.yml`:

```yaml
routes:
  create:
    method: POST
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - authorize: check(create)
    auth_create:
      - project:{project_id}#owner@{=subject}
      - project:{project_id}#tenant@tenant:{tenant_id}
```

The tuple syntax is:

```
resource_type:{id_column}#relation@subject_type:{subject_column}
```

- `project:{project_id}#owner@{=subject}` -- the authenticated caller (subject) becomes the `owner` of the new project
- `project:{project_id}#tenant@tenant:{tenant_id}` -- the tenant from the input becomes the `tenant` relation on the new project

`{=subject}` is a special token that resolves to the authenticated caller's identity at runtime. Column references like `{project_id}` and `{tenant_id}` resolve to values from the newly created row.

---

## Step 5: Run the Code Generator

```sh
gopernicus generate
```

The generator processes the `bridge.yml` configuration and produces:

- **`bridge/repositories/<domain>reposbridge/generated_authschema.go`** -- the authorization schema with relations and permissions as Go code
- **`bridge/repositories/<domain>reposbridge/authschema.go`** (bootstrap, created once) -- customization point that delegates to the generated schema
- **Bridge handler middleware wiring** -- `addGeneratedRoutes()` in `generated.go` wires the ordered middleware from `bridge.yml`

---

## Step 6: Customize authschema.go (if needed)

The bootstrap file `authschema.go` delegates to the generated schema by default:

```go
func AuthSchema() []authorization.ResourceSchema {
    return GeneratedAuthSchema()
}
```

To add custom relations or permissions not expressible via `bridge.yml`, modify the returned schema:

```go
func AuthSchema() []authorization.ResourceSchema {
    schemas := GeneratedAuthSchema()
    for i, s := range schemas {
        if s.Name == "project" {
            // Add a custom "auditor" relation
            s.Def.Relations["auditor"] = authorization.RelationDef{
                AllowedSubjects: []authorization.SubjectTypeRef{
                    {Type: "user"},
                },
            }
            // Auditors can read but not write
            s.Def.Permissions["audit"] = authorization.AnyOf(
                authorization.Direct("auditor"),
                authorization.Direct("owner"),
            )
            schemas[i] = s
        }
    }
    return schemas
}
```

The generated schema is the source of truth for annotation-declared relations. Only add relations here that cannot be expressed in `bridge.yml`.

---

## Step 7: Test Authorization

### Verify the generated schema

Inspect `generated_authschema.go` in the bridge composite directory to confirm all relations and permissions match your intent. The generated code uses `authorization.Direct()` for direct relations and `authorization.AnyOf()` for OR combinations.

### Write integration tests

Test that authorization checks work correctly by creating relationship tuples in tests and verifying access:

```go
func TestProjectAuthorization(t *testing.T) {
    // Create a project and verify the owner can read it
    // Verify a non-owner cannot read it
    // Verify a tenant member can list projects in that tenant
}
```

### Test authorization modes

- **prefilter**: Verify that List returns only resources the caller has access to
- **check**: Verify that Get/Update/Delete return 403 for unauthorized callers
- **auth_create**: Verify that after Create, the caller has the expected relations

---

## Quick Reference: Configuration Summary

| Configuration | Location | Purpose |
|---|---|---|
| `auth_relations` | `bridge.yml` | Define relations on the resource |
| `auth_permissions` | `bridge.yml` | Define permissions from relations |
| `authorize: prefilter(perm)` | Route middleware in `bridge.yml` | Filter list by authorized resources |
| `authorize: prefilter(scope:param, perm)` | Route middleware in `bridge.yml` | Scoped prefilter (tenant) |
| `authorize: check(perm)` | Route middleware in `bridge.yml` | Check permission on resource by ID |
| `authorize: postfilter(perm)` | Route middleware in `bridge.yml` | Filter results after execution |
| `auth_create` | Route config in `bridge.yml` | Write relationship tuples on create |
| `authenticate` | Route middleware in `bridge.yml` | Require authentication |
| `with_permissions` | Route middleware in `bridge.yml` | Include caller's permissions in response |

---

## Checklist

- [ ] `auth_relations` in `bridge.yml` define all relations for the resource
- [ ] `auth_permissions` in `bridge.yml` map actions to relation combinations
- [ ] `authorize` middleware on each route (prefilter for List, check for Get/Update/Delete)
- [ ] `auth_create` on Create route for automatic relationship creation
- [ ] `gopernicus generate` run successfully
- [ ] `generated_authschema.go` (in bridge composite) reviewed for correctness
- [ ] `authschema.go` customized if needed
- [ ] Authorization tested: owner access, non-owner denial, tenant scoping

---

## Related

- [Query Annotations](../gopernicus/topics/code-generation/annotations.md)
- [Bridge Configuration](../gopernicus/topics/code-generation/bridge-configuration.md)
- [CLI: generate](../cli/generate.md)
- [Adding a New Entity](adding-new-entity.md)
