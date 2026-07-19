import { test, expect } from "@playwright/test";

// Host-theme-stylesheet proof (amendment-1 D3/D4): the kit ships compiled
// component CSS + an injected default palette; a host ships a theme stylesheet
// linked AFTER the kit stylesheet (the WordPress model). These specs run in all
// three engines (Chromium/Firefox/WebKit via playwright.config projects) and prove
// the link ordering, the default-asset omission on a host-configured page, the
// text/css content type, and that BOTH override axes (token + class) take effect
// in a real browser.

const HOST_SPECIMEN = "/specimen/theme-host-stylesheet";
const HOST_THEME_PATH = "/theme/host.css";
// The host stylesheet retunes --primary to this value (host_theme.css).
const HOST_PRIMARY = "oklch(0.55 0.22 265)";
// The kit default primary (theme/default.css) the host value must replace.
const KIT_DEFAULT_PRIMARY = "oklch(0.21 0.02 265)";

test("host page links kit CSS → host theme → scripts, in that order", async ({
  page,
}) => {
  await page.goto(HOST_SPECIMEN);
  const html = await page.content();

  const kit = html.search(/\/assets\/goth\/dist\/theme\.[0-9a-f]+\.css/);
  const host = html.indexOf(HOST_THEME_PATH);
  const script = html.indexOf("<script");

  expect(kit, "kit theme.css link present").toBeGreaterThanOrEqual(0);
  expect(host, "host theme link present").toBeGreaterThanOrEqual(0);
  expect(script, "runtime script present (Interactive profile)").toBeGreaterThanOrEqual(0);
  expect(kit, "kit CSS before host theme").toBeLessThan(host);
  expect(host, "host theme before scripts").toBeLessThan(script);

  // The host link is emitted verbatim with no integrity attribute.
  expect(html).toContain(`<link rel="stylesheet" href="${HOST_THEME_PATH}">`);
  // The kit's injected default theme is NOT linked when a host path is configured.
  expect(html, "host page omits the default theme asset").not.toContain(
    "theme-default",
  );
});

test("a default specimen links the injected default theme asset", async ({
  page,
}) => {
  // The omission above is meaningful only if a non-host page DOES carry it.
  await page.goto("/specimen/theme-light");
  const html = await page.content();
  expect(html).toMatch(/\/assets\/goth\/dist\/theme-default\.[0-9a-f]+\.css/);
});

test("the host theme stylesheet is served as text/css", async ({ request }) => {
  const resp = await request.get(HOST_THEME_PATH);
  expect(resp.status()).toBe(200);
  expect(resp.headers()["content-type"]).toContain("text/css");
  const body = await resp.text();
  expect(body).toContain("--primary");
  expect(body).toContain(".goth-badge");
});

test("host stylesheet overrides --primary (token-level) in the browser", async ({
  page,
}) => {
  await page.goto(HOST_SPECIMEN);
  const primary = await page.evaluate(() =>
    getComputedStyle(document.documentElement)
      .getPropertyValue("--primary")
      .trim(),
  );
  // Normalize internal whitespace so an engine's re-serialization still matches.
  const norm = primary.replace(/\s+/g, " ");
  expect(norm).toBe(HOST_PRIMARY);
  expect(norm).not.toBe(KIT_DEFAULT_PRIMARY);
});

test("host stylesheet squares the badge (class-level) in the browser", async ({
  page,
}) => {
  await page.goto(HOST_SPECIMEN);
  const badge = page.locator('[data-slot="badge"]').first();
  await expect(badge).toBeVisible();
  const radius = await badge.evaluate(
    (el) => getComputedStyle(el).borderTopLeftRadius,
  );
  expect(radius).toBe("0px");
});
