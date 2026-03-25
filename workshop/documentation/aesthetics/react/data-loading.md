# Data Loading & TanStack Integration

## API-First Design

The data layer follows a strict factory pattern. Each domain area has three layers:

```
1. Core API          createTenantAdminApi(http)         → { getMemberships, sendInvitation, ... }
2. Query Options     createTenantAdminQueryOptions(api) → { myMemberships(params), directory(id), ... }
3. Mutation Hooks    createTenantAdminHooks(http)        → { useSendInvitation(id), ... }
```

All three are initialized once in each app's `config.ts` and shared throughout:

```typescript
// apps/<app>/src/config.ts
const http = createClient({ ... })

// Core APIs
export const authApi = createAuthApi(http)
export const tenantAdminApi = createTenantAdminApi(http)

// Query options (for loaders + useSuspenseQuery)
export const sessionQueryOptions = createSessionQueryOptions(authApi)
export const tenantAdminQueryOptions = createTenantAdminQueryOptions(tenantAdminApi)

// Mutation hooks (for forms + actions)
export const tenantAdminHooks = createTenantAdminHooks(http)
```

---

## Query Options Factories

Query options are standalone factories that return `queryOptions()` objects. They are used in both route loaders and components — this is what makes the cache-seeding pattern work.

```typescript
// bridge/react-shared/cases/tenant-admin/index.ts
export function createTenantAdminQueryOptions(api: TenantAdminApi) {
  const keys = {
    memberships: ["tenant-admin", "memberships"] as const,
    directory: (tenantId: string) => ["tenant-admin", "directory", tenantId] as const,
  }

  return {
    myMemberships: (params?: { limit?: number; cursor?: string }) =>
      queryOptions({
        queryKey: [...keys.memberships, params] as const,
        queryFn: () => api.getMyMemberships(params),
      }),

    directory: (tenantId: string) =>
      queryOptions({
        queryKey: keys.directory(tenantId),
        queryFn: () => api.getDirectory(tenantId),
      }),
  }
}
```

**Why factories:** Both web and mobile create their own instances with their own HTTP clients (cookies vs tokens), but share identical query key structures and cache behavior.

---

## Route Loaders & Suspense

Web uses TanStack Router file-based routing with loader-seeded query caches.

### 1. Route loader seeds the cache

```typescript
export const Route = createFileRoute("/_app/$tenantSlug")({
  loader: async ({ params: { tenantSlug }, context: { queryClient } }) => {
    // Critical data — await it (pendingComponent shows while loading)
    const tenant = await queryClient.ensureQueryData(
      tenantsQueryOptions.bySlug(tenantSlug)
    )
    // Secondary data — fire and forget (component handles via Suspense)
    queryClient.prefetchQuery(
      tenantAdminQueryOptions.directory(tenant.record.tenant_id)
    )
    return { tenantId: tenant.record.tenant_id }
  },
  pendingComponent: () => <RouteLoading message="Loading workspace..." fullScreen />,
  errorComponent: () => <RouteError message="Workspace not found" fullScreen />,
})
```

### 2. Component subscribes via useSuspenseQuery

```typescript
function TenantLayout() {
  const { tenantSlug } = Route.useParams()
  // Data guaranteed available — no loading checks needed
  const { data: tenant } = useSuspenseQuery(tenantsQueryOptions.bySlug(tenantSlug))
  // Background refetch happens automatically when stale
}
```

### 3. Route boundaries handle loading/error states

- `pendingComponent` — shown while awaited loader data resolves
- `errorComponent` — shown when loader throws
- No inline loading spinners for route data

### Critical vs Secondary Data

- **`ensureQueryData()`** — blocks route transition, shows `pendingComponent`
- **`prefetchQuery()`** — fire-and-forget, component handles via Suspense boundary

### Search Params with Validation

```typescript
const searchSchema = z.object({
  cursor: z.string().optional(),
  limit: z.number().optional().default(25),
  order: z.string().optional(),
  s: z.string().optional(),
})

export const Route = createFileRoute("/_app/$tenantSlug/questions/")({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ search }),  // drives loader re-execution on param change
  loader: async ({ deps: { search }, ... }) => { ... },
})
```

### Mobile Data Loading

Mobile uses `useQuery` directly — Expo Router does not support route loaders. Components handle loading states themselves:

```typescript
export default function DashboardScreen() {
  const { tenantId } = useTenant()
  const { data, isLoading } = useQuery(
    questionAdminQueryOptions.myAssignments(tenantId)
  )
  if (isLoading) return <ActivityIndicator />
  // ...
}
```

---

## Mutations & Cache Invalidation

Mutations use `useMutation()` with `onSuccess` callbacks that invalidate related queries:

```typescript
export function createTenantAdminHooks(http: HTTPClient) {
  const api = createTenantAdminApi(http)

  function useSendInvitation(tenantId: string) {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: SendInvitationRequest) =>
        api.sendInvitation(tenantId, input),
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: keys.directory(tenantId) })
        queryClient.invalidateQueries({ queryKey: keys.invitations(tenantId) })
      },
    })
  }

  return { useSendInvitation }
}
```

**After mutations:** Always `invalidateQueries` so subscribed components refetch. Never manually update the cache.

---

## CRUD Hooks Factory

For standard entity CRUD, `createCrudHooks()` generates a full set of hooks:

```typescript
// bridge/react-shared/crud-hooks.ts
export function createCrudHooks<T, TCreate, TUpdate>(
  api: CrudApi<T, TCreate, TUpdate>,
  key: string,
) {
  const keys = {
    all: [key] as const,
    lists: (tenantId: string) => [key, "list", tenantId] as const,
    detail: (tenantId: string, id: string) => [key, "detail", tenantId, id] as const,
  }

  return {
    keys,
    useList(tenantId, filter?) { ... },     // queryOptions for list
    useGet(tenantId, id) { ... },           // queryOptions for detail
    useCreate(tenantId) { ... },            // useMutation + invalidate lists
    useUpdate(tenantId) { ... },            // useMutation + invalidate all
    useDelete(tenantId) { ... },            // useMutation + invalidate lists
  }
}

// Usage
export function createQuestionHooks(http: HTTPClient) {
  return createCrudHooks<Question, CreateQuestion, UpdateQuestion>(
    createQuestionsApi(http), "questions"
  )
}
```

---

## Rules

- **Never use `useLoaderData()`** — always `useSuspenseQuery` with the same query options used in the loader
- **Never use `useQuery` with `isLoading` checks for route data** — the loader should provide it
- **Never put loading spinners in components for route data** — use `pendingComponent` on the route
- **Always `invalidateQueries` after mutations** — never manually update cache
- **Query options must be standalone factories** — reusable across loaders and components
