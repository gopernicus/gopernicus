# Native UI Component System

## Design Philosophy

The Native UI system follows a **two-tier composition model**, prioritizing native performance and explicit styling over utility classes:

1. **Primitives (internal)** — Thin wrappers around standard React Native core components (`Pressable`, `View`, `Text`, `TextInput`). These handle base layout via `StyleSheet` and dynamic theming via our custom `useColors()` hook.
2. **Composed Components (internal)** — Higher-level components built from primitives. These encode specific mobile UI patterns (ScreenLayout, BottomSheet, FormField) and contain layout/composition logic.

**Why this approach:**

- **No Translation Overhead:** Using `StyleSheet.create` avoids the runtime compilation costs and bundler quirks associated with NativeWind/Tailwind.
- **Predictable Theming:** The `useColors()` hook explicitly maps our design system variables (primary, muted, accent) to the current theme without relying on string interpolation.
- **Native Interaction:** We default to `Pressable` (over `TouchableOpacity`) to utilize modern React Native interaction states (hover, pressed) cleanly.

---

## Primitives

**Location:** `bridge/react-native/ui/primitives/`

Every primitive follows the same pattern: standardizing layout in a `StyleSheet` and applying colors dynamically inline.

```tsx
import { Pressable, Text, StyleSheet, type PressableProps, type ViewStyle, type TextStyle } from "react-native";
import { useColors } from "@pointtaken/react-native/ui/theme";

interface ButtonProps extends PressableProps {
  label: string;
  variant?: "primary" | "secondary" | "outline" | "ghost";
  size?: "sm" | "md" | "lg";
  style?: ViewStyle;
  textStyle?: TextStyle;
}

export function Button({
  label,
  variant = "primary",
  size = "md",
  style,
  textStyle,
  ...props
}: ButtonProps) {
  const colors = useColors();

  // 1. Dynamic style resolution based on variant
  const getVariantStyles = (pressed: boolean) => {
    switch (variant) {
      case "secondary":
```
