import { test, expect } from "@playwright/test";
import { specimenLinks } from "./helpers";

// Strict-CSP smoke (carries the deferred GOTH-1.3/1.4 real-browser proof): every
// specimen loads under the host's strict CSP with zero securitypolicyviolation
// events and zero console/page errors, in all three engines.
test("strict CSP: no violations or console errors on any specimen", async ({
  page,
}) => {
  // This single test crawls every registered specimen in sequence; the crawl time
  // grows with the catalog (slowest on WebKit). Mark it slow (3x budget) so the
  // whole-catalog sweep fits — the zero-violation assertion is unchanged.
  test.slow();
  await page.addInitScript(() => {
    (window as unknown as { __violations: string[] }).__violations = [];
    document.addEventListener("securitypolicyviolation", (e) => {
      (window as unknown as { __violations: string[] }).__violations.push(
        `${e.violatedDirective} ${e.blockedURI}`,
      );
    });
  });

  const consoleErrors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("pageerror", (err) => consoleErrors.push(err.message));

  const links = await specimenLinks(page);
  expect(links.length).toBeGreaterThan(0);

  for (const href of links) {
    consoleErrors.length = 0;
    await page.goto(href);
    await page.waitForLoadState("networkidle");
    const violations = await page.evaluate(
      () => (window as unknown as { __violations: string[] }).__violations ?? [],
    );
    expect(violations, `CSP violations on ${href}`).toEqual([]);
    expect(consoleErrors, `console errors on ${href}`).toEqual([]);
  }
});

// no-unsafe-eval, proven in a live browser: an inline script that calls eval is
// injected INTO THE PAGE (not the isolated evaluate world, which is exempt from
// the page CSP). Under script-src 'self' with no 'unsafe-eval', the browser
// blocks it and raises a securitypolicyviolation — yet the Alpine CSP runtime
// still boots and works (runtime.spec.ts), which is the whole point of
// @alpinejs/csp.
test("CSP blocks inline script + eval in page context (strict script-src)", async ({
  page,
}) => {
  await page.addInitScript(() => {
    (window as unknown as { __evalViolation: boolean }).__evalViolation = false;
    document.addEventListener("securitypolicyviolation", (e) => {
      if (e.violatedDirective.startsWith("script-src")) {
        (window as unknown as { __evalViolation: boolean }).__evalViolation =
          true;
      }
    });
  });
  await page.goto("/specimen/profile-interactive");
  // Inject an inline script node into the document; strict script-src 'self'
  // (no 'unsafe-inline', no 'unsafe-eval') must block it.
  await page.evaluate(() => {
    const s = document.createElement("script");
    s.textContent = "window.__evalRan = eval('1+1');";
    document.body.appendChild(s);
  });
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => "__evalRan" in window)).toBe(false);
  expect(
    await page.evaluate(
      () => (window as unknown as { __evalViolation: boolean }).__evalViolation,
    ),
  ).toBe(true);
});

// no-remote-origin, proven in a live browser: every network request the Full
// profile page makes is same-origin — no CDN for CSS, JS, fonts, or icons.
test("no remote origin: every request is same-origin", async ({
  page,
  baseURL,
}) => {
  const foreign: string[] = [];
  page.on("request", (req) => {
    const url = req.url();
    if (!url.startsWith(baseURL!) && !url.startsWith("data:")) foreign.push(url);
  });
  await page.goto("/specimen/profile-full");
  await page.waitForLoadState("networkidle");
  expect(foreign, `foreign requests: ${foreign.join(", ")}`).toEqual([]);
});
