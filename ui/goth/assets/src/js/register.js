// GOTH controller registration (GOTH-1.4).
//
// Binds every named GOTH controller against the given Alpine instance using the
// CSP-safe data-component form (Alpine.data), so no inline expression is ever
// evaluated and no 'unsafe-eval' is required. Controllers are named by behavior
// with the frozen goth- prefix and registered ONCE here (README §8). A primitive
// binds one with x-data="gothX"; that binding attribute is frozen public surface.

import gothDialog from "./controllers/dialog.js";
import gothCollapse from "./controllers/collapse.js";
import gothRovingFocus from "./controllers/roving-focus.js";
import gothMenu from "./controllers/menu.js";
import gothTabs from "./controllers/tabs.js";
import gothCombobox from "./controllers/combobox.js";
import gothToast from "./controllers/toast.js";
import gothTooltip from "./controllers/tooltip.js";
import gothHoverCard from "./controllers/hover-card.js";
import gothMessageScroller from "./controllers/message-scroller.js";
import gothResizable from "./controllers/resizable.js";

export default function registerControllers(Alpine) {
  Alpine.data("gothDialog", gothDialog);
  Alpine.data("gothCollapse", gothCollapse);
  Alpine.data("gothRovingFocus", gothRovingFocus);
  Alpine.data("gothMenu", gothMenu);
  Alpine.data("gothTabs", gothTabs);
  Alpine.data("gothCombobox", gothCombobox);
  Alpine.data("gothToast", gothToast);
  Alpine.data("gothTooltip", gothTooltip);
  Alpine.data("gothHoverCard", gothHoverCard);
  Alpine.data("gothMessageScroller", gothMessageScroller);
  Alpine.data("gothResizable", gothResizable);
}
