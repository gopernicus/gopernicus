# Adding a Use Case

End-to-end guide for adding business logic that goes beyond simple CRUD -- from scaffolding to wiring HTTP routes.

Use cases (cases) live in the core layer and orchestrate repositories, authorization, and events. Each case has a companion HTTP bridge in the bridge layer. This walkthrough uses a fictional `projectadmin` case as a running example.

---

## Prerequisites

- Entity repositories already generated (see [Adding a New Entity](adding-new-entity.md))
- Familiarity with the hexagonal architecture: `core/cases/` for business logic, `bridge/cases/` for HTTP transport

---

## Step 1: Scaffold the Case

```sh
gopernicus new case projectadmin
```

This creates six files across two directories:

**Core layer** (`core/cases/projectadmin/`):
- `case.go` -- Case struct, constructor, dependency interfaces, operations
- `errors.go` -- domain error definitions
- `events.go` -- domain event types

**Bridge layer** (`bridge/cases/projectadminbridge/`):
- `bridge.go` -- Bridge struct wrapping the Case
- `http.go` -- route registration (`AddHttpRoutes`)
- `model.go` -- request/response types with validation

All files are yours to edit. The generator does not overwrite case files.

---

## Step 2: Define Dependency Interfaces

Open `core/cases/projectadmin/case.go`. Define interfaces for every dependency your case needs. Follow the Go idiom: accept interfaces, return structs.

```go
package projectadmin

import (
    "context"
    "log/slog"

    "github.com/gopernicus/gopernicus/infrastructure/events"
)

// ProjectRepository defines the data access contract this case requires.
type ProjectRepository interface {
    Get(ctx context.Context, id string) (projects.Project, error)
    Create(ctx context.Context, input projects.CreateProject) (projects.Project, error)
    Update(ctx context.Context, id string, input projects.UpdateProject) (projects.Project, error)
}

// MemberRepository defines access to project membership data.
type MemberRepository interface {
    ListByProject(ctx context.Context, projectID string) ([]members.Member, error)
}
```

Interfaces belong at the top of `case.go`, before the Case struct. Keep them minimal -- only the methods this case actually calls.

---

## Step 3: Implement the Case Struct and Operations

Add your dependencies to the `Case` struct and constructor:

```go
type Case struct {
    log      *slog.Logger
    bus      events.Bus
    projects ProjectRepository
    members  MemberRepository
}

func New(
    log *slog.Logger,
    bus events.Bus,
    projects ProjectRepository,
    members MemberRepository,
) *Case {
    return &Case{
        log:      log,
        bus:      bus,
        projects: projects,
        members:  members,
    }
}
```

Define input/result types and operations as methods on `*Case`. Each operation should have its own input and result types:

```go
type ArchiveProjectInput struct {
    ProjectID string
    Reason    string
}

type ArchiveProjectResult struct {
    Project projects.Project
}

func (c *Case) ArchiveProject(ctx context.Context, input ArchiveProjectInput) (ArchiveProjectResult, error) {
    project, err := c.projects.Get(ctx, input.ProjectID)
    if err != nil {
        return ArchiveProjectResult{}, err
    }

    // Business rule: notify all members before archiving
    memberList, err := c.members.ListByProject(ctx, input.ProjectID)
    if err != nil {
        return ArchiveProjectResult{}, err
    }

    // Perform the archive
    updated, err := c.projects.Update(ctx, input.ProjectID, projects.UpdateProject{
        RecordState: ptr("archived"),
    })
    if err != nil {
        return ArchiveProjectResult{}, err
    }

    // Emit domain event
    _ = c.bus.Emit(ctx, ProjectArchivedEvent{
        BaseEvent: events.NewBaseEvent("projectadmin.project_archived"),
        ProjectID: input.ProjectID,
        Members:   len(memberList),
    })

    return ArchiveProjectResult{Project: updated}, nil
}
```

---

## Step 4: Define Domain Errors

Open `core/cases/projectadmin/errors.go`. Wrap sentinel errors from `sdk/errs` so the bridge layer maps them to correct HTTP status codes:

```go
package projectadmin

import (
    "fmt"

    "github.com/gopernicus/gopernicus/sdk/errs"
)

var (
    ErrProjectNotFound = fmt.Errorf("project: %w", errs.ErrNotFound)
    ErrNotAuthorized   = fmt.Errorf("project admin: %w", errs.ErrForbidden)
    ErrAlreadyArchived = fmt.Errorf("project already archived: %w", errs.ErrConflict)
)
```

---

## Step 5: Define Domain Events (if needed)

Open `core/cases/projectadmin/events.go`. Define events by embedding `events.BaseEvent`:

