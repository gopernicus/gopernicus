// gothMenu controller (GOTH-1.4; overlay mechanics GOTH-4.1; menu primitives
// GOTH-4.4).
//
// Backs the action-menu primitives — Dropdown Menu (P41), Context Menu (P38), and
// Menubar (P43) — from the shared overlay mechanics: anchored placement of the
// menu panel, roving focus + typeahead over its items, submenu hierarchy, and
// nested-aware Escape/outside dismissal — dispatching the documented
// goth:open / goth:close / goth:select events. Feature-specific event names are
// forbidden (README §8). No new controller name is introduced for GOTH-4.4; this
// refines gothMenu's per-primitive semantics (in-phase), adding context-menu
// pointer/keyboard opening and menubar horizontal coordination.
//
// Structure it discovers: an optional data-slot="trigger" button (dropdown/
// menubar) OR a data-slot="context-trigger" region (context menu), a
// data-slot="content" floating panel (falling back to the root when absent), and
// data-slot="item" items within the panel. A data-slot="submenu-root" wrapping a
// data-slot="submenu-trigger" + data-slot="submenu" panel becomes a submenu. A
// menu whose root is inside a data-slot="menubar" coordinates horizontal roving
// and adjacent-menu opening with its siblings.

import { createRovingFocus } from "../mechanics/roving.js";
import { createDismisser } from "../mechanics/dismiss.js";
import { createTypeahead } from "../mechanics/typeahead.js";
import { createAnchor } from "../mechanics/anchor.js";
import { createSubmenu } from "../mechanics/submenu.js";

function isRTL(el) {
  return (
    el &&
    typeof getComputedStyle === "function" &&
    getComputedStyle(el).direction === "rtl"
  );
}

