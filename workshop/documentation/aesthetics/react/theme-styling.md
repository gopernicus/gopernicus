# Theme & Styling

## Web: Three-Layer CSS Architecture

### Layer 1 — Base Theme

**Location:** `bridge/react/ui/styles/theme.css`

Defines semantic design tokens via Tailwind CSS 4's `@theme` directive using OKLCH color space:

```css
@theme {
  /* Radius tokens */
  --radius: 0.5rem;
  --radius-sm: 0.375rem;
  --radius-lg: 0.75rem;

  /* Semantic colors (OKLCH for perceptual uniformity) */
  --color-background: oklch(1 0 0);
  --color-foreground: oklch(0.145 0 0);
  --color-primary: oklch(0.405 0.145 265);
  --color-error: oklch(0.577 0.245 27);
  --color-success: oklch(0.517 0.174 142.5);
  --color-warning: oklch(0.75 0.183 55);

  /* Typography */
  --font-sans: "Montserrat", ui-sans-serif, system-ui, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, monospace;

  /* Z-index scale */
  --z-dropdown: 50;
  --z-sticky: 100;
  --z-overlay: 200;
  --z-modal: 300;
  --z-popover: 400;
  --z-tooltip: 500;
}

/* Dark mode via data attribute */
[data-theme="dark"] {
  --color-background: oklch(0.145 0 0);
  --color-foreground: oklch(0.985 0 0);
  /* ... inverted palette */
}
```

### Layer 2 — Animations

**Location:** `bridge/react/ui/styles/animations.css`

```css
@keyframes fadeIn { from { opacity: 0 } to { opacity: 1 } }
@keyframes slideInFromTop { ... }
@keyframes slideInFromBottom { ... }
@keyframes scaleIn { ... }

.animate-fade-in { animation: fadeIn 150ms ease-out; }
.transition-default { transition: all 150ms ease-out; }
```

### Layer 3 — App Brand Overrides

**Location:** `apps/<app>/src/index.css`

Each app imports the base theme and overrides brand-specific tokens:

```css
@import "@<project>/react/ui/styles";
@source "../../../bridge/react";       /* Scan bridge for Tailwind classes */

@theme {
  --color-primary: #192a56;
  --color-secondary: #53629e;
  --color-tertiary: #87bac3;
  --font-title: "fatfrank", sans-serif;
  --font-body: "hoss-round", sans-serif;
  --font-button: "korolev", sans-serif;
}
```

## Tech Stack

- **Tailwind CSS 4** — `@import "tailwindcss"` for full engine
- **OKLCH color space** — better perceptual uniformity than HSL
- **CSS custom properties** — all tokens available as `var(--color-primary)` etc.
- **Data attribute selectors** — `[data-disabled]`, `[data-open]`, `[data-invalid]` for state styling
- **`cn()` utility** — `clsx()` + `tailwind-merge()` for safe class composition

## Class Utility

```typescript
// bridge/react/ui/utils.ts
import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
```

Use `cn()` everywhere classes are composed. It deduplicates conflicting Tailwind utilities (e.g., `cn("px-4", "px-2")` → `"px-2"`).

---

## Native: Context-Based Theme

React Native uses a context-based theme system instead of CSS.

**Location:** `bridge/react-native/ui/theme/`

### Token Definitions

```typescript
export const colors = {
  background: "#f5f5f7",
  foreground: "#0a0a0a",
  primary: "#0ea5e9",
  error: "...",
  success: "...",
  // ...
}

export interface Theme {
  colors: Colors
  spacing: { xs: 4, sm: 8, md: 16, lg: 24, xl: 32 }
  radius: { sm: 4, md: 8, lg: 12, full: 9999 }
  fontSize: { xs: 12, sm: 14, md: 16, lg: 18, xl: 20 }
  fonts?: { title, subtitle, body, button, nav }
}
```

### Provider & Hooks

```typescript
// Wrap app root
<ThemeProvider theme={customTheme}>
  {children}
</ThemeProvider>

// Consume in components
const colors = useColors()
const theme = useTheme()
const fonts = useFonts()
```

### Native Component Styling

Native primitives use `StyleSheet.create()` + theme hooks:

```typescript
export function Button({ variant = "primary", size = "md", ...props }) {
  const colors = useColors()
  const variantStyle = getVariantStyle(variant, colors)
  const sizeStyle = sizeStyles[size]

  return (
    <Pressable
      style={({ pressed }) => [
        styles.base,
        variantStyle.container,
        sizeStyle.container,
        pressed && styles.pressed,
      ]}
      {...props}
    />
  )
}
```

The variant/size API surface mirrors web primitives where possible for conceptual consistency.