```go
package projectadmin

import "github.com/gopernicus/gopernicus/infrastructure/events"

type ProjectArchivedEvent struct {
    events.BaseEvent
    ProjectID string `json:"project_id"`
    Members   int    `json:"members"`
}
```

Event types follow the convention `<domain>.<action>` (e.g., `projectadmin.project_archived`). Use `events.NewBaseEvent("projectadmin.project_archived")` to set the type string.

For durable processing, chain `.ToOutbox()` on the BaseEvent to persist to the event outbox table. Chain `.WithTenant(tenantID)` for tenant-scoped routing.

---

## Step 6: Add HTTP Routes in Bridge http.go

Open `bridge/cases/projectadminbridge/http.go`. Register routes under the case's kebab-case path:

```go
package projectadminbridge

import (
    "github.com/gopernicus/gopernicus/bridge/protocol/httpmid"
    "github.com/gopernicus/gopernicus/sdk/web"
)

func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
    g := group.Group("/project-admin")

    g.POST("/archive", b.httpArchiveProject,
        httpmid.MaxBodySize(httpmid.DefaultBodySize),
        httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
        httpmid.RateLimit(b.rateLimiter, b.log),
    )
}
```

Implement the handler method on Bridge. It decodes the request, calls the case, and encodes the response:

```go
func (b *Bridge) httpArchiveProject(ctx web.Context) error {
    var req ArchiveProjectRequest
    if err := ctx.DecodeJSON(&req); err != nil {
        return err
    }

    result, err := b.useCase.ArchiveProject(ctx.Request().Context(), projectadmin.ArchiveProjectInput{
        ProjectID: req.ProjectID,
        Reason:    req.Reason,
    })
    if err != nil {
        return err
    }

    return ctx.JSON(http.StatusOK, ArchiveProjectResponse{
        ProjectID: result.Project.ProjectID,
    })
}
```

---

## Step 7: Define Request/Response Models

Open `bridge/cases/projectadminbridge/model.go`. Define request types with `Validate()` methods:

```go
package projectadminbridge

import "github.com/gopernicus/gopernicus/sdk/validation"

type ArchiveProjectRequest struct {
    ProjectID string `json:"project_id"`
    Reason    string `json:"reason"`
}

func (r *ArchiveProjectRequest) Validate() error {
    var errs validation.Errors
    errs.Add(validation.Required("project_id", r.ProjectID))
    errs.Add(validation.Required("reason", r.Reason))
    return errs.Err()
}

type ArchiveProjectResponse struct {
    ProjectID string `json:"project_id"`
}
```

---

## Step 8: Wire the Case and Bridge in server.go

In your server configuration, construct the case and bridge, then mount routes on the `/cases` group:

```go
// Construct the case
projectAdminCase := projectadmin.New(
    log,
    eventBus,
    projectRepo,   // satisfies projectadmin.ProjectRepository
    memberRepo,    // satisfies projectadmin.MemberRepository
)

// Construct the bridge
projectAdminBridge := projectadminbridge.New(
    log,
    projectAdminCase,
    rateLimiter,
)

// Mount under /api/v1/cases/project-admin/...
cases := api.Group("/cases")
projectAdminBridge.AddHttpRoutes(cases)
```

Case routes live under `/cases/<kebab-name>/` to avoid conflicts with generated CRUD routes that live directly on the API group (e.g., `/tenants/:tenant_id/projects`).

---

## Step 9: Add Event Subscribers (if needed)

If other parts of the system need to react to your case's events, subscribe in the bridge layer or during server setup:

```go
bus.Subscribe("projectadmin.project_archived", events.TypedHandler(
    func(ctx context.Context, e projectadmin.ProjectArchivedEvent) error {
        log.InfoContext(ctx, "project archived", "project_id", e.ProjectID)
        // Send notifications, update caches, etc.
        return nil
    },
))
```

Use `events.TypedHandler` for type-safe event handling. The handler silently ignores events that do not match the expected type.

---

## Checklist

- [ ] Case scaffolded (`gopernicus new case <name>`)
- [ ] Dependency interfaces defined in `case.go`
- [ ] Case struct and constructor implemented
- [ ] Operations implemented with input/result types
- [ ] Domain errors defined in `errors.go` wrapping `sdk/errs` sentinels
- [ ] Domain events defined in `events.go` (if applicable)
- [ ] HTTP routes registered in bridge `http.go`
- [ ] Request/response models with validation in bridge `model.go`
- [ ] Case and bridge wired in server.go under `cases := api.Group("/cases")`
- [ ] Event subscribers registered (if applicable)
- [ ] Unit tests written for case operations

---

## Related

- [CLI: new](../cli/new.md)
- [Adding a New Entity](adding-new-entity.md)
- [Adding Authorization to an Entity](adding-auth-to-entity.md)
- [Events](../infrastructure/events.md)
