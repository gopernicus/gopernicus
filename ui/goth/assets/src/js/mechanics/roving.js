// Shared roving-tabindex mechanics (GOTH-1.4).
//
// Menus, tabs, toggle groups, and radio-like groups share ONE roving-focus
// implementation (arrow/Home/End traversal, single tab stop) so keyboard
// navigation is identical everywhere (README §8).

// createRovingFocus manages a single tab stop across items and moves focus with
// the arrow keys for the given orientation ("vertical" or "horizontal"), plus
// Home/End. It wraps around the ends.
export function createRovingFocus(items, options = {}) {
  const orientation = options.orientation === "horizontal" ? "horizontal" : "vertical";
  let index = 0;

  function setTabstops() {
    items.forEach((el, i) => el.setAttribute("tabindex", i === index ? "0" : "-1"));
  }

  function setActive(target) {
    if (items.length === 0) return;
    index = ((target % items.length) + items.length) % items.length;
    setTabstops();
    items[index].focus();
    // Optional hook: callers (e.g. gothTabs automatic activation) react to a
    // keyboard-driven focus move without re-implementing traversal.
    if (typeof options.onMove === "function") options.onMove(items[index]);
  }

  // Horizontal arrow keys follow the document direction: in RTL the visually-next
  // item is to the LEFT, so ArrowLeft advances and ArrowRight retreats (Radix/APG
  // RTL parity). Direction is resolved per keydown from the focused item so a
  // direction change never leaves stale bindings. Vertical is unaffected by dir.
  function resolveKeys() {
    if (orientation !== "horizontal") {
      return { next: "ArrowDown", prev: "ArrowUp" };
    }
    const el = items[index] || items[0];
    const rtl = el && typeof getComputedStyle === "function"
      && getComputedStyle(el).direction === "rtl";
    return rtl
      ? { next: "ArrowLeft", prev: "ArrowRight" }
      : { next: "ArrowRight", prev: "ArrowLeft" };
  }

  function onKeydown(event) {
    const { next: nextKey, prev: prevKey } = resolveKeys();
    if (event.key === nextKey) {
      event.preventDefault();
      setActive(index + 1);
    } else if (event.key === prevKey) {
      event.preventDefault();
      setActive(index - 1);
    } else if (event.key === "Home") {
      event.preventDefault();
      setActive(0);
    } else if (event.key === "End") {
      event.preventDefault();
      setActive(items.length - 1);
    }
  }

  return {
    activeIndex: () => index,
    setActive,
    onKeydown,
    init() {
      setTabstops();
      items.forEach((el, i) =>
        el.addEventListener("focus", () => {
          index = i;
          setTabstops();
        }),
      );
    },
  };
}

// createGridRoving manages a single tab stop across the cells of a 2D grid (e.g. a
// Calendar month grid) and moves focus per the APG grid pattern: horizontal arrows
// step to the previous/next focusable cell (direction-aware for RTL), vertical
// arrows move to the same column in the adjacent row, and Home/End jump to the
// first/last focusable cell of the current row. Each item carries its coordinates
// in data-col / data-row, so the grid tolerates holes (disabled/outside-month cells
// are not focusable items). Enter/Space activation is left to the native control.
export function createGridRoving(items) {
  let index = 0;

  const col = (el) => parseInt(el.getAttribute("data-col") || "0", 10);
  const row = (el) => parseInt(el.getAttribute("data-row") || "0", 10);

  function setTabstops() {
    items.forEach((el, i) => el.setAttribute("tabindex", i === index ? "0" : "-1"));
  }

  function setActive(target) {
    if (items.length === 0) return;
    index = ((target % items.length) + items.length) % items.length;
    setTabstops();
    items[index].focus();
  }

  function moveColumn(direction) {
    if (items.length === 0) return;
    const c = col(items[index]);
    if (direction > 0) {
      for (let i = index + 1; i < items.length; i++) {
        if (col(items[i]) === c) return setActive(i);
      }
    } else {
      for (let i = index - 1; i >= 0; i--) {
        if (col(items[i]) === c) return setActive(i);
      }
    }
  }

  function moveRowEdge(toEnd) {
    if (items.length === 0) return;
    const r = row(items[index]);
    let target = index;
    for (let i = 0; i < items.length; i++) {
      if (row(items[i]) !== r) continue;
      target = i;
      if (!toEnd) break;
    }
    setActive(target);
  }

  function resolveHorizontal() {
    const el = items[index] || items[0];
    const rtl = el && typeof getComputedStyle === "function"
      && getComputedStyle(el).direction === "rtl";
    return rtl ? { next: "ArrowLeft", prev: "ArrowRight" } : { next: "ArrowRight", prev: "ArrowLeft" };
  }

  function onKeydown(event) {
    const { next, prev } = resolveHorizontal();
    switch (event.key) {
      case next:
        event.preventDefault();
        setActive(index + 1);
        break;
      case prev:
        event.preventDefault();
        setActive(index - 1);
        break;
      case "ArrowDown":
        event.preventDefault();
        moveColumn(1);
        break;
      case "ArrowUp":
        event.preventDefault();
        moveColumn(-1);
        break;
      case "Home":
        event.preventDefault();
        moveRowEdge(false);
        break;
      case "End":
        event.preventDefault();
        moveRowEdge(true);
        break;
    }
  }

  return {
    activeIndex: () => index,
    setActive,
    onKeydown,
    init() {
      // Respect a server-chosen tab stop (the day rendered with tabindex 0 — the
      // selected/today/first-enabled day) as the starting index so the enhanced
      // and no-JS focus targets match.
      const pre = items.findIndex((el) => el.getAttribute("tabindex") === "0");
      if (pre >= 0) index = pre;
      setTabstops();
      items.forEach((el, i) =>
        el.addEventListener("focus", () => {
          index = i;
          setTabstops();
        }),
      );
    },
  };
}
