// gothToast controller (GOTH-1.4 foundation, refined in place for GOTH-6.5).
//
// The ONE live-region queue backing Toast (P64) and the Sonner facade (P63). Bound to
// the Toaster region (x-data="gothToast"), a single controller instance owns the whole
// queue of [data-slot="toast"] entries — server-rendered and HTMX-appended alike:
//
//   - announce: each toast is announced ONCE through the shared mechanics/live-region.js
//     (the single announcement channel — the region and the toasts are NOT aria-live, so
//     announcements never double-fire), polite by default or assertive by data-priority.
//   - duration: data-duration ms auto-dismiss; timers PAUSE on region hover/focus and
//     while the page is hidden, and RESUME (with remaining time) afterward.
//   - dedupe: a new toast with the same data-dedup-key collapses the older one.
//   - overflow: data-max caps the visible toasts; the oldest overflow out.
//   - dismiss/action: delegated [data-slot="toast-close"] dismissal (real button →
//     keyboard-activatable) and [data-slot="toast-action"] (button dispatches
//     goth:select + dismisses; link navigates).
//
// It holds no domain store: the server owns notification content; the controller owns
// only presentation/queue state. Entrance/exit animation collapses under reduced motion
// (CSS); the controller removes a dismissed toast immediately when motion is reduced.

import { announce } from "../mechanics/live-region.js";

// removeAfterExit is the fallback delay (ms) before a dismissed toast is removed from the
// DOM when no transitionend fires (matches the exit animation budget in components.css).
const removeAfterExit = 400;

