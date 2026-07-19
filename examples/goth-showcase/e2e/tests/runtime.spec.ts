import { test, expect } from "@playwright/test";

// Profile diagnostics: each bundle profile loads exactly its promised assets from
// the self-hosted fingerprinted dist/.

test("StylesOnly loads no JavaScript", async ({ page }) => {
  await page.goto("/specimen/profile-styles");
  expect(await page.locator("script").count()).toBe(0);
  expect(await page.evaluate(() => "Alpine" in window)).toBe(false);
});

test("Interactive: Alpine CSP boots and gothCollapse toggles", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });

  await page.goto("/specimen/profile-interactive");
  await page.waitForFunction(() => "Alpine" in window);

  // gothCollapse enhances a native <details>: the summary toggles it natively and
  // the controller mirrors the open state onto data-state.
  const root = page.locator('[data-slot="collapsible"]');
  await expect(root).toHaveAttribute("data-state", "closed");
  await expect(page.locator("#collapse-content")).toBeHidden();

  await page.locator('[data-slot="collapsible-trigger"]').click();
  await expect(root).toHaveAttribute("data-state", "open");
  await expect(page.locator("#collapse-content")).toBeVisible();

  expect(errors).toEqual([]);
});

test("Full: Alpine and HTMX are both present", async ({ page }) => {
  await page.goto("/specimen/profile-full");
  await page.waitForFunction(() => "Alpine" in window && "htmx" in window);
  expect(await page.evaluate(() => "htmx" in window)).toBe(true);
});
