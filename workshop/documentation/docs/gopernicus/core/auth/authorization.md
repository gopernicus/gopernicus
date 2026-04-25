---
sidebar_position: 2
title: Authorization
---

# Core — Authorization

The authorization package (`core/auth/authorization/`) implements relationship-based access control (ReBAC). It evaluates permission checks against a declarative schema that maps relations to permissions, with support for through-relation traversal, group expansion, batch operations, and resource lookup.

The design draws inspiration from Google's [Zanzibar](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/) paper — the same relationship tuple model that underlies SpiceDB, OpenFGA, and Authzed. The central type is the `Authorizer`. It depends on a `Storer` interface for relationship persistence — designed so that the default pgx-backed store can be swapped for alternatives like OpenFGA or SpiceDB without changing domain code.

## Setup

### Constructor

```go
authorizer := authorization.NewAuthorizer(
    store,   // Storer implementation (e.g., satisfier wrapping rebacrelationships repo)
    schema,  // Schema built from resource definitions
    cfg,     // Config (parsed from environment)
    authorization.WithLogger(log),
)
```

### Configuration

```go
type Config struct {
    MaxTraversalDepth int `env:"AUTHORIZATION_TRAVERSAL_MAX_DEPTH" default:"10"`
}
```

`MaxTraversalDepth` prevents infinite recursion in through-relation chains. Values <= 0 are reset to the default.

## Core Concepts

Authorization decisions are modeled as relationships between subjects and resources, evaluated against a schema.

- **Subject** — who is requesting access (`user`, `apikey`, `service_account`)
- **Resource** — what is being accessed (`org`, `project`, `document`)
- **Relation** — how a subject relates to a resource (`owner`, `member`, `viewer`)
- **Permission** — what a subject can do (`read`, `edit`, `delete`, `manage`)

Permissions are computed from relations via the schema. A subject does not "have" a permission directly — they have relations, and the schema defines which relations grant which permissions.

## Schema DSL

Schemas are built from resource type definitions using a small DSL:

```go
schema := authorization.NewSchema(
    []authorization.ResourceSchema{
        {
            Name: "org",
            Def: authorization.ResourceTypeDef{
                Relations: map[string]authorization.RelationDef{
                    "owner": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
                    "admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
                    "member": {AllowedSubjects: []authorization.SubjectTypeRef{
                        {Type: "user"},
                        {Type: "group", Relation: "member"},  // group membership expansion
                    }},
                },
                Permissions: map[string]authorization.PermissionRule{
                    "manage": authorization.AnyOf(
                        authorization.Direct("owner"),
                    ),
                    "edit": authorization.AnyOf(
                        authorization.Direct("owner"),
                        authorization.Direct("admin"),
                    ),
                    "read": authorization.AnyOf(
                        authorization.Direct("owner"),
                        authorization.Direct("admin"),
                        authorization.Direct("member"),
                    ),
                },
            },
        },
        {
            Name: "project",
            Def: authorization.ResourceTypeDef{
                Relations: map[string]authorization.RelationDef{
                    "org":    {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "org"}}},
                    "editor": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
                },
                Permissions: map[string]authorization.PermissionRule{
                    "edit": authorization.AnyOf(
                        authorization.Direct("editor"),
                        authorization.Through("org", "admin"),  // org admins can edit projects
                    ),
                    "read": authorization.AnyOf(
                        authorization.Direct("editor"),
                        authorization.Through("org", "read"),   // org members can read projects
                    ),
                },
            },
        },
    },
)
```

### DSL Helpers

| Helper | Meaning |
|---|---|
| `Direct(relation)` | Subject has this relation directly on the resource |
| `Through(relation, permission)` | Traverse the relation to another resource, check permission there |
| `AnyOf(checks...)` | Any of the checks grants the permission (union/OR) |
| `Remove()` | Signal deletion during schema merge (for overriding inherited permissions) |

**Through-relations** are the key to hierarchical access. `Through("org", "admin")` on a project means: "find the org this project belongs to, then check if the subject is an admin on that org." The authorizer traverses this chain recursively, with cycle detection and depth limits.

### Schema Composition

`NewSchema` accepts multiple slices of resource schemas and merges them. This allows different domains to contribute their own schemas independently:

```go
schema := authorization.NewSchema(
    tenancySchemas,   // org, team definitions
    projectSchemas,   // project definitions
    contentSchemas,   // document definitions
)
```

`MergeResourceType` handles conflicts — override takes precedence. Use `Remove()` to explicitly delete a permission from a base schema.

### Schema Validation

```go
err := authorization.ValidateSchema(schema)
```

Validates that:
- Through-relations reference defined relations on the resource type
- Permissions referenced in through-checks exist on the target type
- Direct relation references exist on the resource type
- No circular through-relation chains exist

Returns a `SchemaValidationError` with a list of all issues found.

