# Component & Hook Best Practices (2026)

React development has matured. We no longer write defensive, heavily memoized code, nor do we use `useEffect` as a lifecycle method. This document outlines our modern approach to state management, hooks, and composition.

## The 3 Golden Rules

1. **If you can derive it, don't put it in state.**
2. **`useEffect` is an escape hatch, not a data fetcher.**
3. **Server state belongs in TanStack Query. Client state belongs in Zustand or `useState`.**

---

## React Hooks: The Modern Playbook

### 1. `useState` (Local Component State)

Use this exclusively for ephemeral, UI-only state that belongs strictly to one component (e.g., `isOpen`, `inputValue`, `activeIndex`).

**Best Practices:**

- **Never mirror props in state.** If a prop changes, the component will re-render anyway. Just derive the value directly.
- **Group related state.** If you have `setFirstName` and `setLastName` that always update together, use a single object state or standard form handling.

### 2. `useEffect` (The Escape Hatch)

`useEffect` should be the rarest hook in your codebase. It is strictly for synchronizing your React component with an **external system** (e.g., WebSockets, third-party DOM libraries, analytics tracking).

**Anti-patterns to aggressively avoid:**

- ❌ **Fetching data:** Use TanStack Query (`useQuery`, `useSuspenseQuery`).
- ❌ **Updating state based on other state:** If State B changes when State A changes, State B shouldn't be state—it should be a derived variable calculated during render.
- ❌ **Reacting to user events:** If a user clicks a button, handle the logic in the `onClick` handler, not in a `useEffect` watching a boolean.

### 3. `useRef` (Mutable Values & DOM Nodes)

Use `useRef` when you need a value to persist across renders **without** triggering a re-render.

**Best Practices:**

- Use it to store DOM node references (e.g., focusing an input).
- Use it to store mutable instances like `setTimeout` IDs, previous values, or third-party class instances (like a video player object).
- **Never** read or write `ref.current` during the render phase. Only interact with refs inside event handlers or `useEffect`.

### 4. `useMemo` & `useCallback` (Memoization)

In 2026, manual memoization is largely obsolete due to modern React compilation and better architectural patterns. You should rarely write these by hand.

**Best Practices:**

- Only use `useMemo` if you are doing a genuinely computationally expensive mathematical calculation (e.g., filtering a list of 10,000 items on the client).
- Only use `useCallback` if you are passing a function as a dependency to a custom hook that strictly requires referential equality.
- Otherwise, let React render. Re-rendering is fast; memory overhead from excessive `useMemo` is not.

---

## Application State: Zustand

We use [Zustand](https://github.com/pmndrs/zustand) for global, client-side application state. It is lightweight, boilerplate-free, and doesn't require Context providers.

### When to use Zustand:

- **Global UI State:** E.g., `isSidebarOpen`, `themeMode`.
- **Cross-Route State:** E.g., A multi-step form wizard where data needs to persist as the user navigates between `/step-1` and `/step-2`.
- **User Preferences:** Client-side settings that don't need an immediate database round-trip.

### When NOT to use Zustand:

- ❌ **Server Data:** Do not put API responses, user profiles, or lists of questions in Zustand. That is Server State, and TanStack Query handles caching, invalidation, and polling for you.
- ❌ **Local UI State:** If only one component cares about a piece of state, keep it in `useState`. Don't pollute the global store.

### Zustand Store Pattern

Keep stores small and domain-specific. Slice them if they get too large.

```typescript
import { create } from "zustand";

interface AudioRecorderState {
  activeDeviceId: string | null;
  setActiveDevice: (id: string) => void;
  isMuted: boolean;
  toggleMute: () => void;
}

export const useAudioStore = create<AudioRecorderState>((set) => ({
  activeDeviceId: null,
  setActiveDevice: (id) => set({ activeDeviceId: id }),
  isMuted: false,
  toggleMute: () => set((state) => ({ isMuted: !state.isMuted })),
}));

// ✅ GOOD: Component only re-renders when isMuted changes
const isMuted = useAudioStore((state) => state.isMuted);

// ❌ BAD: Component re-renders when ANY value in the store changes
const store = useAudioStore();
```
