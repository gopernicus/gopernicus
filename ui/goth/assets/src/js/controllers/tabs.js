// gothTabs controller (GOTH-1.4; roving focus + activation GOTH-3.1).
//
// Enhances a server-rendered tab set: the active panel is already visible and the
// rest hidden (no-JS baseline), so this controller only adds roving focus across
// the tabs (data-slot="tab") and switches the active tab/panel client-side. It
// dispatches goth:change on selection. Manual activation (data-activation="manual")
// moves focus without activating; automatic activation (the default) activates the
// focused tab as the arrow keys move focus.

import { createRovingFocus } from "../mechanics/roving.js";

export default function gothTabs() {
  return {
    _root: null,
    _tabs: [],
    _roving: null,
    _auto: true,

    init() {
      // Cache the root at init: methods invoked from a descendant's x-on handler
      // (activate on a tab, onKeydown on the list) see $el bound to that
      // descendant, so panel/tab queries must run against the cached root, not
      // this.$el (the GOTH-1.5 audit's descendant-$el fix).
      this._root = this.$el;
      this._tabs = Array.from(this._root.querySelectorAll('[data-slot="tab"]'));
      const orientation = this._root.getAttribute("data-orientation") || "horizontal";
      this._auto = (this._root.getAttribute("data-activation") || "automatic") !== "manual";
      this._roving = createRovingFocus(this._tabs, {
        orientation,
        onMove: (el) => {
          if (this._auto) this._activate(el);
        },
      });
      this._roving.init();
    },

    activate(event) {
      this._activate(event.currentTarget);
    },

    _activate(tab) {
      if (!tab) return;
      const value = tab.getAttribute("data-value");
      this._tabs.forEach((t) => {
        const selected = t === tab;
        t.setAttribute("aria-selected", selected ? "true" : "false");
        t.setAttribute("data-state", selected ? "active" : "inactive");
      });
      this._root.querySelectorAll('[data-slot="tab-panel"]').forEach((panel) => {
        panel.hidden = panel.getAttribute("data-value") !== value;
      });
      this.$dispatch("goth:change", { value });
    },

    onKeydown(event) {
      if (this._roving) this._roving.onKeydown(event);
    },
  };
}
