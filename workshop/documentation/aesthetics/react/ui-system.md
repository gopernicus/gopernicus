# UI Component System

## Design Philosophy

The UI system follows a **three-tier composition model**:

1. **Base UI (external)** — Unstyled, accessible headless components from `@base-ui-components/react`. These provide behavior, ARIA attributes, and keyboard interactions with zero styling opinions.

2. **Primitives (internal)** — Thin wrappers around Base UI that add Tailwind styling via CVA (class-variance-authority). Each primitive has defined variants (size, color, state) but remains a single-purpose building block.

3. **Composed Components (internal)** — Higher-level components built from primitives. These encode specific UI patterns (FormField, ListView, Layout) and contain layout/composition logic.

**Why this approach:**
- Base UI handles accessibility — we never re-implement ARIA patterns
- CVA provides type-safe, composable variants — no `className` string soup
- Primitives are styled but unopinionated about layout — apps compose freely
- Base UI uses `data-*` attributes for state — our styles hook into these naturally

---

## Primitives

**Location:** `bridge/react/ui/primitives/` — 39 components wrapping `@base-ui-components/react`.

Every primitive follows the same pattern:

```typescript
import { Button as BaseButton } from "@base-ui-components/react/button"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../utils"

// 1. Define variants with CVA
export const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 ...", // base styles
  {
    variants: {
      variant: {
        primary: "bg-primary text-primary-foreground hover:bg-primary/90",
        "outline-primary": "border border-primary text-primary ...",
        muted: "bg-muted text-muted-foreground ...",
        ghost: "hover:bg-accent hover:text-accent-foreground",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        xs: "h-7 px-2 text-xs",
        sm: "h-9 px-3",
        md: "h-10 px-4 py-2",   // default
        lg: "h-11 px-6",
        xl: "h-12 px-8 text-lg",
      },
    },
    defaultVariants: { variant: "primary", size: "md" },
  }
)

// 2. Wrap Base UI with styled className
export function Button({ className, variant, size, ...props }: ButtonProps) {
  return (
    <BaseButton
      className={(state) =>
        cn(
          buttonVariants({ variant, size }),
          typeof className === "function" ? className(state) : className
        )
      }
      {...props}
    />
  )
}
```

**Key points:**
- Base UI passes render state (e.g., `data-disabled`, `data-open`) — primitives respond with conditional styles
- `cn()` = `clsx()` + `tailwind-merge()` — safe class merging without conflicts
- All primitives accept a `className` prop for one-off overrides
- Icons come from `lucide-react` (web) / `lucide-react-native` (mobile)

### Primitive Categories

| Category | Components |
|----------|-----------|
| **Inputs** | Button, Input, Label, Checkbox, Radio, Switch, Toggle, Slider, Select, Combobox, Autocomplete, NumberField |
| **Overlays** | Dialog, AlertDialog, Popover, Tooltip, PreviewCard |
| **Menus** | Menu, ContextMenu, Menubar |
| **Navigation** | NavigationMenu, Tabs |
| **Layout** | Accordion, Collapsible, Separator, ScrollArea, Avatar, Progress, Meter, Toolbar |
| **Form** | Field, Fieldset, Form |
| **Feedback** | Toast (ToastProvider, ToastViewport, ToastRoot, etc.) |

---

## Composed Components

**Location:** `bridge/react/ui/components/` — Built from primitives, encode specific UI patterns.

| Component | Purpose |
|-----------|---------|
| **FormField** | Wraps Field primitive with label, error, description, aria attributes |
| **FormStatus** | Displays success/error/warning/info messages |
| **useFormStatus** | Hook for managing form feedback (inline, toast, or both) |
| **Card** | CardHeader, CardTitle, CardDescription, CardContent, CardFooter |
| **ListView** | Data table with search, column sorting, filtering, cursor pagination |
| **CursorPagination** | Previous/Next navigation with page counts |
| **useListState** | Manages sort state, search params, pagination cursor |
| **PageHeader** | Title, description, action buttons |
| **Layout** | SidebarLayout, Sidebar, SidebarNav, Header, Breadcrumbs |
| **UserMenu** | Dropdown profile menu |
| **ConfirmDialog** | Pre-built confirmation modal |
| **RouteStatus** | RouteLoading, RouteError, RouteNotFound for route boundaries |
| **SchemaForm** | Auto-generates CRUD forms from EntityUISchema definitions |
| **Toaster** | Global toast notification manager |

---

## Extending the UI System

### Adding a New Primitive

1. Create `bridge/react/ui/primitives/my-component.tsx`
2. Import the Base UI headless component
3. Define CVA variants for styling
4. Wrap with `cn()` for className merging
5. Export from `bridge/react/ui/primitives/index.ts`

```typescript
// bridge/react/ui/primitives/badge.tsx
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../utils"

export const badgeVariants = cva(
  "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground",
        secondary: "bg-secondary text-secondary-foreground",
        outline: "border border-border text-foreground",
      },
    },
    defaultVariants: { variant: "default" },
  }
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ variant }), className)} {...props} />
}
```

### Adding a Composed Component

1. Create `bridge/react/ui/components/my-component.tsx`
2. Import primitives from `../primitives`
3. Compose layout and behavior logic
4. Export from `bridge/react/ui/components/index.ts`

### Adding a Native Primitive

1. Create `bridge/react-native/ui/primitives/my-component.tsx`
2. Use React Native core components (`View`, `Text`, `Pressable`)
3. Consume theme via `useColors()`, `useTheme()` hooks
4. Use `StyleSheet.create()` for styles
5. Export from `bridge/react-native/ui/primitives/index.ts`

---

## Component Consumption

Components are imported via package subpath exports:

```typescript
// Primitives
import { Button, Input, Dialog, DialogPopup } from "@<project>/react/ui/primitives"

// Composed components
import { FormField, ListView, useFormStatus } from "@<project>/react/ui/components"

// Styles (in app CSS)
@import "@<project>/react/ui/styles";
```

### Usage Example

```typescript
import {
  Dialog, DialogPortal, DialogBackdrop, DialogPopup,
  DialogHeader, DialogTitle, DialogFooter, DialogClose,
  Button, Input
} from "@<project>/react/ui/primitives"
import { FormField } from "@<project>/react/ui/components"

<Dialog open={open} onOpenChange={onOpenChange}>
  <DialogPortal>
    <DialogBackdrop />
    <DialogPopup>
      <DialogHeader>
        <DialogTitle>Edit Question</DialogTitle>
      </DialogHeader>
      <form onSubmit={handleSubmit}>
        <FormField label="Question" error={errors.text}>
          <Input value={text} onChange={...} />
        </FormField>
        <DialogFooter>
          <DialogClose render={<Button variant="outline-muted" />}>Cancel</DialogClose>
          <Button type="submit">Save</Button>
        </DialogFooter>
      </form>
    </DialogPopup>
  </DialogPortal>
</Dialog>
```
