import { test, expect } from "@playwright/test";

// HTMX full / fragment / error / history diagnostics (carries the deferred
// GOTH-1.4 real-browser proof) against the self-hosted htmx 2.0.10 + the
// Gopernicus non-2xx response configuration.

test.beforeEach(async ({ page }) => {
  await page.goto("/specimen/htmx-fixtures");
  await page.waitForFunction(() => "htmx" in window);
});

test("success: 200 fragment swaps into the target", async ({ page }) => {
  await page.locator('[data-slot="htmx-success-trigger"]').click();
  await expect(
    page.locator('#htmx-result [data-slot="htmx-success"]'),
  ).toContainText("Loaded via HTMX");
});

test("validation: 422 swaps a complete validation fragment", async ({
  page,
}) => {
  await page.locator('[data-slot="htmx-validate-trigger"]').click();
  await expect(
    page.locator('#htmx-result [data-slot="htmx-validation"]'),
  ).toContainText("Name is required");
});

test("error: 500 surfaces an error without swapping the target", async ({
  page,
}) => {
  await page.locator('[data-slot="htmx-error-trigger"]').click();
  // Give htmx time to process the response; the 500 must NOT paint a fragment.
  await page.waitForTimeout(750);
  await expect(page.locator("#htmx-result")).toBeEmpty();
});

test("history: push-url then correct full-document re-fetch", async ({
  page,
  baseURL,
}) => {
  await page.locator('[data-slot="htmx-history-trigger"]').click();
  await expect(
    page.locator('#htmx-page [data-slot="htmx-page-two"]'),
  ).toContainText("Page two fragment");
  await expect(page).toHaveURL(`${baseURL}/htmx/page/two`);

  await page.goBack();
  await expect(page).toHaveURL(`${baseURL}/specimen/htmx-fixtures`);

  // A hard re-fetch of the pushed URL returns a correct full document.
  await page.goto("/htmx/page/two");
  await expect(
    page.locator('[data-slot="htmx-page-two-document"]'),
  ).toBeVisible();
});
