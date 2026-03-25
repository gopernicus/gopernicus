# Authorization (ReBAC) Deep Dive

The `authorization` package (`core/auth/authorization/`) implements Relationship-Based Access Control (ReBAC). Permissions are not assigned directly to users. Instead, users hold relations on resources, and permissions are computed from those relations at check time.

## Core Concepts

### Subjects

A `Subject` represents who is requesting access:

```go
type Subject struct {
    Type     string // "user", "service_account"
    ID       string
    Relation string // optional, for "group#member" style references
}
```

### Resources

A `Resource` represents what is being accessed:

```go
type Resource struct {
    Type string // "post", "tenant", "project"
    ID   string
}
```

### Relations

Relations are the edges in the authorization graph. A relationship tuple is: `resource_type:resource_id#relation@subject_type:subject_id`.

Example: `tenant:abc#owner@user:123` means "user 123 is an owner of tenant abc".

Relations are defined per resource type. Each relation specifies which subject types can hold it:

```go
Relations: map[string]RelationDef{
    "owner":  {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
    "member": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}, {Type: "group", Relation: "member"}}},
    "tenant": {AllowedSubjects: []SubjectTypeRef{{Type: "tenant"}}},
}
```

The `group#member` notation means that any member of a group holding this relation also implicitly holds it (group expansion).

### Permissions

Permissions are computed from relations using rules. A permission is granted if any of its checks pass (OR/union semantics):

```go
Permissions: map[string]PermissionRule{
    "read":   AnyOf(Direct("owner"), Direct("member"), Through("tenant", "read")),
    "update": AnyOf(Direct("owner"), Through("tenant", "manage")),
    "delete": AnyOf(Direct("owner"), Through("tenant", "manage")),
}
```

**Direct checks** -- `Direct("owner")` grants the permission if the subject holds the `owner` relation on the resource.

**Through checks** -- `Through("tenant", "manage")` traverses the `tenant` relation on the resource to find related tenant(s), then checks if the subject has the `manage` permission on those tenants. This enables hierarchical authorization (e.g., a tenant admin can manage all projects in the tenant).

## The Authorizer Struct

`Authorizer` is constructed with a store, schema, and configuration:

```go
schema := authorization.NewSchema(
    domainA.AuthSchema(),
    domainB.AuthSchema(),
)
authorizer := authorization.NewAuthorizer(store, schema, cfg,
    authorization.WithLogger(log),
)
```

### Configuration

| Field | Env Var | Default |
|---|---|---|
| `MaxTraversalDepth` | `AUTHORIZATION_TRAVERSAL_MAX_DEPTH` | 10 |

`MaxTraversalDepth` limits how many through-relation hops the engine will follow before denying. This prevents infinite loops in misconfigured schemas.

## Permission Checks

### Check

`Check(ctx, CheckRequest)` evaluates a single permission check:

```go
result, err := authorizer.Check(ctx, authorization.CheckRequest{
    Subject:    authorization.Subject{Type: "user", ID: userID},
    Permission: "read",
    Resource:   authorization.Resource{Type: "project", ID: projectID},
})
if result.Allowed {
    // access granted; result.Reason explains why (e.g., "direct:owner", "through:tenant->direct:admin")
}
```

Evaluation order:
1. **Platform admin bypass** -- if `platform:main#admin@user:X` exists, access is always granted
2. **Self-access** -- users can always read, update, and delete their own user/service_account record
3. **Schema rules** -- evaluates direct relations and through-relation traversals with cycle detection

### CheckBatch

`CheckBatch(ctx, []CheckRequest)` checks multiple permissions efficiently. When all requests share the same subject, permission, and resource type with no through-relations, an optimized batch query is used. Otherwise it falls back to sequential checks.

### FilterAuthorized

`FilterAuthorized(ctx, subject, permission, resourceType, resourceIDs)` returns only the resource IDs the subject can access. Uses `CheckBatch` internally.

### LookupResources

`LookupResources(ctx, subject, permission, resourceType)` returns all resource IDs of a given type that the subject can access. Returns a `LookupResult`:

```go
result, err := authorizer.LookupResources(ctx, subject, "read", "project")
if result.Unrestricted {
    // platform admin -- skip ID filtering entirely
} else {
    // result.IDs contains the authorized resource IDs (non-nil, may be empty)
}
```

