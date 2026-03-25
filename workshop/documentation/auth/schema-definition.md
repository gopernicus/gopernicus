# Defining Auth Schemas via bridge.yml

Authorization schemas in Gopernicus are defined declaratively using `bridge.yml`
configuration files in each bridge package directory. The CLI reads this
configuration and generates Go code that produces `authorization.ResourceSchema`
slices for the Authorizer.

## Configuration Syntax

Auth configuration is placed in the `bridge.yml` file of each entity's bridge
package. It defines the relations and permissions for the resource type
represented by that entity.

### auth_relations

Defines relations on the resource type. Each entry declares a relation name
and the subject types that can hold it.

```yaml
auth_relations:
  - owner(user, service_account)
  - member(user, group#member)
  - tenant(tenant)
```

**Format:** `relation_name(subject_type1, subject_type2, ...)`

- `relation_name` -- the name of the relation (e.g., `owner`, `member`, `admin`)
- Subject types -- comma-separated list of allowed subject types
- Group syntax -- `group#member` means members of a group holding this relation also hold it (group expansion)

### auth_permissions

Defines permissions computed from relations. Each entry declares a permission
name and one or more checks separated by `|` (OR semantics).

```yaml
auth_permissions:
  - list(tenant->list)
  - create(tenant->manage)
  - read(owner|tenant->read)
  - update(owner|tenant->manage)
  - delete(owner|tenant->manage)
  - manage(owner|tenant->manage)
```

**Format:** `permission_name(check1|check2|...)`

Each check is either:
- **Direct relation** -- just the relation name, e.g., `owner`. Grants the permission if the subject holds this relation on the resource.
- **Through relation** -- `relation->permission`, e.g., `tenant->manage`. Traverses the named relation to find related resources, then checks if the subject has the specified permission on those resources.

### auth_create (on routes)

Defined on individual routes (typically a Create route) in `bridge.yml`.
Specifies relationships to create automatically when a new record is inserted.

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

**Format:** `resource_type:{column}#relation@subject_type:{column}`

- `{column}` -- replaced with the value of that column from the inserted row
- `{=subject}` -- replaced with the authenticated subject (from the request context)
- `{=invited_by}` -- replaced with the value of the `invited_by` column (used in invitations)

Multiple `auth_create` entries can appear on the same route. Each defines one relationship tuple to create.

## Complete Example

Here is a complete `bridge.yml` file for a project entity that belongs to a tenant:

```yaml
routes:
  list:
    method: GET
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - rate_limit
      - authorize: prefilter(tenant:tenant_id, read)
  get:
    method: GET
    path: /tenants/{tenant_id}/projects/{project_id}
    middleware:
      - authenticate
      - authorize: check(read)
      - with_permissions
  create:
    method: POST
    path: /tenants/{tenant_id}/projects
    middleware:
      - authenticate
      - authorize: check(create)
    auth_create:
      - project:{project_id}#owner@{=subject}
      - project:{project_id}#tenant@tenant:{tenant_id}
  update:
    method: PUT
    path: /tenants/{tenant_id}/projects/{project_id}
    middleware:
      - authenticate
      - authorize: check(update)
  delete:
    method: DELETE
    path: /tenants/{tenant_id}/projects/{project_id}
    middleware:
      - authenticate
      - authorize: check(delete)

auth_relations:
  - tenant(tenant)
  - owner(user, service_account)

auth_permissions:
  - list(tenant->list)
  - create(tenant->manage)
  - read(owner|tenant->read)
  - update(owner|tenant->manage)
  - delete(owner|tenant->manage)
  - manage(owner|tenant->manage)
```

For a top-level entity (no parent):

```yaml
routes:
  list:
    method: GET
    path: /widgets
    middleware:
      - authenticate
      - authorize: prefilter(read)
  create:
    method: POST
    path: /widgets
    middleware:
      - authenticate
    auth_create:
      - widget:{widget_id}#owner@{=subject}

auth_relations:
  - owner(user, service_account)

auth_permissions:
  - list(owner)
  - create(authenticated)
  - read(owner)
  - update(owner)
  - delete(owner)
  - manage(owner)
```

## Generated Files

Running `gopernicus generate` produces auth schema files in the **bridge
composite directory** (not the repo composite):

### generated_authschema.go (always regenerated)

