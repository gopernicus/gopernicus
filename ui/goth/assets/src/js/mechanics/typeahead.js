// Shared typeahead mechanics (GOTH-1.4).
//
// Menu/select/listbox controllers share ONE typeahead buffer so type-to-focus
// behavior is identical everywhere (README §8).

// createTypeahead returns a function that accumulates printable keys into a
// buffer, resets the buffer after timeoutMs of inactivity, and calls match with
// the current buffer so the caller can move focus to the first matching item.
export function createTypeahead(match, timeoutMs = 500) {
  let buffer = "";
  let timer = null;

  return function (key) {
    if (typeof key !== "string" || key.length !== 1) return;
    buffer += key.toLowerCase();
    if (timer) window.clearTimeout(timer);
    timer = window.setTimeout(() => {
      buffer = "";
    }, timeoutMs);
    match(buffer);
  };
}
