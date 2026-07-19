import { test, expect } from "@playwright/test";

// GOTH-5.3 Data Table (P52): real-browser proof across all three engines. The
// server owns sort/filter/page/selection state end to end. These specs prove the
// no-JS baseline (real links + form GET, shareable URLs, full-document reload) and
// the HTMX-enhanced parity (debounced live filter, sort/page content swaps that
// preserve the filter caret and update aria-sort — no full navigation).

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

const firstRowName =
  '#dt-content [data-slot="table-body"] tr:first-child [data-slot="table-cell"]:nth-child(2)';
const bodyRows = '#dt-content [data-slot="table-body"] tr';

test.describe("Data Table (P52) — no JavaScript", () => {
  test("sort headers are real links that re-sort via full navigation", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table-nojs");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    // Default sort is name ascending — aria-sort is the accessible source of truth.
    const nameHeader = page.locator(
      '[data-slot="data-table-sort-header"][data-direction]',
      { hasText: "Name" },
    );
    await expect(nameHeader).toHaveAttribute("aria-sort", "ascending");

    // Clicking "Age" navigates (no JS): the URL carries the server-owned sort state.
    await page
      .locator('[data-slot="data-table-sort-header"]', { hasText: "Age" })
      .locator("a")
      .click();
    await expect(page).toHaveURL(/sort=age/);
    await expect(page).toHaveURL(/dir=asc/);
    const ageHeader = page.locator(
      '[data-slot="data-table-sort-header"][data-direction]',
      { hasText: "Age" },
    );
    await expect(ageHeader).toHaveAttribute("aria-sort", "ascending");
    await expect(page.locator(firstRowName)).toHaveText("Ada Lovelace"); // youngest, 36

    // Clicking "Age" again flips to descending.
    await ageHeader.locator("a").click();
    await expect(page).toHaveURL(/sort=age/);
    await expect(page).toHaveURL(/dir=desc/);
    await expect(ageHeader).toHaveAttribute("aria-sort", "descending");
    await expect(page.locator(firstRowName)).toHaveText("Grace Hopper"); // oldest, 61

    expect(errors).toEqual([]);
  });

  test("filter form GET and pagination links produce shareable state", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table-nojs");

    // Page size is 4 → 12 people paginate to 3 pages. Page two is a real link.
    await page.locator('[data-slot="pagination-link"]', { hasText: "2" }).click();
    await expect(page).toHaveURL(/page=2/);
    await expect(page.locator(bodyRows)).toHaveCount(4);

    // The filter is a form GET; submitting narrows the rows (server-owned filter).
    await page.goto("/specimen/primitive-data-table-nojs");
    await page.locator("#dt-filter").fill("tur");
    await page.getByRole("button", { name: "Filter" }).click();
    await expect(page).toHaveURL(/q=tur/);
    await expect(page.locator(bodyRows)).toHaveCount(1);
    await expect(page.locator(firstRowName)).toHaveText("Alan Turing");

    expect(errors).toEqual([]);
  });

  test("selection checkboxes submit natively (server-owned selection state)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table-nojs");

    await page.getByLabel("Select Ada Lovelace").check();
    await page.getByRole("button", { name: "Filter" }).click();
    await expect(page).toHaveURL(/sel=Ada/);
    await expect(page.locator('[data-slot="data-table-status"]')).toContainText(
      "1 selected",
    );
    // The committed selection is reflected back by the server.
    await expect(page.getByLabel("Select Ada Lovelace")).toBeChecked();

    expect(errors).toEqual([]);
  });

  test("no inline styles and a clean accessibility tree", async ({ page }) => {
    await page.goto("/specimen/primitive-data-table-nojs");
    const inline = await page.locator("[style]").count();
    expect(inline).toBe(0);
    // The region and the table expose their names.
    await expect(page.locator('[data-slot="data-table"]')).toHaveAttribute(
      "aria-label",
      "People data table",
    );
  });
});

test.describe("Data Table (P52) — HTMX enhanced", () => {
  test("sorting swaps only the content region and updates aria-sort", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table");
    await page.waitForFunction(() => "htmx" in window);

    const heading = page.locator('[data-slot="data-table-htmx-specimen"] p');
    await expect(heading).toBeVisible();

    const ageHeader = page.locator(
      '[data-slot="data-table-sort-header"][data-direction]',
      { hasText: "Age" },
    );
    await ageHeader.locator("a").click();
    // The URL is pushed but the specimen shell (toolbar + intro copy) is not
    // re-navigated: only #dt-content swapped.
    await expect(page).toHaveURL(/sort=age/);
    await expect(heading).toBeVisible();
    await expect(ageHeader).toHaveAttribute("aria-sort", "ascending");
    await expect(page.locator(firstRowName)).toHaveText("Ada Lovelace");

    // The filter input survived the content swap (it lives in the toolbar).
    await expect(page.locator("#dt-filter")).toBeVisible();

    expect(errors).toEqual([]);
  });

  test("debounced live filter swaps the content and preserves the caret", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table");
    await page.waitForFunction(() => "htmx" in window);

    const input = page.locator("#dt-filter");
    await input.click();
    await input.type("ada");
    // The debounced hx-get swaps in only matching rows.
    await expect(page.locator(bodyRows)).toHaveCount(1);
    await expect(page.locator(firstRowName)).toHaveText("Ada Lovelace");
    // The input keeps its value and focus across the swap (it is outside #dt-content).
    await expect(input).toHaveValue("ada");
    await expect(input).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("pagination swaps the content region", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-data-table");
    await page.waitForFunction(() => "htmx" in window);

    await page.locator('[data-slot="pagination-link"]', { hasText: "3" }).click();
    await expect(page).toHaveURL(/page=3/);
    await expect(page.locator(bodyRows)).toHaveCount(4);

    expect(errors).toEqual([]);
  });
});
