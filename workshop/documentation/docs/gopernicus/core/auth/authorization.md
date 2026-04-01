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

For through-relations, `LookupResources` traverses the chain: find all orgs where the user is admin, then find all projects that belong to those orgs.

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

**Cached operations:** `CheckRelationWithGroupExpansion`, `CheckRelationExists`, `CheckBatchDirect` (per-resource, batches misses), `GetRelationTargets`

**Invalidation:** writes (create/delete relationships) invalidate all cached checks for affected resources and subjects using pattern-based deletion.

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