## Permission Checking

### Single Check

```go
result, err := authorizer.Check(ctx, authorization.CheckRequest{
    Subject:    authorization.Subject{Type: "user", ID: userID},
    Permission: "edit",
    Resource:   authorization.Resource{Type: "project", ID: projectID},
})
// result.Allowed, result.Reason
```

**Evaluation order** (fail-closed — no rule means no access):

1. **Platform admin bypass** — checks if the subject has an `admin` relation on `platform:main`. If so, all permissions are granted.
2. **Self-access** — if the subject type is `user` or `service_account` and the resource ID matches the subject ID, `read`, `update`, and `delete` are granted automatically.
3. **Schema rules** — evaluates the permission's `AnyOf` checks:
   - **Direct:** queries the store for the relation, with group membership expansion
   - **Through:** traverses the relation to find target resources, then recursively checks the specified permission on each target

The `Reason` field in the result describes which rule granted access (e.g., `"direct:owner"`, `"through:org->direct:admin"`, `"platform:admin"`, `"self:user"`).

### Batch Check

```go
results, err := authorizer.CheckBatch(ctx, []authorization.CheckRequest{...})
```

Optimized for the common case: all requests share the same subject, permission, and resource type with no through-relations. In this case, the authorizer uses a single batch query (`CheckBatchDirect`) instead of N individual checks. Falls back to sequential checking for heterogeneous requests.

### Filter Authorized

```go
authorizedIDs, err := authorizer.FilterAuthorized(ctx, subject, "read", "post", postIDs)
```

Takes a list of resource IDs and returns only the ones the subject has permission to access. Uses `CheckBatch` internally.

## Resource Lookup

```go
result, err := authorizer.LookupResources(ctx, subject, "read", "post")
// result.Unrestricted — true if platform admin (skip ID filtering)
// result.IDs          — list of authorized resource IDs
```

Returns all resource IDs of a given type that the subject can access. This powers the **prefilter pattern** for list endpoints:

```go
authorized, _ := authorizer.LookupResources(ctx, subject, "read", "post")
if authorized.Unrestricted {
    // Platform admin — no filter needed
    posts, _ = repo.List(ctx, filter, orderBy, page)
} else {
    // Filter by authorized IDs
    filter.IDs = authorized.IDs
    posts, _ = repo.List(ctx, filter, orderBy, page)
}
```

**Contract:** when `Unrestricted` is false, `IDs` is always non-nil (may be an empty slice, meaning no access).

### How Traversal Works

`LookupResources` evaluates each permission rule in AnyOf:

- **Direct relations** — queries the store for all resources where the subject has the relation.
- **Cross-type Through** — recursively evaluates `LookupResources` on the target type, then finds resources pointing to those targets. Example: `project.read = Through("org", "read")` → find all orgs the user can read → find all projects whose `org` relation points to those orgs.
- **Self-referential Through** — when a Through relation targets the same resource type (e.g., `space.read = Through("parent", "read")` where `parent` points to `space`), the authorizer detects this and uses a **recursive CTE** in the database instead of Go recursion. This efficiently walks arbitrarily deep parent/child hierarchies in a single query.

#### Self-Referential Through (Parent/Child Hierarchies)

Resources with parent/child relationships (spaces, folders, org hierarchies) use a self-referential Through to inherit permissions from ancestors:

```go
{Name: "space", Def: authorization.ResourceTypeDef{
    Relations: map[string]authorization.RelationDef{
        "parent": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "space"}}},
        "owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
        "viewer": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
    },
    Permissions: map[string]authorization.PermissionRule{
        "read": authorization.AnyOf(
            authorization.Direct("owner"),
            authorization.Direct("viewer"),
            authorization.Through("parent", "read"),  // inherit from parent space
        ),
    },
}}
```

When `LookupResources` encounters `Through("parent", "read")` and sees that `parent` points to `space` (same type), it:

1. Calls `lookupDirectOnly` to find spaces where the user has direct relations (owner, viewer) — these are the **root IDs**.
2. Calls `store.LookupDescendantResourceIDs` with those roots — a recursive CTE that walks the `parent` relation to find all descendant spaces in one query.
3. Adds the descendants to the result set (the roots are already included from the direct-relation checks).

This handles trees of any depth without Go recursion. The recursive CTE is:

```sql
WITH RECURSIVE descendants AS (
    SELECT resource_id FROM rebac_relationships
    WHERE resource_type = @type AND relation = @relation
      AND subject_type = @subject_type AND subject_id = ANY(@root_ids)
    UNION
    SELECT r.resource_id FROM rebac_relationships r
    INNER JOIN descendants d ON r.subject_id = d.resource_id
    WHERE r.resource_type = @type AND r.relation = @relation
      AND r.subject_type = @subject_type
)
SELECT DISTINCT resource_id FROM descendants
```

