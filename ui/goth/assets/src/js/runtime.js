// ui/goth interaction runtime entry (GOTH-1.4).
//
// Bundles the CSP-safe Alpine build (@alpinejs/csp) into the self-hosted
// runtime.js asset and registers the named GOTH controllers + shared
// accessibility mechanics ONCE here (README §8). The CSP build never uses
// new Function/eval, so no 'unsafe-eval' is required anywhere. This asset ships
// only in the Interactive and Full profiles.
import Alpine from "@alpinejs/csp";

import registerControllers from "./register.js";
import { initPopoverAnchoring } from "./mechanics/popover-anchor.js";

registerControllers(Alpine);

// Native-popover (P45) anchoring: a delegated, CSP-safe enhancement — not an
// Alpine controller — that positions an opened goth popover against its invoker.
initPopoverAnchoring();

window.Alpine = Alpine;
Alpine.start();
