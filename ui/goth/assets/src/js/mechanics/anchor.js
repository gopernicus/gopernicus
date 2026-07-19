// Shared anchored-placement mechanics (GOTH-4.1).
//
// Popover/menu/tooltip/combobox content is positioned relative to a trigger with
// viewport-collision flipping. To honor the no-inline-style invariant (README §4
// invariant a) the placement is expressed as:
//
//   - data-side / data-align attributes for the RESOLVED semantic placement,
//     which external component CSS keys off; and
//   - the numeric offsets --goth-anchor-top / --goth-anchor-left set through the
//     CSSOM (element.style.setProperty). CSSOM style mutations are exempt from CSP
//     style-src, so no 'unsafe-inline' is required and no server-rendered inline
//     style attribute is emitted. The floating element's CSS is position: fixed;
//     top: var(--goth-anchor-top); left: var(--goth-anchor-left).
//
// The primitive markup carries NO inline style; only the runtime writes the two
// custom properties, and only for a floating element it manages.

const OPPOSITE = { top: "bottom", bottom: "top", left: "right", right: "left" };

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

// createAnchor positions floatingEl against anchorEl. options: side
// ("top"|"right"|"bottom"|"left", default "bottom"), align ("start"|"center"|
// "end", default "start"), gap (px between anchor and floating, default 6).
export function createAnchor(anchorEl, floatingEl, options = {}) {
  const preferredSide = OPPOSITE[options.side] ? options.side : "bottom";
  const align = ["start", "center", "end"].includes(options.align) ? options.align : "start";
  const gap = typeof options.gap === "number" ? options.gap : 6;

  function resolveSide(anchor, float, vw, vh) {
    if (preferredSide === "bottom" && anchor.bottom + gap + float.height > vh && anchor.top - gap - float.height >= 0) {
      return "top";
    }
    if (preferredSide === "top" && anchor.top - gap - float.height < 0 && anchor.bottom + gap + float.height <= vh) {
      return "bottom";
    }
    if (preferredSide === "right" && anchor.right + gap + float.width > vw && anchor.left - gap - float.width >= 0) {
      return "left";
    }
    if (preferredSide === "left" && anchor.left - gap - float.width < 0 && anchor.right + gap + float.width <= vw) {
      return "right";
    }
    return preferredSide;
  }

  function alignedCross(start, anchorSize, floatSize) {
    if (align === "center") return start + anchorSize / 2 - floatSize / 2;
    if (align === "end") return start + anchorSize - floatSize;
    return start;
  }

  function update() {
    const anchor = anchorEl.getBoundingClientRect();
    const float = floatingEl.getBoundingClientRect();
    const vw = document.documentElement.clientWidth;
    const vh = document.documentElement.clientHeight;
    const side = resolveSide(anchor, float, vw, vh);

    let top;
    let left;
    if (side === "bottom" || side === "top") {
      top = side === "bottom" ? anchor.bottom + gap : anchor.top - gap - float.height;
      left = alignedCross(anchor.left, anchor.width, float.width);
    } else {
      left = side === "right" ? anchor.right + gap : anchor.left - gap - float.width;
      top = alignedCross(anchor.top, anchor.height, float.height);
    }

    // Clamp inside the viewport so a collision never pushes content off-screen.
    left = clamp(left, 0, Math.max(0, vw - float.width));
    top = clamp(top, 0, Math.max(0, vh - float.height));

    floatingEl.style.setProperty("--goth-anchor-top", Math.round(top) + "px");
    floatingEl.style.setProperty("--goth-anchor-left", Math.round(left) + "px");
    floatingEl.setAttribute("data-side", side);
    floatingEl.setAttribute("data-align", align);
  }

  function onViewportChange() {
    update();
  }

  return {
    update,
    activate() {
      update();
      window.addEventListener("scroll", onViewportChange, true);
      window.addEventListener("resize", onViewportChange);
    },
    deactivate() {
      window.removeEventListener("scroll", onViewportChange, true);
      window.removeEventListener("resize", onViewportChange);
      floatingEl.style.removeProperty("--goth-anchor-top");
      floatingEl.style.removeProperty("--goth-anchor-left");
    },
  };
}