function prefersReducedMotion() {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

export default function gothToast() {
  return {
    _root: null,
    _max: 0,
    _timers: null, // WeakMap<toastEl, {id, remaining, startedAt, duration}>
    _seen: null, // WeakSet<toastEl> — wired-once guard
    _keys: null, // Map<dedupKey, toastEl>
    _onVisibility: null,
    _hovering: false,

    init() {
      // Cache the root at init; a method invoked from a descendant's delegated handler
      // sees $el bound to that descendant (the GOTH-4.1 descendant-$el discipline).
      this._root = this.$el;
      this._max = parseInt(this._root.getAttribute("data-max") || "0", 10) || 0;
      this._timers = new WeakMap();
      this._seen = new WeakSet();
      this._keys = new Map();
      this._root.setAttribute("data-enhanced", "true");

      // Hovering or focusing the stack pauses ALL timers and expands the Sonner stack;
      // leaving/blurring resumes. Pointer events use pointerover/pointerout (which BUBBLE
      // from the toasts) because the region itself is click-through (pointer-events:none),
      // so the non-bubbling pointerenter/leave never reach it. relatedTarget gates the
      // real enter/leave of the whole region.
      this._root.addEventListener("pointerover", () => {
        this._hovering = true;
        this._pauseAll();
      });
      this._root.addEventListener("pointerout", (e) => {
        if (!this._root.contains(e.relatedTarget)) {
          this._hovering = false;
          this._resumeAll();
        }
      });
      this._root.addEventListener("focusin", () => this._pauseAll());
      this._root.addEventListener("focusout", (e) => {
        if (!this._root.contains(e.relatedTarget)) this._resumeAll();
      });

      // Don't burn duration while the tab is backgrounded.
      this._onVisibility = () => {
        if (document.hidden) this._pauseAll();
        else this._resumeAll();
      };
      document.addEventListener("visibilitychange", this._onVisibility);

      // Delegated dismissal + action.
      this._root.addEventListener("click", (e) => this._onClick(e));

      // HTMX-appended toasts: re-scan after each settle (the message-scroller idiom;
      // htmx events bubble to the region).
      this._root.addEventListener("htmx:afterSettle", () => this._scan());

      this._scan();
    },

    destroy() {
      if (this._onVisibility) {
        document.removeEventListener("visibilitychange", this._onVisibility);
      }
    },

    _toasts() {
      return Array.from(this._root.querySelectorAll('[data-slot="toast"]'));
    },

    _open() {
      return this._toasts().filter(
        (t) => t.getAttribute("data-state") !== "closed",
      );
    },

    _scan() {
      for (const t of this._toasts()) {
        if (!this._seen.has(t)) this._wire(t);
      }
      this._enforceOverflow();
      this._reindex();
    },

    _wire(t) {
      this._seen.add(t);
      if (t.getAttribute("data-state") === "closed") return;
      t.setAttribute("data-state", "open");

      // Dedup: an identical key collapses the older toast.
      const key = t.getAttribute("data-dedup-key");
      if (key) {
        const prev = this._keys.get(key);
        if (prev && prev !== t && prev.isConnected) this._remove(prev);
        this._keys.set(key, t);
      }

      // Announce ONCE through the shared live region, polite/assertive by priority.
      if (!t.hasAttribute("data-goth-announced")) {
        t.setAttribute("data-goth-announced", "true");
        const priority =
          t.getAttribute("data-priority") === "assertive"
            ? "assertive"
            : "polite";
        announce(this._message(t), priority);
      }

      // Auto-dismiss timer (data-duration ms; 0 = persistent). If the page is hidden at
      // wire time, record the remaining time so the next resume starts it.
      const duration = parseInt(t.getAttribute("data-duration") || "0", 10);
      if (duration > 0) {
        if (document.hidden) {
          this._timers.set(t, { id: null, remaining: duration, duration });
        } else {
          this._start(t, duration);
        }
      }
    },

    _message(t) {
      const title = t.querySelector('[data-slot="toast-title"]');
      const desc = t.querySelector('[data-slot="toast-description"]');
      const parts = [];
      if (title) parts.push((title.textContent || "").trim());
      if (desc) parts.push((desc.textContent || "").trim());
      const msg = parts.filter(Boolean).join(". ");
      return msg || (t.textContent || "").trim();
    },

    _start(t, duration) {
      const rec = { id: null, duration, remaining: duration, startedAt: Date.now() };
      rec.id = window.setTimeout(() => this._dismiss(t), duration);
      this._timers.set(t, rec);
    },

    _clearTimer(t) {
      const rec = this._timers.get(t);
      if (rec && rec.id != null) window.clearTimeout(rec.id);
      this._timers.delete(t);
    },

    _pause(t) {
      const rec = this._timers.get(t);
      if (!rec || rec.id == null) return;
      window.clearTimeout(rec.id);
      rec.remaining = Math.max(0, rec.remaining - (Date.now() - rec.startedAt));
      rec.id = null;
    },

    _resume(t) {
      const rec = this._timers.get(t);
      if (!rec || rec.id != null || rec.remaining <= 0) return;
      rec.startedAt = Date.now();
      rec.id = window.setTimeout(() => this._dismiss(t), rec.remaining);
    },

    _pauseAll() {
      this._root.setAttribute("data-paused", "true");
      this._root.setAttribute("data-expanded", "true");
      for (const t of this._open()) this._pause(t);
    },

    _resumeAll() {
      // Keep paused while any hover/focus is still active or the page is hidden.
      if (document.hidden) return;
      if (this._hovering) return;
      if (this._root.contains(document.activeElement)) return;
      this._root.removeAttribute("data-paused");
      this._root.removeAttribute("data-expanded");
      for (const t of this._open()) this._resume(t);
    },

    _enforceOverflow() {
      if (this._max <= 0) return;
      const open = this._open();
      const excess = open.length - this._max;
      // Remove the oldest (front of the DOM order) first.
      for (let i = 0; i < excess; i++) this._remove(open[i]);
    },

    _reindex() {
      const open = this._open();
      // Newest is appended last (beforeend); index 0 marks the frontmost/newest toast.
      for (let i = 0; i < open.length; i++) {
        open[open.length - 1 - i].setAttribute("data-index", String(i));
      }
      this._root.setAttribute("data-count", String(open.length));
    },

    _remove(t) {
      this._clearTimer(t);
      const key = t.getAttribute("data-dedup-key");
      if (key && this._keys.get(key) === t) this._keys.delete(key);
      if (t.parentNode) t.parentNode.removeChild(t);
      this._reindex();
    },

    _dismiss(t) {
      if (t.getAttribute("data-state") === "closed") return;
      this._clearTimer(t);
      t.setAttribute("data-state", "closed");
      this.$dispatch("goth:close", { id: t.id || "" });
      if (prefersReducedMotion()) {
        this._remove(t);
        return;
      }
      let removed = false;
      const finish = () => {
        if (removed) return;
        removed = true;
        t.removeEventListener("transitionend", finish);
        this._remove(t);
      };
      t.addEventListener("transitionend", finish);
      window.setTimeout(finish, removeAfterExit);
      this._reindex();
    },

    _onClick(e) {
      const close =
        e.target.closest && e.target.closest('[data-slot="toast-close"]');
      if (close) {
        const toast = close.closest('[data-slot="toast"]');
        if (toast) {
          e.preventDefault();
          this._dismiss(toast);
        }
        return;
      }
      const action =
        e.target.closest && e.target.closest('[data-slot="toast-action"]');
      if (action) {
        const toast = action.closest('[data-slot="toast"]');
        this.$dispatch("goth:select", { id: toast ? toast.id : "" });
        // A button action dismisses after acting; a link proceeds to navigate.
        if (action.tagName !== "A" && toast) this._dismiss(toast);
      }
    },
  };
}
