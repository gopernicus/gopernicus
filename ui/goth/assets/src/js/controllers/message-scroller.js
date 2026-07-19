// gothMessageScroller controller (GOTH-6.2; §8 tenth controller, ratified by the
// 2026-07-18 gate-b-review.md addendum).
//
// Backs Message Scroller (P60). A thin controller composing the frozen GOTH-4.1
// mechanics discipline (cache _root at init; discover parts via data-slot) and the
// shared live-region mechanic. It never holds a domain store: the server owns the
// transcript and pagination; the controller only manages scroll position and the
// scroll/unread presentation state.
//
// Behaviors:
//   - live-edge following: while the reader is at the live edge (bottom), new content
//     appended into the content region sticks to the bottom; when the reader scrolls
//     up, following stops.
//   - history-prepend-without-jump: across an HTMX afterbegin (prepend) swap driven by
//     the data-goth-history trigger, the viewport is re-anchored so the same message
//     stays under the reader's eye (native overflow-anchor is the no-JS baseline).
//   - jump-to-message: a data-goth-jump anchor scrolls to #id and focuses the target so
//     a keyboard/AT reader lands on the jumped message (reader intent preserved).
//   - unread/scroll-state exposure: data-at-edge / data-unread on the root, a revealed
//     status affordance with a live count, documented goth:change events, and a polite
//     live-region summary when messages arrive off-edge.

import { announce } from "../mechanics/live-region.js";

// atEdgeThreshold: pixels from the bottom still counted as "at the live edge".
const atEdgeThreshold = 24;