export default function gothMenu() {
  return {
    open: false,
    _root: null,
    _trigger: null,
    _contextTrigger: null,
    _content: null,
    _items: [],
    _roving: null,
    _typeahead: null,
    _dismisser: null,
    _anchor: null,
    _pointAnchor: null,
    _menubar: null,
    _menubarTriggers: [],

    init() {
      // Cache the root at init; a method invoked from a descendant's x-on handler
      // sees $el bound to that descendant (the GOTH-1.5 descendant-$el fix applied
      // here at source per the GOTH-3.4 Phase-4 follow-up).
      this._root = this.$el;
      this._trigger = this._root.querySelector('[data-slot="trigger"]');
      this._contextTrigger = this._root.querySelector('[data-slot="context-trigger"]');
      this._content = this._root.querySelector('[data-slot="content"]') || this._root;
      // Top-level roving items: the menu items AND submenu triggers, excluding
      // anything nested inside a submenu panel (a submenu owns its own roving).
      this._items = Array.from(
        this._content.querySelectorAll('[data-slot="item"],[data-slot="submenu-trigger"]'),
      ).filter((el) => !el.closest('[data-slot="submenu"]'));
      this._roving = createRovingFocus(this._items, { orientation: "vertical" });
      this._typeahead = createTypeahead((buffer) => {
        const match = this._items.find((el) =>
          (el.textContent || "").trim().toLowerCase().startsWith(buffer),
        );
        if (match) this._roving.setActive(this._items.indexOf(match));
      });
      // Wire each submenu once (its own open/close/roving/anchor lifecycle).
      this._root.querySelectorAll('[data-slot="submenu-root"]').forEach((sub) => {
        const trigger = sub.querySelector('[data-slot="submenu-trigger"]');
        const panel = sub.querySelector('[data-slot="submenu"]');
        if (trigger && panel) createSubmenu(sub, trigger, panel);
      });

      // Menubar coordination: a menu inside a data-slot="menubar" shares a single
      // horizontal tab stop across the sibling triggers and opens the adjacent
      // menu when navigating while one is open.
      this._menubar = this._root.closest('[data-slot="menubar"]');
      if (this._menubar && this._trigger) {
        this._menubar._gothMenus = this._menubar._gothMenus || [];
        this._menubar._gothMenus.push(this);
        this._menubarTriggers = Array.from(
          this._menubar.querySelectorAll('[data-slot="trigger"]'),
        );
        const index = this._menubarTriggers.indexOf(this._trigger);
        this._trigger.setAttribute("tabindex", index === 0 ? "0" : "-1");
        this._trigger.addEventListener("focus", () => this._setMenubarTabstop());
      }
    },

    _focusRestoreTarget() {
      return this._trigger || this._contextTrigger;
    },

    show() {
      if (this.open) return;
      this.open = true;
      this._root.setAttribute("data-state", "open");
      this._content.setAttribute("data-state", "open");
      if (this._trigger) this._trigger.setAttribute("aria-expanded", "true");
      this._roving.init();
      // Escape / selection return focus to the trigger; an outside click leaves
      // focus where the pointer landed.
      this._dismisser = createDismisser(this._root, (reason) => this.hide(reason !== "outside"));
      if (this._pointAnchor) {
        // Context menu: anchor to the pointer position via a virtual zero-size rect.
        const point = this._pointAnchor;
        const virtual = {
          getBoundingClientRect: () => ({
            top: point.y,
            bottom: point.y,
            left: point.x,
            right: point.x,
            width: 0,
            height: 0,
          }),
        };
        this._anchor = createAnchor(virtual, this._content, { side: "bottom", align: "start" });
      } else if (this._trigger && this._content !== this._root) {
        this._anchor = createAnchor(this._trigger, this._content, {
          side: "bottom",
          align: "start",
        });
      }
      this.$nextTick(() => {
        if (this._anchor) this._anchor.activate();
        this._dismisser.activate();
        this._roving.setActive(0);
        this.$dispatch("goth:open");
      });
    },

    hide(refocus = true) {
      if (!this.open) return;
      this.open = false;
      this._root.setAttribute("data-state", "closed");
      this._content.setAttribute("data-state", "closed");
      if (this._trigger) this._trigger.setAttribute("aria-expanded", "false");
      if (refocus) {
        const target = this._focusRestoreTarget();
        if (target) target.focus();
      }
      if (this._dismisser) this._dismisser.deactivate();
      if (this._anchor) {
        this._anchor.deactivate();
        this._anchor = null;
      }
      this._pointAnchor = null;
      this.$dispatch("goth:close");
    },

    toggle() {
      if (this.open) {
        this.hide();
      } else {
        this._pointAnchor = null;
        this.show();
      }
    },

    // openContext opens a context menu at the pointer position (right-click /
    // long-press). It suppresses the browser's native menu.
    openContext(event) {
      event.preventDefault();
      this._pointAnchor = { x: event.clientX, y: event.clientY };
      if (this.open) this.hide(false);
      this.show();
    },

    // onContextKeydown opens a context menu from the keyboard: the ContextMenu key
    // or Shift+F10 (APG) or Enter/Space, anchored at the region's box.
    onContextKeydown(event) {
      const opener =
        event.key === "ContextMenu" ||
        (event.shiftKey && event.key === "F10") ||
        event.key === "Enter" ||
        event.key === " ";
      if (!opener || this.open) return;
      event.preventDefault();
      const region = this._contextTrigger || this._root;
      const box = region.getBoundingClientRect();
      this._pointAnchor = { x: box.left + 8, y: box.top + 8 };
      this.show();
    },

    onTriggerKeydown(event) {
      // ArrowDown/ArrowUp/Enter/Space open the menu (Up focuses the last item).
      if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        if (!this.open) {
          this._pointAnchor = null;
          this.show();
        }
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        if (!this.open) {
          this._pointAnchor = null;
          this.show();
          this.$nextTick(() => this._roving.setActive(this._items.length - 1));
        }
        return;
      }
      // Menubar horizontal navigation across the closed triggers.
      if (this._menubar) {
        const rtl = isRTL(this._trigger);
        const nextKey = rtl ? "ArrowLeft" : "ArrowRight";
        const prevKey = rtl ? "ArrowRight" : "ArrowLeft";
        if (event.key === nextKey) {
          event.preventDefault();
          this._menubarMove(1, false);
        } else if (event.key === prevKey) {
          event.preventDefault();
          this._menubarMove(-1, false);
        }
      }
    },

    onKeydown(event) {
      if (!this.open) return;
      // Menubar: ArrowLeft/ArrowRight (RTL-aware) switch to the adjacent menu and
      // open it. Vertical roving ignores these keys, so they are ours to handle.
      if (this._menubar) {
        const rtl = isRTL(this._content);
        const nextKey = rtl ? "ArrowLeft" : "ArrowRight";
        const prevKey = rtl ? "ArrowRight" : "ArrowLeft";
        if (event.key === nextKey) {
          event.preventDefault();
          this._menubarMove(1, true);
          return;
        }
        if (event.key === prevKey) {
          event.preventDefault();
          this._menubarMove(-1, true);
          return;
        }
      }
      this._roving.onKeydown(event);
      if (event.key && event.key.length === 1) this._typeahead(event.key);
    },

    select(event) {
      const value = event.currentTarget.getAttribute("data-value");
      this.$dispatch("goth:select", { value });
      this.hide();
    },

    // _menubarMove moves focus to the adjacent menu in the bar. step is +1/-1 in
    // logical (direction-adjusted) order; reopen opens the target menu when the
    // current one was open (traversal while a menu is displayed).
    _menubarMove(step, reopen) {
      const triggers = this._menubarTriggers;
      const n = triggers.length;
      if (n === 0) return;
      const i = triggers.indexOf(this._trigger);
      const targetTrigger = triggers[((i + step) % n + n) % n];
      const target = (this._menubar._gothMenus || []).find((m) => m._trigger === targetTrigger);
      const wasOpen = this.open;
      if (wasOpen) this.hide(false);
      if (target) {
        target._focusTrigger();
        if (reopen && wasOpen) target.show();
      } else if (targetTrigger) {
        targetTrigger.focus();
      }
    },

    _focusTrigger() {
      this._setMenubarTabstop();
      if (this._trigger) this._trigger.focus();
    },

    _setMenubarTabstop() {
      this._menubarTriggers.forEach((t) =>
        t.setAttribute("tabindex", t === this._trigger ? "0" : "-1"),
      );
    },
  };
}
