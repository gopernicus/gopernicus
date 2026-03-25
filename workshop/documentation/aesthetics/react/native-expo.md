# Native / Expo

The mobile app is built with Expo 54, React Native 0.81, and Expo Router 6. It shares all business logic and data fetching with the web app via `react-shared`.

**Current state:** The native UI layer is still being developed. Core patterns are established but the component library is smaller than web.

---

## What's Shared (via react-shared)

- All TanStack Query hooks and query options
- All core API clients and types
- Auth error handling utilities
- Permission checking logic

## What's Platform-Specific

| Concern | Web | Mobile |
|---------|-----|--------|
| **Router** | TanStack Router (file-based) | Expo Router (file-based) |
| **Data loading** | Route loaders + `useSuspenseQuery` | `useQuery` in screens |
| **Authentication** | Cookies + route guards | SecureStore + AuthProvider context |
| **UI components** | Base UI + Tailwind | RN Pressable/TextInput + StyleSheet |
| **Theme** | CSS variables + Tailwind | Context-based `useTheme()` hook |
| **Icons** | `lucide-react` | `lucide-react-native` |
| **Build** | Vite | Expo (Metro) |
| **State** | Route context + Zustand | React Context |

---

## Expo Router Navigation Structure

```
app/
├── index.tsx                    # Auth check → redirect to (auth) or (main)
├── _layout.tsx                  # Root: Stack + QueryClientProvider + ThemeProvider + AuthProvider
├── (auth)/
│   ├── _layout.tsx
│   ├── login.tsx
│   ├── register.tsx
│   ├── verify-email.tsx
│   ├── forgot-password.tsx
│   └── reset-password.tsx
└── (main)/
    ├── _layout.tsx              # Auth guard, Stack navigator
    ├── index.tsx                # Workspace switcher
    └── [tenantSlug]/
        ├── _layout.tsx          # TenantProvider wrapper
        └── (tabs)/
            ├── _layout.tsx      # Bottom tab navigator
            ├── (home)/
            ├── (assignments)/
            ├── (questions)/
            ├── (groups)/
            └── (more)/
```

- `(auth)` and `(main)` are route groups (don't appear in URLs)
- `[tenantSlug]` is a dynamic route parameter
- Tabs use route groups with Stack navigators inside each tab

---

## Provider Stack

```typescript
// app/_layout.tsx
export default function RootLayout() {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider theme={appTheme}>
        <AuthProvider>
          <Stack>
            <Stack.Screen name="(auth)" />
            <Stack.Screen name="(main)" />
          </Stack>
        </AuthProvider>
      </ThemeProvider>
    </QueryClientProvider>
  )
}
```

---

## Data Loading (No Route Loaders)

Expo Router does not support loaders like TanStack Router. Screens use `useQuery` directly:

```typescript
export default function DashboardScreen() {
  const { tenantId } = useTenant()
  const { data, isLoading, refetch } = useQuery(
    questionAdminQueryOptions.myAssignments(tenantId)
  )

  if (isLoading) return <ActivityIndicator />

  return (
    <FlatList
      data={data?.data}
      renderItem={({ item }) => <AssignmentCard assignment={item} />}
      onRefresh={refetch}
      refreshing={isLoading}
    />
  )
}
```

The query options are the same factories used by the web app — only the consumption pattern differs.

---

## Config & Environment

**HTTP client initialization:**
```typescript
// apps/mobile/src/config.ts
const API_BASE_URL = process.env.EXPO_PUBLIC_API_BASE_URL
  ?? (Platform.OS === "android" ? "http://10.0.2.2:3000" : "http://localhost:3000")
```

**Expo config highlights** (`app.json`):
- New Architecture enabled (`newArchEnabled: true`)
- Microphone permissions for audio recording
- OAuth redirect schemes configured
- Metro bundler with web support

---

## Key Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `expo` | ~54.0 | Framework |
| `expo-router` | ~6.0 | File-based navigation |
| `expo-secure-store` | ~14.0 | Encrypted token storage |
| `expo-av` | ~15.0 | Audio recording |
| `expo-auth-session` | ~7.0 | OAuth flows |
| `expo-web-browser` | - | OAuth browser redirect |
| `react-native` | 0.81 | UI runtime |
| `@tanstack/react-query` | ^5.62 | Data fetching (shared with web) |
| `@tanstack/react-form` | ^1.0 | Forms (shared with web) |
| `lucide-react-native` | ~0.525 | Icons |

---

## Native UI Package

**Location:** `bridge/react-native/ui/`

```
react-native/ui/
├── primitives/         # Button, Input, Label, Dialog
├── components/         # Card
└── theme/              # ThemeProvider, useColors(), useTheme(), useFonts()
```

The native primitive library is smaller than web and actively being expanded. See [UI System](./ui-system.md) for the adding-a-native-primitive pattern.
