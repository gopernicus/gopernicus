import { test, expect } from "@playwright/test";

// GOTH-5.1 date primitives (P49 Calendar, P53 Date Picker): real-browser proof
// across all three engines. The server owns every date computation and the
// parse/format contract; these specs prove the no-JS baseline, the gothRovingFocus
// grid keyboard enhancement, and the HTMX-enhanced selection with its no-JS parity.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

const dayValue = (iso: string) => `[data-slot="calendar-day"][value="${iso}"]`;

test.describe("Calendar (P49)", () => {
  test("server-rendered grid: single tab stop + APG arrow/Home grid keyboard", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-calendar");
    await page.waitForFunction(() => "Alpine" in window);

    // No-JS baseline is present: the grid, weekday headers, and the server-selected
    // day are all in the markup.
    await expect(page.locator('[data-slot="calendar-grid"]')).toHaveAttribute(
      "role",
      "grid",
    );
    const selected = page.locator(dayValue("2026-07-15"));
    await expect(selected).toHaveAttribute("data-selected", "true");
    // aria-selected lives on the gridcell (the role that supports it), per APG.
    await expect(
      page.locator('[data-slot="calendar-cell"]:has(button[value="2026-07-15"])'),
    ).toHaveAttribute("aria-selected", "true");

    // Roving: only the selected day is in the tab order (single tab stop).
    await expect(selected).toHaveAttribute("tabindex", "0");
    await expect(page.locator(dayValue("2026-07-16"))).toHaveAttribute(
      "tabindex",
      "-1",
    );

    // ArrowRight moves focus to the next day; ArrowDown moves one week down (same
    // column); Home jumps to the first focusable day of that row.
    await selected.focus();
    await page.keyboard.press("ArrowRight");
    await expect(page.locator(dayValue("2026-07-16"))).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator(dayValue("2026-07-23"))).toBeFocused();
    await page.keyboard.press("Home");
    // The week of the 23rd (Sun 19 – Sat 25); Min is the 6th so all are enabled.
    await expect(page.locator(dayValue("2026-07-19"))).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("no-JS: selecting a day submits the form to the server", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-calendar");

    // A day button is a native submit: clicking it GETs /calendar/select with the
    // ISO date, and the server echoes the parsed selection.
    await page.locator(dayValue("2026-07-20")).click();
    await expect(page).toHaveURL(/\/calendar\/select\?date=2026-07-20/);
    await expect(page.locator('[data-slot="calendar-echo"]')).toContainText(
      "Monday, July 20, 2026",
    );

    expect(errors).toEqual([]);
  });

  test("out-of-range days are disabled and not focusable", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-calendar");

    // Min is the 6th: the 5th is a disabled span, not a submit button.
    await expect(page.locator(dayValue("2026-07-05"))).toHaveCount(0);
    const disabled = page.locator(
      '[data-slot="calendar-cell"] span[data-disabled="true"]',
    );
    await expect(disabled.first()).toBeVisible();

    expect(errors).toEqual([]);
  });
});

test.describe("Date Picker (P53)", () => {
  test("HTMX: selecting a day swaps the field and closes the popover", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-date-picker");
    await page.waitForFunction(() => "htmx" in window);

    // The server-formatted value is in the field; the popover is closed initially.
    await expect(page.locator("#dp-input")).toHaveValue("2026-07-15");
    await expect(page.locator("#dp-pop")).toBeHidden();

    // Open the native popover and select a day; HTMX swaps #dp-fragment with the
    // server-rendered field carrying the new value, and the popover closes.
    await page.locator("#dp-trigger").click();
    await expect(page.locator("#dp-pop")).toBeVisible();
    await page.locator(dayValue("2026-07-20")).click();

    await expect(page.locator("#dp-input")).toHaveValue("2026-07-20");
    await expect(page.locator("#dp-pop")).toBeHidden();

    expect(errors).toEqual([]);
  });

  test("no-JS: native popover + form submission with no scripting", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-date-picker-nojs");

    // StylesOnly: neither Alpine nor HTMX is loaded.
    await expect(await page.evaluate(() => "htmx" in window)).toBe(false);

    // The native popover opens from the trigger with no JavaScript.
    await page.locator("#dp-trigger").click();
    await expect(page.locator("#dp-pop")).toBeVisible();

    // Selecting a day submits the form (GET) to the server, which re-renders the
    // picker with the parsed value.
    await page.locator(dayValue("2026-07-20")).click();
    await expect(page).toHaveURL(/\/datepicker\/pick\?/);
    await expect(page.locator("#dp-input")).toHaveValue("2026-07-20");

    expect(errors).toEqual([]);
  });
});
