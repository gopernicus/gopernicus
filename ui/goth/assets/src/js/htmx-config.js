// Gopernicus HTMX response configuration (GOTH-1.4).
//
// Appended to the vendored htmx 2.0.10 to form the single self-hosted htmx.js
// asset (Full profile). It configures NON-2xx fragment handling EXPLICITLY so a
// route can return a complete swappable region with a stable target on a client
// error, per README §9 / the plan's HTMX-4-forward rule 5. It is pinned to HTMX
// 2.0.10 semantics and does NOT rely on HTMX 4's changed defaults.
//
// responseHandling rules are evaluated top-to-bottom; the first matching code
// wins. Codes are regex-tested against the 3-digit status.
//
//   403 forbidden, 409 conflict, 422 validation -> swap the returned fragment
//     (a complete forbidden/conflict/validation region) while still marking the
//     exchange an error so hx-on / htmx:responseError observers fire.
//   204 No Content -> nothing to swap.
//   2xx / 3xx     -> swap (the success path).
//   other 4xx/5xx -> do not swap; surface as an error (the server did not return
//     a fragment for these).
//
// The runtime derives NO identity/CSRF/authorization decision from an HTMX
// header; this only chooses which responses paint a fragment (README §9).
(function () {
  var htmx = window.htmx;
  if (!htmx || !htmx.config) return;
  // htmx 2 injects an inline <style> for its default indicator opacity rules,
  // which a strict style-src 'self' (no 'unsafe-inline') blocks — violating the
  // kit invariant "no static inline script/style requirement" (README §4). The
  // kit owns indicator styling through its compiled theme.css, so the runtime
  // inline-style injection is disabled here; no adopter needs 'unsafe-inline'.
  htmx.config.includeIndicatorStyles = false;
  htmx.config.responseHandling = [
    { code: "204", swap: false },
    { code: "[23]..", swap: true },
    { code: "403", swap: true, error: true },
    { code: "409", swap: true, error: true },
    { code: "422", swap: true, error: true },
    { code: "[45]..", swap: false, error: true },
  ];
})();