#### Cycle Detection

`LookupResources` tracks visited `(resourceType, permission)` pairs to prevent infinite recursion in cross-type Through chains (e.g., A → Through B → Through A). If a pair is encountered again during recursion, it returns an empty result for that branch. Self-referential Through is handled by the CTE, not by recursion, so it bypasses this tracking entirely.

#### Mixed Through Scenarios

A single permission rule can combine self-referential and cross-type Through:

```go
"read": authorization.AnyOf(
    authorization.Direct("viewer"),
    authorization.Through("parent", "read"),   // self-ref → CTE
    authorization.Through("tenant", "read"),   // cross-type → recursive lookup
)
```

All three paths are evaluated and their results unioned. A user who is a tenant member AND has viewer on a parent space sees dashboards from both sets of spaces.

## Relationship Management

### Create

```go
err := authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{
    {
        ResourceType: "org",
        ResourceID:   orgID,
        Relation:     "member",
        SubjectType:  "user",
        SubjectID:    userID,
    },
})
```

Validates all relationships against the schema before persisting. Returns `ErrInvalidRelation` if a relation or subject type is not allowed.

### Delete

```go
// All relationships for a resource
err := authorizer.DeleteResourceRelationships(ctx, "org", orgID)

// Specific tuple
err := authorizer.DeleteRelationship(ctx, "org", orgID, "member", "user", userID)

// All relations between a resource and subject
err := authorizer.DeleteByResourceAndSubject(ctx, "org", orgID, "user", userID)
```

## Membership Operations

Two higher-level operations for managing resource membership with safety guards:

### Remove Member

```go
err := authorizer.RemoveMember(ctx, "org", orgID, "user", userID)
```

Deletes all relations between the subject and resource. If the subject is the last `owner`, returns `ErrCannotRemoveLastOwner` to prevent orphaning the resource.

### Change Member Role

```go
err := authorizer.ChangeMemberRole(ctx, "org", orgID, "user", userID, "member", "admin", actorID)
```

Deletes the old relation and creates the new one. Guards:

- **Self-role-change** — if `subjectID == actorID`, returns `ErrCannotChangeOwnRole`
- **Last-owner protection** — if changing away from `owner` and there is only one owner, returns `ErrCannotChangeLastOwner`

## Querying Relationships

```go
// Who has a specific relation to a resource?
targets, err := authorizer.GetRelationTargets(ctx, "org", orgID, "admin")

// What resources does a subject access?
relationships, pagination, err := authorizer.ListRelationshipsBySubject(ctx, "user", userID,
    authorization.SubjectRelationshipFilter{ResourceType: ptr("org")},
    orderBy, page,
)

// Who accesses a resource?
relationships, pagination, err := authorizer.ListRelationshipsByResource(ctx, "org", orgID,
    authorization.ResourceRelationshipFilter{Relation: ptr("member")},
    orderBy, page,
)

// Count (used internally for last-owner checks)
count, err := authorizer.CountByResourceAndRelation(ctx, "org", orgID, "owner")
```

## Schema Queries

```go
// Get the full schema
schema := authorizer.GetSchema()

// What permissions does a relation grant? (useful for API responses)
perms := authorizer.GetPermissionsForRelation("org", "admin")
// → ["edit", "read"]
```

## Cache Store

The `CacheStore` wraps any `Storer` with cache-aside reads and write-through invalidation:

```go
cachedStore := authorization.NewCacheStore(innerStore, cacheInstance,
    authorization.WithCacheTTL(30 * time.Second),
    authorization.WithCacheKeyPrefix("authz"),
)
```

### Cached Operations

**Permission checks:** `CheckRelationWithGroupExpansion`, `CheckRelationExists`, `CheckBatchDirect` (per-resource, batches misses), `GetRelationTargets`

**Resource lookups:** `LookupResourceIDs` (per-user), `LookupResourceIDsByRelationTarget` (structural, shared across users), `LookupDescendantResourceIDs` (structural, shared across users)

### Cache Key Design

| Method | Key shape | Scope |
|--------|-----------|-------|
| `LookupResourceIDs` | `authz:lookup:ids:{type}:{relations}:{subjectType}:{subjectID}` | Per-user — different users have different results |
| `LookupResourceIDsByRelationTarget` | `authz:lookup:target:{type}:{relation}:{targetType}:{hash}` | Structural — the graph shape, shared across all users |
| `LookupDescendantResourceIDs` | `authz:lookup:descendants:{type}:{relation}:{subjectType}:{hash}` | Structural — the tree shape, shared across all users |

Structural caches are particularly valuable: the parent/child hierarchy for spaces rarely changes, so the CTE result is cached and shared across all users who access the same part of the tree.

### Invalidation Strategy

Writes (create/delete relationships) trigger **two-axis targeted invalidation**:

