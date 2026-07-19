// Shared live-region mechanics (GOTH-1.4).
//
// Toasts, spinners, and status announcements share ONE pair of visually-hidden
// aria-live regions so announcements are consistent and never fork (README §8).
// The regions use the .goth-sr-only utility from the compiled theme CSS — no
// inline style is emitted (README §4 invariant a).

let politeRegion = null;
let assertiveRegion = null;

function ensureRegion(politeness) {
  const assertive = politeness === "assertive";
  const existing = assertive ? assertiveRegion : politeRegion;
  if (existing) return existing;
  const el = document.createElement("div");
  el.setAttribute("aria-live", assertive ? "assertive" : "polite");
  el.setAttribute("aria-atomic", "true");
  el.setAttribute("role", assertive ? "alert" : "status");
  el.className = "goth-sr-only";
  document.body.appendChild(el);
  if (assertive) {
    assertiveRegion = el;
  } else {
    politeRegion = el;
  }
  return el;
}

// announce posts message to the shared live region for the given politeness
// ("polite" default, or "assertive"). It clears first, then sets on the next
// frame so an identical repeated message is re-announced.
export function announce(message, politeness = "polite") {
  if (!message) return;
  const region = ensureRegion(politeness);
  region.textContent = "";
  window.requestAnimationFrame(() => {
    region.textContent = message;
  });
}
