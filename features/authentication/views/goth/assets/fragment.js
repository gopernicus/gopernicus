// Externalized fragment-token reader for the authentication reset and magic-link
// landings (design §6.4; ui-goth GOTH-7.2). It replaces the bespoke inline nonced
// page scripts so the auth pages run under a CSP whose script-src is 'self' with no
// inline script. A single element carries the configuration via data-* attributes:
//
//   data-auth-fragment          "hash" (copy the whole fragment) or "token" (parse
//                               the fragment as a query string and read `token`)
//   data-auth-fragment-target   the id of the hidden input to populate
//   data-auth-fragment-submit   "true" to submit the element (a <form>) after
//                               populating (the magic-link auto-redeem)
//
// On every visit it scrubs the fragment from history first (so the token never
// survives in the address bar, a bookmark, or the Referer), then populates the
// target ONLY when a fragment value is present — an error re-render whose hidden
// field already carries the submitted token is never clobbered.
(function () {
  "use strict";
  var el = document.querySelector("[data-auth-fragment]");
  if (!el) {
    return;
  }
  var mode = el.getAttribute("data-auth-fragment");
  var targetID = el.getAttribute("data-auth-fragment-target");
  var autosubmit = el.getAttribute("data-auth-fragment-submit") === "true";

  var frag = window.location.hash ? window.location.hash.slice(1) : "";
  if (window.history && window.history.replaceState) {
    window.history.replaceState(
      null,
      document.title,
      window.location.pathname + window.location.search
    );
  }

  var value = "";
  if (frag) {
    if (mode === "token") {
      try {
        value = new URLSearchParams(frag).get("token") || "";
      } catch (e) {
        value = "";
      }
    } else {
      value = frag;
    }
  }

  if (!value) {
    return;
  }
  var field = targetID ? document.getElementById(targetID) : null;
  if (!field) {
    return;
  }
  field.value = value;
  if (autosubmit && el.tagName === "FORM") {
    el.submit();
  }
})();
