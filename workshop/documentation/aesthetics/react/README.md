# Aesthetics — React Frontend Documentation

The frontend layer of a Gopernicus-generated application. This monorepo mirrors the backend's hexagonal architecture: **SDK** (infrastructure), **Core** (business logic), **Bridge** (platform adapters), and **Apps** (entry points).

## Documents

| Document                                            | What it covers                                                                              |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| [Architecture & Monorepo](./architecture.md)        | Layer diagram, package structure, dependency graph, workspace config                        |
| [Component Best Practices](./components.md)         | State management, React Hooks (useEffect, useMemo), Zustand stores, derived state           |
| [UI Component System](./ui-system.md)               | Primitives, composed components, Base UI, CVA variants, extending the system                |
| [Native UI Component System](./ui-system-native.md) | Primitives, composed components, StyleSheet layout, useColors theming, extending the system |
| [Theme & Styling](./theme-styling.md)               | Tailwind 4, CSS variables, OKLCH colors, animations, brand overrides, native theme          |
| [Data Loading & TanStack](./data-loading.md)        | Query options factories, route loaders, Suspense, mutations, CRUD hooks                     |
| [Forms](./forms.md)                                 | @tanstack/react-form patterns, useFormStatus, submission flow                               |
| [Authentication](./authentication.md)               | Web cookies, mobile tokens, route guards, auth components, OAuth                            |
| [Native / Expo](./native-expo.md)                   | Expo Router, shared vs platform-specific code, current state                                |
| [File Reference](./file-reference.md)               | Quick-lookup table of every key file by category                                            |
