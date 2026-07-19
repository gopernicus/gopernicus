// Shared focus mechanics (GOTH-1.4; nesting-aware GOTH-4.1): focus trap + restore.
//
// One place owns the hard focus mechanics so individual GOTH controllers never
// fork slightly different accessibility behavior (README §8 shared families).
// A module-level trap stack makes nesting correct: when a dialog opens over a
// dialog, only the topmost trap enforces Tab, so focus cannot leak to the parent
// while the child is open, and each deactivate restores focus one level up.

const trapStack = [];

const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

// focusableWithin returns the visible, tabbable descendants of root in DOM order.
export function focusableWithin(root) {
  return Array.from(root.querySelectorAll(FOCUSABLE)).filter(
    (el) => el.offsetParent !== null || el.getClientRects().length > 0,
  );
}

// createFocusTrap returns a trap that, when active, keeps Tab/Shift+Tab inside
// root and, on deactivate, restores focus to the element that had it before
// activate — the shared modal focus contract for dialog/menu/combobox.
export function createFocusTrap(root) {
  let previouslyFocused = null;
  const self = {};

  function onKeydown(event) {
    if (event.key !== "Tab") return;
    // Only the topmost trap acts; a nested child trap fully owns Tab while open.
    if (trapStack[trapStack.length - 1] !== self) return;
    const items = focusableWithin(root);
    if (items.length === 0) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return {
    activate() {
      previouslyFocused = document.activeElement;
      trapStack.push(self);
      root.addEventListener("keydown", onKeydown);
      const items = focusableWithin(root);
      (items[0] || root).focus();
    },
    deactivate() {
      root.removeEventListener("keydown", onKeydown);
      const i = trapStack.indexOf(self);
      if (i >= 0) trapStack.splice(i, 1);
      if (previouslyFocused && typeof previouslyFocused.focus === "function") {
        previouslyFocused.focus();
      }
    },
  };
}
