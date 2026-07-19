// gothRovingFocus controller (GOTH-1.4; grid mode GOTH-5.1).
//
// A thin controller exposing the shared roving-tabindex mechanics to any group
// primitive (toolbar, radio-like group, calendar grid). Items are discovered
// through data-slot="item" or the data-roving-item marker. When the root sets
// data-orientation="grid" the 2D grid mechanics drive APG arrow/Home/End
// navigation across cells (Calendar); otherwise the linear roving mechanics drive
// arrow/Home/End along data-orientation. Bind the group's keydown to onKeydown.

import { createRovingFocus, createGridRoving } from "../mechanics/roving.js";

export default function gothRovingFocus() {
  return {
    _roving: null,

    init() {
      const root = this.$el;
      const items = Array.from(
        root.querySelectorAll('[data-slot="item"], [data-roving-item]'),
      );
      if ((root.getAttribute("data-orientation") || "") === "grid") {
        this._roving = createGridRoving(items);
      } else {
        const orientation = root.getAttribute("data-orientation") || "vertical";
        this._roving = createRovingFocus(items, { orientation });
      }
      this._roving.init();
    },

    onKeydown(event) {
      if (this._roving) this._roving.onKeydown(event);
    },
  };
}