This powers the prefilter pattern: look up authorized IDs first, then pass them to the repository as `WHERE id = ANY(@authorized_ids)`.

## Relationship Management

### Creating Relationships

```go
err := authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{
    {ResourceType: "tenant", ResourceID: tenantID, Relation: "owner", SubjectType: "user", SubjectID: userID},
    {ResourceType: "tenant", ResourceID: tenantID, Relation: "member", SubjectType: "user", SubjectID: userID},
})
```

All relationships are validated against the schema before persisting. If a relationship references an unknown resource type, relation, or disallowed subject type, `ErrInvalidRelation` is returned.

### Deleting Relationships

- `DeleteRelationship(ctx, resourceType, resourceID, relation, subjectType, subjectID)` -- removes a specific tuple
- `DeleteResourceRelationships(ctx, resourceType, resourceID)` -- removes all relationships for a resource
- `DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)` -- removes all relations a subject holds on a resource

### Membership Operations

`RemoveMember(ctx, resourceType, resourceID, subjectType, subjectID)` removes a subject with last-owner protection. If the subject is the only owner, `ErrCannotRemoveLastOwner` is returned.

`ChangeMemberRole(ctx, resourceType, resourceID, subjectType, subjectID, oldRelation, newRelation, actorID)` changes a subject's relation with guards against self-role-change and last-owner orphaning.

## Relationship Queries

- `GetRelationTargets(ctx, resourceType, resourceID, relation)` -- all subjects with a specific relation to a resource
- `ListRelationshipsBySubject(ctx, subjectType, subjectID, filter, orderBy, page)` -- all resources a subject has relationships with (paginated, filterable by resource type and relation)
- `ListRelationshipsByResource(ctx, resourceType, resourceID, filter, orderBy, page)` -- all subjects with relationships to a resource (paginated, filterable by subject type and relation)
- `CountByResourceAndRelation(ctx, resourceType, resourceID, relation)` -- count of relationships (used for last-owner checks)

## Schema Queries

- `GetSchema()` -- returns the full authorization schema
- `GetPermissionsForRelation(resourceType, relation)` -- returns all permissions granted by a relation on a resource type (useful for building permission lists in API responses)

## The Storer Interface

The `Storer` interface defines the storage contract. The framework provides `AuthorizationStoreSatisfier` which wraps the generated `rebac_relationships` repository. The interface includes:

- Permission check methods: `CheckRelationWithGroupExpansion`, `GetRelationTargets`, `CheckRelationExists`, `CheckBatchDirect`
- CRUD methods: `CreateRelationships`, `DeleteResourceRelationships`, `DeleteRelationship`, `DeleteByResourceAndSubject`
- Listing methods: `ListRelationshipsBySubject`, `ListRelationshipsByResource`
- Lookup methods: `LookupResourceIDs`, `LookupResourceIDsByRelationTarget`

## Schema Composition

Schemas from multiple domains are composed via `NewSchema`:

```go
schema := authorization.NewSchema(
    authRepos.AuthSchema(),    // auth domain (users, api_keys, etc.)
    rebacRepos.AuthSchema(),   // rebac domain (invitations, etc.)
)
```

When two domains define the same resource type, their definitions are merged. Relations and permissions from the later schema add to or replace those from the earlier one. Use `Remove()` to explicitly delete a permission during merge.

## Explain

The `Explain(ctx, CheckRequest)` method returns a detailed `ExplainResult` with step-by-step traversal information, useful for debugging authorization decisions. Each step records the type (platform admin, self-access, direct relation, through relation, group expansion), the result (granted, denied, skipped, continued), and a human-readable message.

## Integration with Middleware

The Authorizer integrates with HTTP middleware via the `PermissionChecker` interface defined in `httpmid`:

```go
type PermissionChecker interface {
    Check(ctx context.Context, req authorization.CheckRequest) (authorization.CheckResult, error)
}
```

The `authorization.Authorizer` satisfies this interface structurally. See the [Auth Middleware](middleware.md) documentation for details on `AuthorizeParam`, `AuthorizeType`, and `RequirePlatformAdmin`.

## Related

- [Auth Architecture Overview](overview.md)
- [Auth Schema Definition](schema-definition.md)
- [Authentication](authentication.md)
- [Auth Middleware](middleware.md)
