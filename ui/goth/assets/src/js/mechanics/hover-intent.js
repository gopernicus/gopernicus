// Shared hover-intent timing mechanics (GOTH-4.3).
//
// Hover-opened overlays (Tooltip P48, Hover Card P42) must not open/close on
// every incidental pointer crossing: an open delay filters accidental hovers and
// a close delay bridges the gap between the trigger and its floating content so a
// pointer travelling from one to the other does not dismiss it. One place owns the
// timer bookkeeping so gothTooltip and gothHoverCard never fork slightly different
// intent behavior (README §8 shared families). Focus-driven open/close is
// immediate (accessibility: a keyboard user gets no artificial delay) via now().

// createHoverIntent returns an intent scheduler. openDelay/closeDelay are the
// pointer debounce windows in ms.
export function createHoverIntent({ openDelay = 180, closeDelay = 140 } = {}) {
  let timer = null;

  function cancel() {
    if (timer) {
      window.clearTimeout(timer);
      timer = null;
    }
  }

  return {
    // open runs fn after openDelay unless cancelled first.
    open(fn) {
      cancel();
      timer = window.setTimeout(fn, openDelay);
    },
    // close runs fn after closeDelay unless cancelled first.
    close(fn) {
      cancel();
      timer = window.setTimeout(fn, closeDelay);
    },
    // now cancels any pending timer and runs fn immediately (focus path).
    now(fn) {
      cancel();
      fn();
    },
    cancel,
  };
}
