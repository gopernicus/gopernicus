// gothDialog controller (GOTH-1.4; overlay mechanics GOTH-4.1; modal/panel
// primitives GOTH-4.2).
//
// Modal/nonmodal dialog behavior composed from the shared overlay mechanics:
// focus trap+restore, nested-aware Escape/outside-click dismissal, and — for a
// modal (default; data-modal="false" opts out) — background scroll lock and
// background inert. It backs every GOTH-4.2 modal/panel primitive (Dialog, Alert
// Dialog, Sheet, Drawer) unchanged in name (README §8): data-modal="false" makes
// the dialog non-modal, and data-dismiss-outside="false" (the Alert Dialog
// decision contract) keeps Escape but ignores an outside/scrim press. aria-modal
// is asserted on the content ONLY while modality is actually enforced (background
// inert), so the assertion stays honest under the no-JS baseline. It emits the
// documented goth:open / goth:close events, discovers its content region through
// data-slot="content", and holds only local open/close state — the server owns
// authoritative state (README §8).

import { createFocusTrap } from "../mechanics/focus.js";
import { createDismisser } from "../mechanics/dismiss.js";
import { createScrollLock } from "../mechanics/scroll-lock.js";
import { createBackgroundInert } from "../mechanics/inert.js";

export default function gothDialog() {
  return {
    open: false,
    _root: null,
    _content: null,
    _modal: true,
    _dismissOutside: true,
    _trap: null,
    _dismisser: null,
    _scrollLock: null,
    _inert: null,

    init() {
      // Cache the controller root at init: a method invoked from a descendant's
      // x-on handler (a trigger/close button) sees $el bound to that descendant,
      // so state/queries must run against the cached root, not this.$el (the
      // GOTH-1.5 descendant-$el fix, applied here at source per the GOTH-3.4
      // Phase-4 follow-up).
      this._root = this.$el;
      this._content = this._root.querySelector('[data-slot="content"]') || this._root;
      this._modal = this._root.getAttribute("data-modal") !== "false";
      // Alert Dialog opts out of outside/scrim dismissal (the destructive-decision
      // contract): Escape still cancels, but a scrim press does not. The nesting
      // policy is untouched — this only filters the "outside" reason the shared
      // dismisser already reports.
      this._dismissOutside = this._root.getAttribute("data-dismiss-outside") !== "false";
      this._scrollLock = createScrollLock();
    },

    show() {
      if (this.open) return;
      this.open = true;
      this._root.setAttribute("data-state", "open");
      this._trap = createFocusTrap(this._content);
      this._dismisser = createDismisser(this._content, (reason) => {
        if (reason === "outside" && !this._dismissOutside) return;
        this.hide();
      });
      if (this._modal) {
        // aria-modal is asserted only while background inert is enforced, so the
        // no-JS baseline (nothing inert without JS) never over-claims modality.
        this._content.setAttribute("aria-modal", "true");
        this._scrollLock.lock();
        this._inert = createBackgroundInert(this._root);
        this._inert.activate();
      }
      this.$nextTick(() => {
        this._trap.activate();
        this._dismisser.activate();
        this.$dispatch("goth:open");
      });
    },

    hide() {
      if (!this.open) return;
      this.open = false;
      this._root.setAttribute("data-state", "closed");
      this._content.removeAttribute("aria-modal");
      if (this._dismisser) this._dismisser.deactivate();
      if (this._trap) this._trap.deactivate();
      if (this._inert) {
        this._inert.deactivate();
        this._inert = null;
      }
      this._scrollLock.unlock();
      this.$dispatch("goth:close");
    },

    toggle() {
      if (this.open) {
        this.hide();
      } else {
        this.show();
      }
    },
  };
}
