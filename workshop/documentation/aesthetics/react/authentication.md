# Authentication

## Web Auth (Cookie-Based)

```typescript
// apps/<app>/src/config.ts
const http = createClient({
  baseUrl: "",
  credentials: "include",  // Cookies sent automatically
})

export const authApi = createAuthApi(http)
export const sessionQueryOptions = createSessionQueryOptions(authApi)
export const requireAuth = createRequireAuth(sessionQueryOptions, {
  loginPath: "/login",
})
export const redirectIfAuthenticated = createRedirectIfAuthenticated(sessionQueryOptions, {
  defaultPath: "/",
})
```

### Route Guards

```typescript
// Authenticated layout — redirects to login if unauthenticated
export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ context, location }) => {
    const auth = await requireAuth(context.queryClient, location)
    context.queryClient.prefetchQuery(tenantAdminQueryOptions.myMemberships())
    return { auth }
  },
  component: AppLayout,
  pendingComponent: () => <RouteLoading fullScreen />,
  errorComponent: ({ error }) => <RouteError error={error} fullScreen />,
})

// Auth layout — redirects away from login if already authenticated
export const Route = createFileRoute("/_auth")({
  beforeLoad: async ({ context, search }) => {
    await redirectIfAuthenticated(context.queryClient, search)
  },
  component: () => <Outlet />,
})
```

**How `requireAuth` works:**
1. Calls `ensureQueryData(sessionQueryOptions)` to check for cached session
2. If session exists and is valid → returns `MeResponse`
3. If no session → throws `redirect({ to: loginPath, search: { redirect: location.pathname } })`
4. Supports redirect-after-login via `?redirect=` search param

**How `redirectIfAuthenticated` works:**
1. Checks session cache
2. If authenticated → throws `redirect({ to: search.redirect || defaultPath })`
3. If not → returns null, allows page to render

---

## Mobile Auth (Token-Based)

```typescript
// apps/mobile/src/config.ts
const http = createClient({
  baseUrl: API_BASE_URL,
  credentials: "omit",
  getHeaders: async () => {
    const token = await SecureStore.getItemAsync(ACCESS_TOKEN_KEY)
    return token ? { Authorization: `Bearer ${token}` } : {}
  },
  onRefresh: async () => {
    const refreshToken = await SecureStore.getItemAsync(REFRESH_TOKEN_KEY)
    if (!refreshToken) return false
    // POST /auth/refresh with refresh token
    // Store new tokens in SecureStore
    return true
  },
  onAuthFailure: () => {
    // Force logout — clear tokens, redirect to login
  },
})
```

### AuthProvider

The mobile app wraps everything in an `AuthProvider` that manages:

- Token storage via `expo-secure-store` (encrypted on-device)
- Login flow → store access + refresh tokens
- Automatic refresh on 401 (handled by HTTP client)
- OAuth flow via `expo-auth-session` + `expo-web-browser`
- Context value: `{ user, session, isAuthenticated, isLoading, login(), logout() }`

### OAuth Flow (Mobile)

```typescript
// 1. Initiate — get authorization URL from backend
const { authorization_url, flow_secret } = await authApi.mobileInitiateOAuth({ provider: "google" })

// 2. Store flow secret in SecureStore

// 3. Open browser for user consent
const result = await WebBrowser.openAuthSessionAsync(authorization_url, redirectUri)

// 4. Complete — exchange code for tokens via backend
await authApi.mobileOAuthCallback({ code, flow_secret })
```

---

## Auth Components

**Location:** `bridge/react/auth/components/` — Reusable forms for the full auth flow.

| Component | Purpose |
|-----------|---------|
| `LoginForm` | Email/password with error-specific handling |
| `RegisterForm` | Account creation with password confirmation |
| `VerifyEmailForm` | OTP verification with resend capability |
| `ForgotPasswordForm` | Password reset request |
| `ResetPasswordForm` | New password entry with token |
| `ChangePasswordForm` | Authenticated password change |
| `OAuthButtons` | Provider buttons (Google, GitHub, etc.) |

All auth forms accept an `auth: AuthHooks` prop — they don't import hooks directly, making them testable and reusable across apps.

### Auth Form Pattern

```typescript
export function LoginForm({
  auth,
  onSuccess,
  onEmailNotVerified,
  feedback = "inline",
}: LoginFormProps) {
  const login = auth.useLogin()
  const { status, showError, clearStatus } = useFormStatus({ feedback })

  const form = useForm({
    defaultValues: { email: "", password: "" },
    onSubmit: async ({ value }) => {
      clearStatus()
      try {
        await login.mutateAsync(value)
        onSuccess?.()
      } catch (error) {
        if (isEmailNotVerified(error)) {
          showError("Please verify your email address.")
          onEmailNotVerified?.(value.email)
          return
        }
        if (isInvalidCredentials(error)) {
          showError("Invalid email or password.")
          return
        }
        if (isRateLimited(error)) {
          showError("Too many attempts. Please try again later.")
          return
        }
        showError(AuthError.isAuthError(error)
          ? error.message
          : "An unexpected error occurred.")
      }
    },
  })

  // ... render with form.Field
}
```

### Error Checking Utilities

From `core/auth/errors.ts`:

- `isInvalidCredentials(error)` — wrong email/password
- `isEmailNotVerified(error)` — account exists but unverified
- `isRateLimited(error)` — too many attempts
- `isTokenExpiredError(error)` — JWT expired
- `isSessionExpired(error)` — session no longer valid
- `AuthError.isAuthError(error)` — any auth-related error

---

## Session Query Options

```typescript
// bridge/react-shared/auth/index.ts
export function createSessionQueryOptions(authApi: AuthApi) {
  return queryOptions({
    queryKey: authKeys.session(),
    queryFn: async (): Promise<MeResponse | null> => {
      try {
        return await authApi.getCurrentUser()
      } catch {
        try {
          await authApi.refresh()
          return await authApi.getCurrentUser()
        } catch {
          return null
        }
      }
    },
    staleTime: 4 * 60 * 1000,  // 4 minutes
    gcTime: 10 * 60 * 1000,    // 10 minutes
    retry: false,
  })
}
```

The session query attempts a refresh on failure before returning null — this handles token expiry transparently.