This file is regenerated on every `gopernicus generate` run. It is located in
the bridge composite (e.g., `bridge/repositories/tenancyreposbridge/`). It
contains the `GeneratedAuthSchema()` function that returns
`[]authorization.ResourceSchema` built from `bridge.yml` configuration:

```go
func GeneratedAuthSchema() []authorization.ResourceSchema {
    return []authorization.ResourceSchema{
        {
            Name: "project",
            Def: authorization.ResourceTypeDef{
                Relations: map[string]authorization.RelationDef{
                    "owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}, {Type: "service_account"}}},
                    "tenant": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "tenant"}}},
                },
                Permissions: map[string]authorization.PermissionRule{
                    "read":   authorization.AnyOf(authorization.Direct("owner"), authorization.Through("tenant", "read")),
                    "update": authorization.AnyOf(authorization.Direct("owner"), authorization.Through("tenant", "manage")),
                    // ...
                },
            },
        },
    }
}
```

Never edit this file directly. Changes will be overwritten.

### authschema.go (created once, customizable)

This bootstrap file is created once and never overwritten. By default it delegates to the generated schema:

```go
func AuthSchema() []authorization.ResourceSchema {
    return GeneratedAuthSchema()
}
```

To customize, modify this function. Common customizations:

**Adding a custom relation:**
```go
func AuthSchema() []authorization.ResourceSchema {
    schemas := GeneratedAuthSchema()
    for i, s := range schemas {
        if s.Name == "project" {
            s.Def.Relations["reviewer"] = authorization.RelationDef{
                AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}},
            }
            s.Def.Permissions["review"] = authorization.AnyOf(
                authorization.Direct("reviewer"),
                authorization.Direct("owner"),
            )
            schemas[i] = s
        }
    }
    return schemas
}
```

**Adding an entirely new resource type:**
```go
func AuthSchema() []authorization.ResourceSchema {
    schemas := GeneratedAuthSchema()
    schemas = append(schemas, authorization.ResourceSchema{
        Name: "platform",
        Def: authorization.ResourceTypeDef{
            Relations: map[string]authorization.RelationDef{
                "admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
            },
            Permissions: map[string]authorization.PermissionRule{
                "admin": authorization.AnyOf(authorization.Direct("admin")),
            },
        },
    })
    return schemas
}
```

## Schema Composition Across Domains

Each bridge composite domain has its own `AuthSchema()` function. At the app layer, schemas are composed via `authorization.NewSchema`:

```go
authorizationSchema := authorization.NewSchema(
    authBridges.AuthSchema(),
    rebacBridges.AuthSchema(),
)
```

The `Bridges` struct in each bridge composite exposes `AuthSchema()` which delegates to the customizable function in `authschema.go`.

When two domains define the same resource type, their definitions are merged:
- Relations from the later domain add to or replace those from the earlier domain
- Permissions from the later domain add to or replace those from the earlier domain
- Use `authorization.Remove()` in a permission rule to explicitly delete a permission defined in an earlier domain

## How the Generator Works

The generation pipeline:

1. Parses `bridge.yml` files, extracting `auth_relations` and `auth_permissions` at the package level, and `auth_create` entries at the route level
2. Singularizes the table name to derive the resource type (e.g., `projects` becomes `project`)
3. Parses relation entries into `AuthRelation{Name, Subjects}` structs
4. Parses permission entries into `AuthPermission{Name, Rules}` structs, where rules are either direct (`"owner"`) or through (`"tenant->manage"`)
5. Renders `generated_authschema.go` in the bridge composite directory (always overwritten)
6. Renders `authschema.go` from the bootstrap template (created once, skipped if file exists)

The resource type comes from the table name, not the directory name. The generator uses `Singularize()` to convert plural table names to singular resource types.

## Parsing Details

- `auth_relations` values are parsed by extracting the relation name before `(` and subjects split by `,` inside the parentheses
- `auth_permissions` values are parsed similarly, with rules inside parentheses split by `|`
- `auth_create` tuples are split on `#` (separating resource from relation) and `@` (separating relation from subject), with `{column}` placeholders resolved at runtime from the inserted row

## Related

- [Authorization](authorization.md)
- [Auth Architecture Overview](overview.md)
- [Core Layer](../layers/core.md)
- [Bridge Layer](../layers/bridge.md)
