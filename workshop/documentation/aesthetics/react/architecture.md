# Architecture & Monorepo Structure

## Layer Diagram

The frontend mirrors the backend's hexagonal architecture:

```
┌─────────────────────────────────────────────────────┐
│  Apps (tenant web, mobile native)                   │
│  Entry points — routing, providers, app config      │
├─────────────────────────────────────────────────────┤
│  Bridge (react, react-shared, react-native)         │
│  Platform adapters — UI components, hooks, queries  │
├─────────────────────────────────────────────────────┤
│  Core (cases, repositories, auth)                   │
│  Business logic — API clients, types, domain logic  │
├─────────────────────────────────────────────────────┤
│  SDK (http, errors, logger)                         │
│  Infrastructure — HTTP client, error types          │
└─────────────────────────────────────────────────────┘
```

**Design principle:** Accept interfaces, return structs. Each layer depends only on the layer below it. Platform-specific code never leaks into core or SDK.

---

## Monorepo Layout

```
aesthetics/ts/
├── sdk/                          # Infrastructure (HTTP, errors, logging)
├── core/                         # Business logic (cases, repositories, auth)
├── bridge/
│   ├── react/                    # Web UI — Base UI primitives, Tailwind, components
│   ├── react-shared/             # Platform-agnostic TanStack Query hooks & options
│   └── react-native/             # Mobile UI — RN primitives, theme system
├── apps/
│   ├── tenant/                   # Web app (Vite + TanStack Router)
│   └── mobile/                   # Native app (Expo + Expo Router)
├── package.json                  # Bun workspace root
└── tsconfig.json                 # Shared TypeScript config with path aliases
```

**Package manager:** Bun with workspace protocol (`workspace:*`).

**Workspaces:** `["sdk", "core", "bridge/*", "apps/*"]`

**Path aliases** (from root tsconfig):
- `@<project>/sdk` → `./sdk/index.ts`
- `@<project>/core` → `./core/index.ts`
- `@<project>/react` → `./bridge/react/index.ts`
- `@<project>/react-shared` → `./bridge/react-shared/index.ts`
- `@<project>/react-native` → `./bridge/react-native/index.ts`

---

## Package Dependency Graph

```
sdk (leaf — no internal deps)
 └── core → sdk
      └── react-shared → core, sdk
           ├── react → core, sdk, react-shared
           │    └── tenant app → core, sdk, react, react-shared
           └── react-native (peer deps only)
                └── mobile app → core, sdk, react-shared, react-native
```

`react-shared` is the cross-platform bridge. Both web and mobile apps consume it for all TanStack Query hooks and query options. `react` and `react-native` provide platform-specific UI only.

---

## SDK Layer

**Package:** `sdk/` — Platform-agnostic infrastructure.

### HTTP Client (`sdk/http/`)

Handles all backend communication with built-in auth lifecycle:

- **Cookie-based auth** for web (`credentials: "include"`)
- **Bearer token auth** for mobile (`getHeaders()` callback)
- **Automatic 401 refresh** with request deduplication (one refresh in-flight at a time)
- **Configurable timeout** (30s default)
- **Structured error responses**

```typescript
import { createClient } from "@<project>/sdk/http"

const http = createClient({
  baseUrl: "",
  apiRoot: "/api/v1",
  credentials: "include",
  onRefresh: async () => { ... },
  onAuthFailure: () => { ... },
})
```

### Errors (`sdk/errors/`)

Shared error types used across all layers.

### Logger (`sdk/logger/`)

Structured logging utilities.

---

## Core Layer

**Package:** `core/` — Framework-agnostic business logic. Mirrors backend domain structure.

```
core/
├── auth/                   # Auth API client, JWT parsing, error types
│   ├── api.ts              # createAuthApi(http) — login, register, verify, refresh
│   ├── jwt.ts              # Token parsing
│   ├── errors.ts           # AuthError, isInvalidCredentials(), isEmailNotVerified(), etc.
│   ├── permissions.ts      # Permission checking utilities
│   └── types.ts            # User, Session, MeResponse, LoginRequest, etc.
├── cases/                  # Use case API clients (mirror backend cases)
│   ├── tenant-admin/       # Workspace management (memberships, invitations, directory)
│   ├── question-admin/     # Question management, assignments
│   ├── group-admin/        # Group CRUD
│   └── takes/              # Audio take submissions
└── repositories/           # Data access API clients (mirror backend repos)
    ├── tenancy/            # Tenant lookup
    ├── questions/          # Question/answer CRUD
    └── rebac/              # Group/relationship data
```

