// Shared scroll-lock mechanics (GOTH-4.1).
//
// Modal overlays (dialog, drawer, sheet) lock background scrolling while open.
// One ref-counted lock owns the <html> class so nested modals do not double-lock
// or prematurely release: the class is added on the first lock and removed only
// when the last lock releases. It toggles a CLASS the compiled theme CSS styles
// (.goth-scroll-locked), never an inline style, so no 'unsafe-inline' is needed
// (README §4 invariant a).

const LOCK_CLASS = "goth-scroll-locked";
let count = 0;

// createScrollLock returns a per-overlay lock handle. lock() engages the shared
// body scroll lock (idempotent per handle); unlock() releases this handle's hold.
export function createScrollLock() {
  let held = false;

  return {
    lock() {
      if (held) return;
      held = true;
      count += 1;
      if (count === 1) {
        document.documentElement.classList.add(LOCK_CLASS);
      }
    },
    unlock() {
      if (!held) return;
      held = false;
      count -= 1;
      if (count <= 0) {
        count = 0;
        document.documentElement.classList.remove(LOCK_CLASS);
      }
    },
  };
}
