// Shared submenu-hierarchy mechanics (GOTH-4.1).
//
// A menu item that opens a nested menu (data-slot="submenu-trigger" + a sibling
// data-slot="submenu" panel) shares ONE submenu implementation so context menus,
// dropdown menus, and menubars all behave the same (README §8). The submenu is
// anchored to the side of its trigger (RTL-aware) via the anchor mechanic, its
// items get roving focus, and it participates in the overlay stack so Escape /
// outside-click dismiss it before its parent menu. Keyboard: ArrowRight (LTR) /
// ArrowLeft (RTL) or Enter/Space on the trigger opens and focuses the first item;
// ArrowLeft (LTR) / ArrowRight (RTL) or Escape on the panel closes and returns
// focus to the trigger.

import { createRovingFocus } from "./roving.js";
import { createAnchor } from "./anchor.js";
import { createDismisser } from "./dismiss.js";

function isRTL(el) {
  return (
    typeof getComputedStyle === "function" &&
    getComputedStyle(el).direction === "rtl"
  );
}

// createSubmenu wires a submenu trigger to its panel. rootEl is the
// data-slot="submenu-root" wrapper (the "inside" region for dismissal).
export function createSubmenu(rootEl, triggerEl, panelEl) {
  const items = Array.from(panelEl.querySelectorAll('[data-slot="item"]'));
  const roving = createRovingFocus(items, { orientation: "vertical" });
  let anchor = null;
  let dismisser = null;
  let open = false;

  function openKey() {
    return isRTL(triggerEl) ? "ArrowLeft" : "ArrowRight";
  }
  function closeKey() {
    return isRTL(triggerEl) ? "ArrowRight" : "ArrowLeft";
  }

  function show() {
    if (open) return;
    open = true;
    triggerEl.setAttribute("aria-expanded", "true");
    panelEl.setAttribute("data-state", "open");
    roving.init();
    anchor = createAnchor(triggerEl, panelEl, {
      side: isRTL(triggerEl) ? "left" : "right",
      align: "start",
    });
    anchor.activate();
    // Escape returns focus to the submenu trigger (APG); an outside click leaves
    // focus wherever the pointer landed.
    dismisser = createDismisser(rootEl, (reason) => hide(reason === "escape"));
    dismisser.activate();
    if (items[0]) {
      roving.setActive(0);
    }
  }

  function hide(refocus = false) {
    if (!open) return;
    open = false;
    triggerEl.setAttribute("aria-expanded", "false");
    panelEl.setAttribute("data-state", "closed");
    if (dismisser) dismisser.deactivate();
    if (anchor) anchor.deactivate();
    if (refocus) triggerEl.focus();
  }

  function onTriggerKeydown(event) {
    if (event.key === openKey() || event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      event.stopPropagation();
      show();
    }
  }

  function onPanelKeydown(event) {
    if (event.key === closeKey() || event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      hide(true);
      return;
    }
    roving.onKeydown(event);
  }

  triggerEl.addEventListener("keydown", onTriggerKeydown);
  triggerEl.addEventListener("pointerenter", () => show());
  panelEl.addEventListener("keydown", onPanelKeydown);

  return {
    isOpen: () => open,
    show,
    hide,
  };
}
