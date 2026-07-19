// gothHoverCard controller (GOTH-4.3; added to the frozen §8 set by the recorded
// owner reopen — see gate-b-review.md 2026-07-17 addendum).
//
// Enhances a Hover Card: a supplementary, non-critical floating panel opened by
// hovering or focusing its trigger. Its no-JS baseline is the same pure CSS
// hover/focus-within reveal as Tooltip (content always in the DOM). This
// controller composes the frozen GOTH-4.1 mechanics — anchored placement
// (anchor.js) + nested-aware Escape/outside dismissal (dismiss.js/
// overlay-stack.js) — plus the shared hover-intent timing (hover-intent.js). The
// close delay bridges the gap between trigger and panel so a pointer can travel
// into the (possibly interactive) content without dismissing it. Focus opens
// immediately; Escape, blur outside the card, or an outside press (touch-safe on
// pointer devices with no hover) dismisses it. It does NOT trap focus and does not
// move focus into the panel — the card is supplementary, not a dialog. It marks
// the root data-enhanced so the CSS follows the controller-owned data-state.
// Dispatches goth:open / goth:close.

import { createAnchor } from "../mechanics/anchor.js";
import { createDismisser } from "../mechanics/dismiss.js";
import { createHoverIntent } from "../mechanics/hover-intent.js";

export default function gothHoverCard() {
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
      this._root = this.$el;
      this._trigger = this._root.querySelector('[data-slot="trigger"]');
      this._content = this._root.querySelector('[data-slot="hovercard-content"]');
      this._intent = createHoverIntent({ openDelay: 240, closeDelay: 240 });
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
          side: "bottom",
          align: "center",
        });
      }
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