1. **Per-user** — all `LookupResourceIDs` caches for the affected subject are cleared. Any role change can ripple through Through chains (e.g., losing space viewer affects dashboard visibility), so all resource types for this user are invalidated. Other users' caches are untouched.
2. **Structural** — target and descendant caches for the affected resource type are cleared. If the graph shape changed for spaces, space-level structural caches are stale, but dashboard or tenant structural caches remain valid.

This design avoids a global cache nuke on every write while still ensuring correctness through cache-miss propagation: if a user's space cache misses (invalidated), `LookupResources` re-evaluates space access, feeds the fresh result into the (still-valid) structural dashboard cache, and returns correct results.

If the cache is nil, the store passes through to the inner implementation with no caching.

## Explain

For debugging permission decisions:

```go
result, err := authorizer.CheckExplain(ctx, authorization.CheckRequest{
    Subject:    authorization.Subject{Type: "user", ID: userID},
    Permission: "edit",
    Resource:   authorization.Resource{Type: "project", ID: projectID},
})

fmt.Println(result.FormatExplain())
```

Output shows each step in the traversal with its result:

```
Permission Check: project:P1#edit@user:U1
Result: true (reason: through:org->direct:admin)

Traversal Path:
1. ✗ [platform_admin] checking platform:main#admin@user:U1
2. > [self_access] checking self-access for edit on project:P1
3. > [through_relation] traversing project:P1#org → found 1 targets
4. ✓ [direct_relation] checking org:O1#admin@user:U1
```

## Satisfier

The `AuthorizationStoreSatisfier` adapts the generated `rebacrelationships` repository to satisfy `authorization.Storer`:

```go
store := satisfiers.NewAuthorizationStoreSatisfier(rebacRepos.RebacRelationship)
```

This keeps the authorization engine decoupled from the generated repository layer. The satisfier handles ID generation and type mapping between authorization's domain types and the generated entity types.

## Known Limitations

### LookupResources: Sibling Through Rules Targeting the Same Type

When a permission has two Through rules that both point to the **same target resource type**, only the first Through path contributes results. The second Through is blocked by the cycle-detection visited set.

**Example:**

```go
// project.read has two Through rules, BOTH targeting "org"
"read": authorization.AnyOf(
    authorization.Through("dept", "read"),   // dept → org
    authorization.Through("team", "read"),   // team → org
)
```

If a project's `dept` is org O1 and its `team` is org O2, and the user has access to both O1 and O2:

- Through("dept", "read") evaluates `org:read`, finds O1 and O2, returns projects in O1. **Marks `org:read` as visited.**
- Through("team", "read") tries to evaluate `org:read`, sees it's already visited, returns empty. **Projects reachable only via O2's team relation are lost.**

**Why it exists:** The visited set prevents infinite recursion in cross-type cycles (A → Through B → Through A). Removing it would break cycle detection.

**Workaround:** Merge the two relations into one. Instead of separate `dept` and `team` relations both pointing to `org`, use a single relation:

```go
"org": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "org"}}},
// Permission: Through("org", "read") — one Through, no conflict
```

If the two relations carry different semantic meaning, create separate resource types instead of reusing the same target type.

**This does NOT affect:**
- Self-referential Through (e.g., `space.read → Through("parent", "read")`) — handled by the CTE path, not the visited set
- Multiple Through rules targeting **different** types (e.g., Through to `space` + Through to `tenant`) — each type is visited independently
- Single-resource `Check()` — uses a different visited tracking scheme (per resource ID, not per type)

**Future fix:** Batch all Through rules targeting the same type into a single evaluation, eliminating the re-entry issue without removing cycle protection.

### Schema Validator: Self-Referential Through Flagged as Circular

The schema validator (`ValidateSchema`) currently flags self-referential Through relations (e.g., `space.read = Through("parent", "read")` where parent points to space) as circular references. At runtime, `LookupResources` handles this correctly via the CTE path, and `Check` handles it via depth limits. The validator warning is a false positive for intentional parent/child hierarchies.

## Errors

All errors wrap base types from `sdk/errs`:

| Error | Base | When |
|---|---|---|
| `ErrPermissionDenied` | `errs.ErrForbidden` | Subject lacks permission |
| `ErrCannotRemoveLastOwner` | `errs.ErrConflict` | Would orphan resource |
| `ErrCannotChangeLastOwner` | `errs.ErrConflict` | Would orphan resource |
| `ErrCannotChangeOwnRole` | `errs.ErrConflict` | Subject tried to change own role |
| `ErrInvalidRelation` | `errs.ErrInvalidInput` | Relation not allowed by schema |
| `ErrInvalidSchema` | `errs.ErrInvalidInput` | Structural schema error |

See also: [Bridge Middleware](../../bridge/middleware.md) for HTTP-level authorization middleware.
