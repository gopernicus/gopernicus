// gothCollapse controller (GOTH-1.4; native-<details> model GOTH-3.1).
//
// Enhances a native <details> disclosure (Accordion item / Collapsible). The
// native element owns disclosure, keyboard (Enter/Space on the summary), focus,
// and — for a shared name= group — single-open exclusivity, so the primitive works
// with NO JavaScript. This controller is additive: it mirrors the native open
// state onto data-state (the animation/style hook) and dispatches goth:open /
// goth:close so a host can react. It never toggles visibility itself.

export default function gothCollapse() {
  return {
    _root: null,
    _onToggle: null,

    init() {
      // Cache the controller root at init (where $el is the x-data <details>). A
      // native "toggle" event bubbles from the element, so listen on the cached
      // root rather than a descendant that a later handler might rebind $el to.
      this._root = this.$el;
      this._reflect();
      this._onToggle = () => {
        this._reflect();
        this.$dispatch(this._root.open ? "goth:open" : "goth:close");
      };
      this._root.addEventListener("toggle", this._onToggle);
    },

    _reflect() {
      this._root.setAttribute("data-state", this._root.open ? "open" : "closed");
    },
  };
}
