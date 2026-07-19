// Shared overlay-stack mechanics (GOTH-4.1): the nesting model that every
// overlay/menu shares so escape/outside-click behave correctly when overlays are
// stacked (dialog over dialog, submenu over menu). One module owns the single
// document-level Escape/pointer listeners; individual controllers never fork a
// slightly different dismissal policy (README §8 shared families).
//
// The stack is last-in/first-out. Escape dismisses ONLY the topmost overlay so a
// nested overlay closes before its parent. An outside pointer press dismisses,
// from the top down, every overlay that does not contain the pressed target and
// stops at the first overlay that does — so clicking inside the parent (but
// outside the child) closes only the child, and clicking fully outside closes the
// whole stack.

const stack = [];
let listening = false;

function onKeydown(event) {
  if (event.key !== "Escape") return;
  const top = stack[stack.length - 1];
  if (!top) return;
  // Stop propagation so a single Escape unwinds exactly one level per press.
  event.stopPropagation();
  top.onDismiss("escape");
}

function onPointerdown(event) {
  // Collect the overlays to dismiss BEFORE calling any onDismiss, because a
  // dismiss handler pops itself from the stack (mutating it mid-iteration).
  const toDismiss = [];
  for (let i = stack.length - 1; i >= 0; i--) {
    if (stack[i].root.contains(event.target)) break;
    toDismiss.push(stack[i]);
  }
  toDismiss.forEach((entry) => entry.onDismiss("outside"));
}

function ensureListeners() {
  if (listening) return;
  // Capture phase so an overlay dismisses before an inner handler can stop
  // propagation, matching the frozen dismiss policy from GOTH-1.4.
  document.addEventListener("keydown", onKeydown, true);
  document.addEventListener("pointerdown", onPointerdown, true);
  listening = true;
}

// pushOverlay registers an open overlay { root, onDismiss } on top of the stack
// and returns the entry as an opaque handle for popOverlay.
export function pushOverlay(entry) {
  stack.push(entry);
  ensureListeners();
  return entry;
}

// popOverlay removes an overlay entry from the stack (idempotent).
export function popOverlay(entry) {
  const i = stack.indexOf(entry);
  if (i >= 0) stack.splice(i, 1);
}

// isTopOverlay reports whether entry is the current topmost overlay — used by
// mechanics (e.g. focus trap) that must act only for the active overlay.
export function isTopOverlay(entry) {
  return stack.length > 0 && stack[stack.length - 1] === entry;
}

// overlayDepth is the current number of open overlays (test/diagnostic hook).
export function overlayDepth() {
  return stack.length;
}
