// Shared background-inert mechanics (GOTH-4.1).
//
// A modal overlay makes the rest of the document inert so AT and pointer/focus
// cannot reach the background while the modal is open. This uses the native
// `inert` attribute (supported across Chromium/Firefox/WebKit). Because this kit
// renders overlays inline (not portaled to <body>), inertness is applied to the
// SIBLINGS of the overlay at every level from the overlay root up to <body>: the
// overlay's own ancestor chain stays interactive while everything else goes
// inert. Nested modals stack — each records exactly which elements IT newly set
// and restores only those on release, so an inner modal closing never un-inerts
// the outer modal's background.

// createBackgroundInert returns a handle. activate() inerts every sibling of the
// overlay's ancestor chain; deactivate() clears only the ones it newly set.
export function createBackgroundInert(overlayRoot) {
  const marked = [];

  function shouldSkip(el) {
    // Never inert the shared live-region announcer nodes so status/toast
    // announcements are not silenced by a modal.
    if (el.classList && el.classList.contains("goth-sr-only")) return true;
    // Already inert (an outer modal owns it) — leave it and its ownership alone.
    return el.hasAttribute("inert");
  }

  return {
    activate() {
      let node = overlayRoot;
      const stop = document.body || document.documentElement;
      while (node && node !== stop && node.parentElement) {
        const parent = node.parentElement;
        Array.from(parent.children).forEach((sibling) => {
          if (sibling === node) return;
          if (shouldSkip(sibling)) return;
          sibling.setAttribute("inert", "");
          marked.push(sibling);
        });
        node = parent;
      }
    },
    deactivate() {
      while (marked.length) {
        marked.pop().removeAttribute("inert");
      }
    },
  };
}
