// gothResizable controller (GOTH-6.3; §8 eleventh controller, ratified by the
// 2026-07-18 gate-b-review.md addendum).
//
// Backs Resizable (P61). The server owns the default split (the root's
// data-default-size + external CSS render the initial geometry through the
// --goth-resize-basis custom property with NO JavaScript). This controller adds the
// live resize: pointer drag (with pointer capture) and APG window-splitter keyboard
// input (Arrow keys, Home, End), clamped to the handle's aria-valuemin/valuemax
// bounds. It writes the new geometry through the controller-owned CSSOM custom
// property `--goth-resize-basis` on the root — never a server-rendered style
// attribute (README §4 frozen invariant) — and keeps the handle's aria-valuenow in
// sync. Horizontal arrow direction is RTL-aware per the roving.js precedent.
//
// It holds no domain store: the numeric split percentage is local interaction state
// only; the server owns the authoritative default.

// keyStep: percentage moved per Arrow keypress (APG small increment).
const keyStep = 5;

function isRTL(el) {
  return (
    typeof getComputedStyle === "function" &&
    getComputedStyle(el).direction === "rtl"
  );
}

function toInt(v, fallback) {
  const n = parseInt(v, 10);
  return Number.isFinite(n) ? n : fallback;
}

export default function gothResizable() {
  return {
    _root: null,
    _handle: null,
    _vertical: false,
    _basis: 50,
    _min: 10,
    _max: 90,
    _dragging: false,
    _startPos: 0,
    _startBasis: 50,
    _pointerId: null,

    init() {
      // Cache the root at init; a method invoked from a descendant's handler sees
      // $el bound to that descendant (the GOTH-4.1 descendant-$el discipline).
      this._root = this.$el;
      this._handle = this._root.querySelector('[data-slot="resizable-handle"]');
      if (!this._handle) return;

      this._vertical = this._root.getAttribute("data-orientation") === "vertical";
      this._min = toInt(this._handle.getAttribute("aria-valuemin"), 10);
      this._max = toInt(this._handle.getAttribute("aria-valuemax"), 90);
      this._basis = this._clamp(
        toInt(this._handle.getAttribute("aria-valuenow"), 50),
      );

      // Once enhanced the controller owns the geometry: sync the custom property to
      // aria-valuenow so JS and the no-JS bucket CSS agree even at odd granularities.
      this._root.setAttribute("data-enhanced", "true");
      this._apply();

      this._onPointerDown = (e) => this._pointerDown(e);
      this._onPointerMove = (e) => this._pointerMove(e);
      this._onPointerUp = (e) => this._pointerUp(e);
      this._onKeyDown = (e) => this._keyDown(e);

      this._handle.addEventListener("pointerdown", this._onPointerDown);
      this._handle.addEventListener("pointermove", this._onPointerMove);
      this._handle.addEventListener("pointerup", this._onPointerUp);
      this._handle.addEventListener("keydown", this._onKeyDown);
    },

    _clamp(v) {
      return Math.max(this._min, Math.min(this._max, v));
    },

    // _apply writes the current split through the CSSOM custom property (never a
    // server-rendered style attribute) and mirrors aria-valuenow for AT.
    _apply() {
      this._root.style.setProperty("--goth-resize-basis", this._basis + "%");
      this._handle.setAttribute("aria-valuenow", String(Math.round(this._basis)));
    },

    _setBasis(v) {
      const next = this._clamp(v);
      if (next === this._basis) return;
      this._basis = next;
      this._apply();
      this.$dispatch("goth:change", { size: Math.round(this._basis) });
    },

    _containerSize() {
      return this._vertical ? this._root.clientHeight : this._root.clientWidth;
    },

    _pointerDown(e) {
      this._dragging = true;
      this._pointerId = e.pointerId;
      if (this._handle.setPointerCapture) {
        this._handle.setPointerCapture(e.pointerId);
      }
      this._startPos = this._vertical ? e.clientY : e.clientX;
      this._startBasis = this._basis;
      e.preventDefault();
    },

    _pointerMove(e) {
      if (!this._dragging) return;
      const pos = this._vertical ? e.clientY : e.clientX;
      let deltaPx = pos - this._startPos;
      // RTL flips the horizontal axis: the primary pane sits on the inline-end
      // (right) side, so a rightward drag shrinks it.
      if (!this._vertical && isRTL(this._root)) deltaPx = -deltaPx;
      const size = this._containerSize();
      if (size <= 0) return;
      this._setBasis(this._startBasis + (deltaPx / size) * 100);
    },

    _pointerUp(e) {
      if (!this._dragging) return;
      this._dragging = false;
      if (this._handle.releasePointerCapture && this._pointerId !== null) {
        try {
          this._handle.releasePointerCapture(this._pointerId);
        } catch (_) {
          /* capture already released */
        }
      }
      this._pointerId = null;
    },

    _keyDown(e) {
      const rtl = !this._vertical && isRTL(this._root);
      let handled = true;
      switch (e.key) {
        case "ArrowRight":
          // Move the separator visually right; in RTL the primary pane is on the
          // right, so the same visual move shrinks it.
          if (this._vertical) handled = false;
          else this._setBasis(this._basis + (rtl ? -keyStep : keyStep));
          break;
        case "ArrowLeft":
          if (this._vertical) handled = false;
          else this._setBasis(this._basis + (rtl ? keyStep : -keyStep));
          break;
        case "ArrowDown":
          if (this._vertical) this._setBasis(this._basis + keyStep);
          else handled = false;
          break;
        case "ArrowUp":
          if (this._vertical) this._setBasis(this._basis - keyStep);
          else handled = false;
          break;
        case "Home":
          this._setBasis(this._min);
          break;
        case "End":
          this._setBasis(this._max);
          break;
        default:
          handled = false;
      }
      if (handled) e.preventDefault();
    },
  };
}
