import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { specimenLinks } from "./helpers";

// Automated accessibility pass on every showcase specimen in all three engines.
// An axe green is not a substitute for the interaction assertions in the other
// specs (plan Layer 3); it complements them.
test("axe: every specimen has no accessibility violations", async ({ page }) => {
  // This single test crawls and axe-analyzes every registered specimen in
  // sequence; the crawl time grows with the catalog and is slowest on WebKit.
  // Mark it slow (3x budget) so the whole-catalog sweep fits — the zero-violation
  // assertion is unchanged.
  test.slow();
  // The combined /all page is the single largest DOM; it gets its own axe case
  // (below) with its own budget so the whole-catalog sweep stays within its slow
  // budget on the slowest engine (WebKit).
  const links = (await specimenLinks(page)).filter((h) => h !== "/all");
  expect(links.length).toBeGreaterThan(0);

  for (const href of links) {
    await page.goto(href);
    await page.waitForLoadState("networkidle");
    const results = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
      .analyze();
    expect(
      results.violations,
      `axe violations on ${href}: ${results.violations
        .map((v) => v.id)
        .join(", ")}`,
    ).toEqual([]);
  }
});

// The combined /all "kitchen sink" page — every specimen on one document — has no
// accessibility violations either. Its own case (own slow budget) proves the
// per-specimen id namespacing keeps label/for and aria wiring valid at scale so no
// duplicate-id or broken-reference violation appears when the whole catalog shares
// one page.
test("axe: combined /all page has no accessibility violations", async ({
  page,
}) => {
  test.slow();
  await page.goto("/all");
  await page.waitForLoadState("networkidle");
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  expect(
    results.violations,
    `axe violations on /all: ${results.violations.map((v) => v.id).join(", ")}`,
  ).toEqual([]);
});

// Reduced-motion axis: the motion specimen renders cleanly with the
// prefers-reduced-motion media emulation, and axe stays green.
test.describe("reduced motion", () => {
  test.use({ reducedMotion: "reduce" });
  test("axe: reduced-motion specimen", async ({ page }) => {
    await page.goto("/specimen/theme-motion");
    const results = await new AxeBuilder({ page }).analyze();
    expect(results.violations).toEqual([]);
  });
});
