// Native-popover anchoring enhancement (GOTH-4.3).
//
// The Popover primitive (P45) rides the native popover/popovertarget attributes:
// click toggles, Escape closes, an outside press light-dismisses, and the content
// enters the top layer — all with NO JavaScript (the StylesOnly baseline). The one
// thing native popover does not do is anchor the panel to its trigger (CSS anchor
// positioning is not yet reliable across all three engines), so it defaults to the
// UA-centered position. This enhancement — shipped only in the Interactive/Full
// runtime — positions an opened goth popover against its invoker with the shared
// anchor mechanic (collision flip/clamp), staying CSP-safe (CSSOM custom
// properties, no inline style) and adding NO controller: it is a delegated
// listener, not an Alpine component.
//
// The native ToggleEvent does not bubble, so the document listener is registered
// in the CAPTURE phase (capturing runs for non-bubbling events too).

import { createAnchor } from "./anchor.js";

const active = new WeakMap();

function onToggle(event) {
  const el = event.target;
  if (!el || typeof el.matches !== "function") return;
  if (!el.matches('[data-slot="popover-content"]')) return;

  if (event.newState === "open") {
    const trigger = el.id
      ? document.querySelector('[popovertarget="' + CSS.escape(el.id) + '"]')
      : null;
    if (!trigger) return;
    const side = el.getAttribute("data-side-preferred") || "bottom";
    const align = el.getAttribute("data-align-preferred") || "start";
    const anchor = createAnchor(trigger, el, { side, align });
    active.set(el, anchor);
    anchor.activate();
  } else {
    const anchor = active.get(el);
    if (anchor) {
      anchor.deactivate();
      active.delete(el);
    }
  }
}

// initPopoverAnchoring wires the single delegated capture listener once.
export function initPopoverAnchoring() {
  document.addEventListener("toggle", onToggle, true);
}