Each module exports a factory function: `createXxxApi(http: HTTPClient)` that returns a typed API object. These are consumed by `react-shared` to create hooks and query options.

---

## Bridge Layer

The bridge translates core APIs into platform-specific integrations. Three packages:

### react-shared

**Package:** `bridge/react-shared/` — Platform-agnostic TanStack Query integration.

Both web and mobile apps import from here for all data fetching. Provides:

1. **Query options factories** — Standalone functions that create `queryOptions()` objects for route loaders and components
2. **Hook factories** — Functions that create `useMutation()` hooks with automatic cache invalidation
3. **CRUD hook factory** — Generic factory for standard list/get/create/update/delete patterns

```
react-shared/
├── auth/                   # createAuthHooks(), createSessionQueryOptions()
├── cases/
│   ├── tenant-admin/       # createTenantAdminHooks(), createTenantAdminQueryOptions()
│   ├── question-admin/     # createQuestionAdminHooks(), createQuestionAdminQueryOptions()
│   ├── group-admin/        # createGroupAdminHooks(), createGroupAdminQueryOptions()
│   └── takes/              # createTakesHooks(), createTakesQueryOptions()
├── repositories/
│   ├── tenancy/            # Tenant query hooks
│   ├── questions/          # Question query hooks
│   └── rebac/              # Relationship query hooks
└── crud-hooks.ts           # createCrudHooks() generic factory
```

### react (Web)

**Package:** `bridge/react/` — Web-specific React components, UI system, and router integration.

```
react/
├── auth/
│   ├── components/         # LoginForm, RegisterForm, VerifyEmailForm, etc.
│   └── router-helpers.ts   # createRequireAuth(), createRedirectIfAuthenticated()
├── cases/
│   └── audio-recorder/     # Web audio recording hooks
├── ui/
│   ├── primitives/         # 39 Base UI component wrappers
│   ├── components/         # Composed components (FormField, ListView, Layout, etc.)
│   │   ├── schema/         # Schema-driven form generator
│   │   └── layout/         # App layout (sidebar, header, breadcrumbs)
│   └── styles/             # Tailwind theme, animations, CSS variables
├── contexts/               # Web-specific React contexts
└── hooks/                  # Web-specific React hooks
```

### react-native (Mobile)

**Package:** `bridge/react-native/` — React Native UI components and theme system.

```
react-native/
└── ui/
    ├── primitives/         # Button, Input, Label, Dialog (RN Pressable/TextInput)
    ├── components/         # Card and other composed components
    └── theme/              # Context-based theme (colors, spacing, radius, fonts)
```

---

## Apps

### Tenant (Web App)

**Location:** `apps/tenant/` — Vite + React + TanStack Router + Tailwind CSS 4.

```
tenant/
├── src/
│   ├── main.tsx            # React DOM root, QueryClient, Router setup
│   ├── config.ts           # API initialization, query options, hooks, route guards
│   ├── index.css           # Tailwind + brand theme overrides
│   ├── routes/             # TanStack Router file-based routes
│   │   ├── __root.tsx      # Root route (QueryClient in context)
│   │   ├── _auth/          # Unauthenticated routes (login, register, etc.)
│   │   └── _app/           # Authenticated routes
│   │       └── $tenantSlug/  # Tenant-scoped routes
│   ├── components/         # App-specific components
│   ├── stores/             # Zustand state stores
│   ├── hooks/              # App-specific hooks
│   └── lib/                # Utilities
├── vite.config.ts
└── tsconfig.json
```

### Mobile (Native App)

**Location:** `apps/mobile/` — Expo 54 + React Native 0.81 + Expo Router 6.

```
mobile/
├── app/                    # Expo Router file-based routes
│   ├── _layout.tsx         # Root layout (providers, fonts, QueryClient)
│   ├── index.tsx           # Auth check → redirect
│   ├── (auth)/             # Login, register, verify, forgot/reset password
│   └── (main)/
│       ├── _layout.tsx     # Auth guard
│       └── [tenantSlug]/
│           └── (tabs)/     # Bottom tab navigator
├── src/
│   ├── config.ts           # API init with Bearer tokens + SecureStore
│   ├── theme.ts            # Font and color definitions
│   ├── providers/          # AuthProvider, TenantProvider
│   └── components/         # App-specific components
├── app.json                # Expo config
└── assets/
```
