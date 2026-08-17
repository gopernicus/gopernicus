---
title: React and TanStack clients
description: Use a Gopernicus host as an API for a React application.
---

# React and TanStack clients

Gopernicus does not require the Go process to render the user interface. A host can expose JSON and OpenAPI endpoints while a separate React application owns pages, browser state, routing, and assets.

The repository does not currently contain a `ui/react` Go module. This page describes the integration boundary for React clients today and the likely responsibility of a future sibling UI module: reusable client-side components and conventions, not feature schemas or Go routes.

## The boundary

```text
React application
  ├── TanStack Router       browser routes and route data
  ├── TanStack Query        server state, caching, mutations
  └── components            client-side presentation
            │
            │ JSON / HTTP (and optionally OpenAPI)
            ▼
Gopernicus host
  ├── sdk/foundation/web    router, middleware, responses
  ├── features/*            domain services and feature routes
  ├── integrations/*        database, identity, mail, storage, tracing
  └── host code              composition, lifecycle, app-local routes
```

The Go host still owns authentication policy, authorization checks, persistence, migrations, background runtimes, and response contracts. The React application owns the browser experience and should not reach through those boundaries to a datastore.

## API-only host shape

Leave feature view configuration empty when the host only needs an API. Register JSON routes with the SDK web foundation and document the public surface explicitly:

```go
router := web.NewWebHandler(web.WithLogging(log))
router.Use(web.RequestID(), web.Logger(log), web.Panics(log))

mount := feature.Mount{Router: router, Logger: log}

// Construct the feature service and its repositories in the host.
// Register feature routes on mount, then add app-local API routes.
router.GET("/api/projects", listProjects)
router.POST("/api/projects", createProject)

router.ServeOpenAPI(
    "/openapi.json",
    web.OpenAPIInfo{Title: "Projects API", Version: "1.0.0"},
    routeSpecs,
)
```

The exact feature construction depends on the feature and store modules selected by the host. The important part is that no GOTH or other UI dependency enters the API host's module graph just because a feature has an optional view adapter.

## A small client wrapper

Keep transport details in one client module. This makes credentials, error decoding, and base URLs consistent across TanStack Query functions:

```ts
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${import.meta.env.VITE_API_ORIGIN}${path}`, {
    ...init,
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(init?.body ? {'Content-Type': 'application/json'} : {}),
      ...init?.headers,
    },
  });

  if (!response.ok) {
    throw await response.json().catch(() => ({message: response.statusText}));
  }

  return response.json() as Promise<T>;
}
```

Use `credentials: 'include'` when the host uses a cookie-backed session. If the application uses an access token, add its `Authorization` header in this wrapper instead. Cross-origin deployments must configure the host's CORS and CSRF policy deliberately.

## TanStack Query

Query keys should describe the API resource and its scope. Mutations invalidate the queries that can observe the changed resource:

```tsx
import {queryOptions, useMutation, useQuery, useQueryClient} from '@tanstack/react-query';

const projects = queryOptions({
  queryKey: ['projects'],
  queryFn: () => api<Project[]>('/api/projects'),
});

export function ProjectList() {
  const queryClient = useQueryClient();
  const list = useQuery(projects);
  const create = useMutation({
    mutationFn: (input: CreateProject) =>
      api<Project>('/api/projects', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    onSuccess: () => queryClient.invalidateQueries({queryKey: ['projects']}),
  });

  if (list.isPending) return <p>Loading…</p>;
  if (list.isError) return <p role="alert">Could not load projects.</p>;

  return <ProjectTable projects={list.data} onCreate={create.mutate} />;
}
```

The API remains the source of truth for authorization and validation. Client-side validation improves feedback but does not replace the Go service's checks.

## TanStack Router

TanStack Router can own browser navigation independently of the Go router. Keep the two route trees related by resource and URL, not by sharing implementation:

```tsx
const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId',
  loader: ({context, params}) =>
    context.queryClient.ensureQueryData(projectQuery(params.projectId)),
  component: ProjectPage,
});
```

The Go host can still expose `/api/projects/{id}` while the browser route is `/projects/{projectId}`. This keeps API URLs stable if the React application later changes its navigation or deployment path.

## OpenAPI and generated clients

`sdk/foundation/web` can emit an explicit OpenAPI document from route specifications. A React project may use that document to generate TypeScript types or a typed client when the API contract is ready for it. Client generation is planned tooling rather than a required part of the current Workshop flow; handwritten clients like the example above are valid in the meantime.

## Shared UI work

A future `ui/react` module can hold client-side primitives, tokens, and patterns shared by multiple React applications. It should remain independent of feature persistence and Go route registration. Feature-specific API hooks and screens belong in the consuming React application or in a separate client package that depends on the API contract.

See [Web foundation](../sdk/web.md), [Feature modules](../features/overview.md), and [Compose a host](../guides/compose-host.md) for the Go side of this boundary.
