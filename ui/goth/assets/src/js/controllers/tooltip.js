// gothTooltip controller (GOTH-4.3; added to the frozen §8 set by the recorded
// owner reopen — see gate-b-review.md 2026-07-17 addendum).
//
// Enhances a describedby tooltip whose no-JS baseline is a pure CSS hover/
// focus-within reveal (the content is always in the DOM with role="tooltip", and
// the trigger's aria-describedby points at it). This controller composes the
// frozen GOTH-4.1 mechanics — anchored placement (anchor.js) + nested-aware
// Escape/outside dismissal (dismiss.js/overlay-stack.js) — plus the shared
// hover-intent timing (hover-intent.js): pointer hover opens after an intent
// delay, focus opens immediately (no artificial keyboard delay), and Escape,
// blur, or an outside press hides it. It never traps focus and never moves focus
// into the tooltip (the content is non-interactive). It marks the root
// data-enhanced so the CSS switches from the instant :hover baseline to the
// delayed, controller-owned data-state. Dispatches goth:open / goth:close.

import { createAnchor } from "../mechanics/anchor.js";
import { createDismisser } from "../mechanics/dismiss.js";
import { createHoverIntent } from "../mechanics/hover-intent.js";

export default function gothTooltip() {
  return {
    open: false,
    _root: null,
    _trigger: null,
    _content: null,
    _anchor: null,
    _dismisser: null,
    _intent: null,
    _hovering: false,
    _focused: false,

    init() {
      // Cache the root at init; a method invoked from a descendant's x-on handler
      // sees $el bound to that descendant (the shared GOTH-1.5 descendant-$el fix).
      this._root = this.$el;
      this._trigger = this._root.querySelector('[data-slot="trigger"]');
      this._content = this._root.querySelector('[data-slot="tooltip-content"]');
      this._intent = createHoverIntent({ openDelay: 200, closeDelay: 120 });
      // Signal the CSS to stop using the instant :hover reveal and follow the
      // controller-owned data-state (which carries the intent delay + Escape).
      this._root.setAttribute("data-enhanced", "true");
    },

    enter() {
      this._hovering = true;
      this._intent.open(() => this.show());
    },

    leave() {
      this._hovering = false;
      this._scheduleClose();
    },

    focusShow() {
      this._focused = true;
      this._intent.now(() => this.show());
    },

    focusHide(event) {
      if (event && this._root.contains(event.relatedTarget)) return;
      this._focused = false;
      this._scheduleClose();
    },

    _scheduleClose() {
      if (this._hovering || this._focused) return;
      this._intent.close(() => this.hide());
    },

    show() {
      if (this.open) return;
      this.open = true;
      this._root.setAttribute("data-state", "open");
      if (this._trigger && this._content) {
        this._anchor = createAnchor(this._trigger, this._content, {
          side: "top",
          align: "center",
        });
      }
      // A tooltip does not trap focus, but it joins the overlay stack so Escape
      // (topmost) and an outside press dismiss it through the shared mechanic.
      this._dismisser = createDismisser(this._root, () => {
        this._hovering = false;
        this._focused = false;
        this.hide();
      });
      this.$nextTick(() => {
        if (this._anchor) this._anchor.activate();
        this._dismisser.activate();
        this.$dispatch("goth:open");
      });
    },

    hide() {
      if (!this.open) return;
      this.open = false;
      this._intent.cancel();
      this._root.setAttribute("data-state", "closed");
      if (this._dismisser) {
        this._dismisser.deactivate();
        this._dismisser = null;
      }
      if (this._anchor) {
        this._anchor.deactivate();
        this._anchor = null;
      }
      this.$dispatch("goth:close");
    },
  };
}
