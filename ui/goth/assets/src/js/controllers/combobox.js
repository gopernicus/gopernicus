// gothCombobox controller (GOTH-1.4; overlay mechanics GOTH-4.1; command/combobox
// primitives GOTH-5.2).
//
// Backs the Combobox (P50) and Command (P51) primitives from one controller with no
// new §8 name. Two shapes:
//   - popup (Combobox): the listbox opens on focus/typing and dismisses on
//     Escape/outside press through the shared overlay mechanics;
//   - inline (Command, data-inline): the listbox is always visible, no overlay.
//
// In both, the input keeps DOM focus and an active option is tracked with
// aria-activedescendant (APG editable-combobox pattern): the arrow keys move it,
// Enter activates it (a native submit/link — the server owns the round-trip),
// Home/End jump to the ends. Filtering is client-side (hide non-matching options +
// toggle the empty state) or server-owned (data-filter="server": the server
// replaces the options via HTMX and renders its own empty state). Dispatches the
// documented goth:open / goth:close / goth:select events (README §8). The server
// still owns the submitted value.

import { createDismisser } from "../mechanics/dismiss.js";

export default function gothCombobox() {
  return {
    open: false,
    _root: null,
    _input: null,
    _listbox: null,
    _empty: null,
    _inline: false,
    _serverFilter: false,
    _dismisser: null,
    _active: -1,

    init() {
      // Cache the root at init; a method invoked from a descendant's x-on handler
      // sees $el bound to that descendant (the GOTH-4.1 descendant-$el fix).
      this._root = this.$el;
      this._input = this._root.querySelector('[data-slot="input"]');
      this._listbox = this._root.querySelector('[data-slot="listbox"]');
      this._empty = this._root.querySelector('[data-slot="empty"]');
      this._inline = this._root.hasAttribute("data-inline");
      this._serverFilter = this._root.getAttribute("data-filter") === "server";
      // Enhanced: keyboard is activedescendant on the input, so options leave the
      // tab order (one tab stop). No-JS keeps the buttons natively focusable.
      this._demoteOptions();
      if (this._inline) {
        this.open = true;
        this._root.setAttribute("data-state", "open");
        if (this._input) this._input.setAttribute("aria-expanded", "true");
      }
      // Re-index options after an HTMX swap replaces the server-owned option list.
      if (this._listbox) {
        this._listbox.addEventListener("htmx:afterSwap", () => this._afterSwap());
      }
    },

    _options() {
      if (!this._root) return [];
      return Array.from(this._root.querySelectorAll('[data-slot="option"]'));
    },

    _visibleOptions() {
      return this._options().filter(
        (o) =>
          !o.hidden &&
          !o.hasAttribute("disabled") &&
          o.getAttribute("aria-disabled") !== "true",
      );
    },

    _demoteOptions() {
      this._options().forEach((o) => o.setAttribute("tabindex", "-1"));
    },

    _afterSwap() {
      this._demoteOptions();
      this._setActive(-1);
      this._syncEmpty();
    },

    _ensureID(opt, i) {
      if (!opt.id) {
        opt.id = (this._root.id || "goth-combobox") + "-opt-" + i;
      }
      return opt.id;
    },

    onFocus() {
      if (!this._inline) this.show();
    },

    onInput() {
      if (!this._serverFilter) this._clientFilter();
      if (!this._inline && !this.open) this.show();
      this._syncEmpty();
    },

    _clientFilter() {
      const q = (this._input ? this._input.value : "").trim().toLowerCase();
      this._options().forEach((opt) => {
        const label = (opt.textContent || "").trim().toLowerCase();
        opt.hidden = q !== "" && !label.includes(q);
      });
      this._setActive(-1);
    },

    _syncEmpty() {
      // In server-filter mode the server renders the empty state; only toggle it
      // for client filtering.
      if (!this._empty || this._serverFilter) return;
      this._empty.hidden = this._visibleOptions().length > 0;
    },

    show() {
      if (this.open) return;
      this.open = true;
      this._root.setAttribute("data-state", "open");
      if (this._input) this._input.setAttribute("aria-expanded", "true");
      // Escape refocuses the input (it never lost focus anyway); an outside press
      // just closes.
      this._dismisser = createDismisser(this._root, (reason) =>
        this.hide(reason === "escape"),
      );
      this.$nextTick(() => {
        if (this._dismisser) this._dismisser.activate();
        this.$dispatch("goth:open");
      });
    },

    hide(refocus = true) {
      if (!this.open || this._inline) return;
      this.open = false;
      this._root.setAttribute("data-state", "closed");
      if (this._input) {
        this._input.setAttribute("aria-expanded", "false");
        this._input.removeAttribute("aria-activedescendant");
        if (refocus) this._input.focus();
      }
      this._setActive(-1);
      if (this._dismisser) {
        this._dismisser.deactivate();
        this._dismisser = null;
      }
      this.$dispatch("goth:close");
    },

    _setActive(target) {
      this._options().forEach((o) => o.removeAttribute("data-active"));
      const opts = this._visibleOptions();
      if (opts.length === 0 || target < 0) {
        this._active = -1;
        if (this._input) this._input.removeAttribute("aria-activedescendant");
        return;
      }
      this._active = ((target % opts.length) + opts.length) % opts.length;
      const opt = opts[this._active];
      const id = this._ensureID(opt, this._active);
      opt.setAttribute("data-active", "true");
      if (this._input) this._input.setAttribute("aria-activedescendant", id);
      if (typeof opt.scrollIntoView === "function") {
        opt.scrollIntoView({ block: "nearest" });
      }
    },

    _activeOption() {
      const opts = this._visibleOptions();
      if (this._active < 0 || this._active >= opts.length) return null;
      return opts[this._active];
    },

    onKeydown(event) {
      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          if (!this.open && !this._inline) {
            this.show();
            this.$nextTick(() => this._setActive(0));
          } else {
            this._setActive(this._active + 1);
          }
          break;
        case "ArrowUp":
          event.preventDefault();
          if (!this.open && !this._inline) {
            this.show();
            this.$nextTick(() => this._setActive(this._visibleOptions().length - 1));
          } else {
            this._setActive(this._active - 1);
          }
          break;
        case "Home":
          if (this.open || this._inline) {
            event.preventDefault();
            this._setActive(0);
          }
          break;
        case "End":
          if (this.open || this._inline) {
            event.preventDefault();
            this._setActive(this._visibleOptions().length - 1);
          }
          break;
        case "Enter": {
          const opt = this._activeOption();
          if (opt) {
            event.preventDefault();
            opt.click();
          }
          break;
        }
        case "Escape":
          if (this.open && !this._inline) {
            event.preventDefault();
            this.hide(true);
          } else if (this._inline) {
            this.$dispatch("goth:close");
          }
          break;
        case "Tab":
          if (this.open && !this._inline) this.hide(false);
          break;
      }
    },

    select(event) {
      // The option is a native submit button or link: let the browser (no-JS) or
      // HTMX perform the server round-trip. We only report the selection.
      this.$dispatch("goth:select", {
        value: event.currentTarget.getAttribute("data-value"),
      });
    },
  };
}
