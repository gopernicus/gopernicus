// Shared dismissal mechanics (GOTH-1.4; nested-aware GOTH-4.1).
//
// Overlays (dialog, menu, combobox, popover) share ONE dismissal implementation
// so escape/outside-click behavior is identical everywhere (README §8). The
// nesting policy lives in the overlay stack: Escape dismisses only the topmost
// overlay, and an outside pointer press dismisses the stack from the top down to
// the overlay containing the target. createDismisser is the per-overlay handle a
// controller activates/deactivates; the stack owns the global listeners.

import { pushOverlay, popOverlay } from "./overlay-stack.js";

// createDismisser calls onDismiss("escape") when this overlay is topmost and
// Escape is pressed, and onDismiss("outside") on an outside pointer press,
// respecting the nesting order. root is the element considered "inside".
export function createDismisser(root, onDismiss) {
  let entry = null;

  return {
    activate() {
      if (entry) return;
      entry = pushOverlay({ root, onDismiss });
    },
    deactivate() {
      if (!entry) return;
      popOverlay(entry);
      entry = null;
    },
  };
}