export default function gothMessageScroller() {
  return {
    _root: null,
    _viewport: null,
    _content: null,
    _status: null,
    _statusLabel: null,
    _unread: 0,
    _atEdge: true,
    // HTMX pre-swap snapshot for prepend anchoring / append detection.
    _snap: null,

    init() {
      // Cache the root at init; a method invoked from a descendant's handler sees $el
      // bound to that descendant (the GOTH-4.1 descendant-$el fix).
      this._root = this.$el;
      this._viewport = this._root.querySelector(
        '[data-slot="message-scroller-viewport"]',
      );
      this._content = this._root.querySelector(
        '[data-slot="message-scroller-content"]',
      );
      this._status = this._root.querySelector(
        '[data-slot="message-scroller-status"]',
      );
      this._statusLabel = this._root.querySelector(
        '[data-slot="message-scroller-status-label"]',
      );
      if (!this._viewport) return;

      // The controller now owns scroll anchoring; disable native overflow-anchor (the
      // no-JS baseline) so it does not double-correct the JS prepend restore — and so
      // engines without overflow-anchor (WebKit) get the same behavior via JS.
      this._root.setAttribute("data-enhanced", "true");

      this._onScroll = () => this._recomputeEdge();
      this._viewport.addEventListener("scroll", this._onScroll, { passive: true });

      // HTMX prepend/append hooks live on the content (the swap target). The scroll
      // correction runs on afterSettle (after HTMX's own settle-phase scrolling) so it
      // is authoritative; the snapshot is taken on beforeSwap (pre-DOM-change).
      if (this._content) {
        this._content.addEventListener("htmx:beforeSwap", () => this._beforeSwap());
        this._content.addEventListener("htmx:afterSettle", () => this._afterSettle());
      }

      // Delegated jump-to-message.
      this._root.addEventListener("click", (e) => this._onClick(e));

      // Saved-thread initial position: open at the live edge (latest), unless the URL
      // hash already targets a message inside this transcript.
      this.$nextTick(() => {
        const hashTarget = this._hashTarget();
        if (hashTarget) {
          this._jumpTo(hashTarget, false);
        } else {
          this._scrollToBottom();
        }
        this._recomputeEdge();
      });
    },

    _hashTarget() {
      const hash = window.location.hash;
      if (!hash || hash.length < 2 || !this._content) return null;
      const el = this._content.querySelector(hash);
      return el || null;
    },

    _distanceFromBottom() {
      const v = this._viewport;
      return v.scrollHeight - v.scrollTop - v.clientHeight;
    },

    _scrollToBottom() {
      this._viewport.scrollTop = this._viewport.scrollHeight;
    },

    _recomputeEdge() {
      const atEdge = this._distanceFromBottom() <= atEdgeThreshold;
      const changed = atEdge !== this._atEdge;
      this._atEdge = atEdge;
      this._root.setAttribute("data-at-edge", atEdge ? "true" : "false");
      // role=log is polite while following; off-edge we mute per-message
      // announcements and speak an unread summary instead (avoids double speech).
      if (this._viewport) {
        this._viewport.setAttribute("aria-live", atEdge ? "polite" : "off");
      }
      if (atEdge && this._unread !== 0) {
        this._setUnread(0);
      }
      if (changed) this._emitChange();
    },

    _setUnread(n) {
      this._unread = n < 0 ? 0 : n;
      this._root.setAttribute("data-unread", String(this._unread));
      if (this._statusLabel && this._unread > 0) {
        const noun = this._unread === 1 ? "new message" : "new messages";
        this._statusLabel.textContent = this._unread + " " + noun;
      }
      this._emitChange();
    },

    _emitChange() {
      this.$dispatch("goth:change", {
        atEdge: this._atEdge,
        unread: this._unread,
      });
    },

    _snapshot() {
      const v = this._viewport;
      const c = this._content;
      return {
        height: v.scrollHeight,
        top: v.scrollTop,
        atEdge: this._atEdge,
        count: c ? c.children.length : 0,
        first: c ? c.firstElementChild : null,
        last: c ? c.lastElementChild : null,
      };
    },

    _beforeSwap() {
      this._snap = this._snapshot();
    },

    _afterSettle() {
      const v = this._viewport;
      const c = this._content;
      const snap = this._snap || this._snapshot();
      this._snap = null;
      if (!c) return;
      const delta = v.scrollHeight - snap.height;
      const added = c.children.length - snap.count;
      if (added <= 0) {
        this._recomputeEdge();
        return;
      }
      // Detect the swap position by whether the leading/trailing child changed, so we
      // never depend on which element initiated the HTMX request.
      const prepended = c.firstElementChild !== snap.first;
      const appended = c.lastElementChild !== snap.last;

      if (prepended && !appended) {
        // History prepend: keep the same message under the reader by restoring offset.
        v.scrollTop = snap.top + delta;
        this._recomputeEdge();
        return;
      }

      // Append (new incoming messages).
      if (snap.atEdge) {
        this._scrollToBottom();
        this._recomputeEdge();
      } else {
        this._setUnread(this._unread + added);
        const noun = added === 1 ? "new message" : "new messages";
        announce(added + " " + noun + " below", "polite");
        this._recomputeEdge();
      }
    },

    _onClick(e) {
      const jump = e.target.closest && e.target.closest("[data-goth-jump]");
      if (jump && this._content) {
        const hash = jump.getAttribute("href") || "";
        if (hash.startsWith("#")) {
          const target = this._content.querySelector(hash);
          if (target) {
            e.preventDefault();
            this._jumpTo(target, true);
          }
        }
        return;
      }
      const status = e.target.closest && e.target.closest("[data-goth-status]");
      if (status) {
        e.preventDefault();
        this._scrollToBottom();
        this._recomputeEdge();
      }
    },

    // _jumpTo scrolls a message into view and, when focus is requested, moves focus to
    // it so a keyboard/AT reader lands on the jumped message. The target is made
    // programmatically focusable without entering the tab order.
    _jumpTo(target, focus) {
      if (typeof target.scrollIntoView === "function") {
        target.scrollIntoView({ block: "center" });
      }
      if (focus) {
        if (!target.hasAttribute("tabindex")) {
          target.setAttribute("tabindex", "-1");
        }
        target.focus({ preventScroll: true });
      }
      this._root.setAttribute("data-jumped", target.id || "true");
      this.$dispatch("goth:select", { id: target.id || "" });
      this._recomputeEdge();
    },
  };
}
