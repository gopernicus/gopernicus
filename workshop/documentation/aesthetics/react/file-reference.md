# File Reference

Quick-lookup table of key files by category. All paths relative to `aesthetics/ts/`.

## Configuration & Setup

| File | Purpose |
|------|---------|
| `package.json` | Bun workspace root, scripts |
| `tsconfig.json` | Path aliases, shared compiler options |
| `apps/tenant/src/config.ts` | Web API init, query options, hooks, route guards |
| `apps/tenant/src/main.tsx` | Router + QueryClient setup |
| `apps/tenant/vite.config.ts` | Vite build config |
| `apps/mobile/src/config.ts` | Mobile API init with Bearer tokens |
| `apps/mobile/app/_layout.tsx` | Root providers, fonts, QueryClient |
| `apps/mobile/app.json` | Expo configuration |

## UI System

| File | Purpose |
|------|---------|
| `bridge/react/ui/primitives/index.ts` | All primitive exports |
| `bridge/react/ui/primitives/button.tsx` | Reference primitive (CVA pattern) |
| `bridge/react/ui/primitives/dialog.tsx` | Dialog primitive (overlay pattern) |
| `bridge/react/ui/primitives/field.tsx` | Field primitive (form integration) |
| `bridge/react/ui/components/index.ts` | All composed component exports |
| `bridge/react/ui/components/form-field.tsx` | FormField component |
| `bridge/react/ui/components/form-status.tsx` | FormStatus display component |
| `bridge/react/ui/components/use-form-status.ts` | Form feedback hook |
| `bridge/react/ui/components/list-view.tsx` | Data table with pagination |
| `bridge/react/ui/components/use-list-state.ts` | List state management hook |
| `bridge/react/ui/components/cursor-pagination.tsx` | Cursor pagination controls |
| `bridge/react/ui/components/schema/schema-form.tsx` | Schema-driven forms |
| `bridge/react/ui/components/layout/` | App layout (sidebar, header, breadcrumbs) |
| `bridge/react/ui/styles/theme.css` | Base theme (CSS variables, OKLCH) |
| `bridge/react/ui/styles/animations.css` | Animation utilities |
| `bridge/react/ui/utils.ts` | `cn()` utility |
| `bridge/react-native/ui/theme/` | Native theme system |
| `bridge/react-native/ui/primitives/` | Native primitive components |

## Data Layer

| File | Purpose |
|------|---------|
| `sdk/http/client.ts` | HTTP client with auth lifecycle |
| `sdk/http/types.ts` | HTTPClient, HTTPResponse, HTTPOptions types |
| `bridge/react-shared/crud-hooks.ts` | Generic CRUD hook factory |
| `bridge/react-shared/auth/index.ts` | Auth query options + hooks |
| `bridge/react-shared/cases/tenant-admin/index.ts` | Tenant admin queries + mutations |
| `bridge/react-shared/cases/question-admin/index.ts` | Question admin queries + mutations |
| `bridge/react-shared/cases/group-admin/index.ts` | Group admin queries + mutations |
| `bridge/react-shared/cases/takes/index.ts` | Takes queries + mutations |
| `bridge/react-shared/repositories/tenancy/index.ts` | Tenant repository hooks |
| `bridge/react-shared/repositories/questions/index.ts` | Question repository hooks |
| `bridge/react-shared/repositories/rebac/index.ts` | Relationship repository hooks |

## Authentication

| File | Purpose |
|------|---------|
| `bridge/react/auth/components/login-form.tsx` | Reference auth form pattern |
| `bridge/react/auth/components/register-form.tsx` | Registration form |
| `bridge/react/auth/components/verify-email-form.tsx` | Email verification form |
| `bridge/react/auth/components/forgot-password-form.tsx` | Password reset request |
| `bridge/react/auth/components/reset-password-form.tsx` | New password form |
| `bridge/react/auth/components/change-password-form.tsx` | Authenticated password change |
| `bridge/react/auth/components/oauth-buttons.tsx` | OAuth provider buttons |
| `bridge/react/auth/router-helpers.ts` | Route guard factories |
| `core/auth/api.ts` | Auth API client |
| `core/auth/errors.ts` | Auth error types and checkers |
| `core/auth/types.ts` | User, Session, MeResponse types |
| `apps/mobile/src/providers/AuthProvider.tsx` | Mobile auth context |

## Routes

| File | Purpose |
|------|---------|
| `apps/tenant/src/routes/__root.tsx` | Root route (QueryClient context) |
| `apps/tenant/src/routes/_app/route.tsx` | Auth guard + prefetch pattern |
| `apps/tenant/src/routes/_auth/route.tsx` | Redirect-if-authenticated guard |
| `apps/tenant/src/routes/_app/$tenantSlug/route.tsx` | Loader with ensureQueryData |
| `apps/tenant/src/routes/_app/$tenantSlug/questions/index.tsx` | List with pagination + useSuspenseQuery |
| `apps/tenant/src/routeTree.gen.ts` | Auto-generated route tree (do not edit) |
| `apps/mobile/app/(main)/[tenantSlug]/(tabs)/_layout.tsx` | Mobile tab navigator |

## Core Domain

| File | Purpose |
|------|---------|
| `core/cases/tenant-admin/` | Workspace management API |
| `core/cases/question-admin/` | Question management API |
| `core/cases/group-admin/` | Group management API |
| `core/cases/takes/` | Take submission API |
| `core/repositories/tenancy/` | Tenant data access |
| `core/repositories/questions/` | Question data access |
| `core/repositories/rebac/` | Relationship/group data access |
| `core/auth/permissions.ts` | Permission checking utilities |
