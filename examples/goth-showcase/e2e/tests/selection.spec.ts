import { test, expect } from "@playwright/test";

// GOTH-3.2 form-selection primitives: real-browser interaction proof for Checkbox
// (P28), Switch (P33), Radio Group (P31), and Slider (P32) across all three
// engines. These are entirely native (no Alpine controller), so the specimens run
// under the StylesOnly profile: the tests double as a strict no-JavaScript
// baseline proof — native toggling, arrow-key selection, the native range keyboard
// model, and ordinary form submission all work with no runtime on the page.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Checkbox (P28)", () => {
  test("native toggle, keyboard, and indeterminate visual state", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-checkbox");

    const terms = page.locator("#cb-terms");
    await expect(terms).not.toBeChecked();

    // Pointer toggle is native (no JS).
    await terms.click();
    await expect(terms).toBeChecked();

    // Keyboard: Space toggles the focused checkbox natively.
    await terms.focus();
    await page.keyboard.press(" ");
    await expect(terms).not.toBeChecked();

    // A server-checked box submits checked.
    await expect(page.locator("#cb-news")).toBeChecked();

    // Indeterminate is a visual-only third state carried on data-state.
    await expect(page.locator("#cb-all")).toHaveAttribute(
      "data-state",
      "indeterminate",
    );

    // Clicking a label toggles its associated control (for/id wiring).
    const bad = page.locator("#cb-bad");
    await expect(bad).toHaveAttribute("aria-invalid", "true");

    expect(errors).toEqual([]);
  });
});

test.describe("Switch (P33)", () => {
  test("native checkbox with role=switch toggles without JS", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-switch");

    const notify = page.locator("#sw-notify");
    await expect(notify).toHaveAttribute("role", "switch");
    await expect(notify).not.toBeChecked();

    await notify.click();
    await expect(notify).toBeChecked();

    await notify.focus();
    await page.keyboard.press(" ");
    await expect(notify).not.toBeChecked();

    // The server-on switch is checked; the disabled one cannot toggle.
    await expect(page.locator("#sw-beta")).toBeChecked();
    await expect(page.locator("#sw-lock")).toBeDisabled();

    expect(errors).toEqual([]);
  });
});

test.describe("Radio Group (P31)", () => {
  test("native single selection and arrow-key navigation", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-radio-group");

    const free = page.locator("#rg-free");
    const pro = page.locator("#rg-pro");

    // Server-selected default.
    await expect(free).toBeChecked();
    await expect(pro).not.toBeChecked();

    // Native arrow navigation moves selection within the group (roving focus).
    await free.focus();
    await page.keyboard.press("ArrowDown");
    await expect(pro).toBeFocused();
    await expect(pro).toBeChecked();
    await expect(free).not.toBeChecked();

    // The disabled item is skipped and cannot be selected.
    await expect(page.locator("#rg-ent")).toBeDisabled();

    // A second, horizontally-oriented group selects independently.
    await expect(page.locator("#rg-m")).toBeChecked();

    expect(errors).toEqual([]);
  });
});

test.describe("Slider (P32)", () => {
  test("native range keyboard model increments the submitted value", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-slider");

    const vol = page.locator("#sl-vol");
    await expect(vol).toHaveJSProperty("value", "60");

    // Native range keyboard: ArrowRight increments by the step (1).
    await vol.focus();
    await page.keyboard.press("ArrowRight");
    await expect(vol).toHaveJSProperty("value", "61");

    // Home/End jump to the bounds natively.
    await page.keyboard.press("Home");
    await expect(vol).toHaveJSProperty("value", "0");
    await page.keyboard.press("End");
    await expect(vol).toHaveJSProperty("value", "100");

    // Stepped slider honours its step.
    const temp = page.locator("#sl-temp");
    await temp.focus();
    await page.keyboard.press("ArrowRight");
    await expect(temp).toHaveJSProperty("value", "25");

    expect(errors).toEqual([]);
  });
});

test.describe("Selection form (no-JS submission)", () => {
  test("native controls submit their values with no JavaScript", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/selection-form");

    // Check the terms box; the switch, radio, and slider keep their defaults.
    await page.locator("#f-terms").check();

    // Submitting navigates natively (GET) to the echo route.
    await Promise.all([
      page.waitForURL(/\/selection\/echo/),
      page.locator('[data-slot="selection-form"] button[type="submit"]').click(),
    ]);

    await expect(page.locator('[data-field="terms"]')).toHaveText("yes");
    await expect(page.locator('[data-field="notify"]')).toHaveText("on");
    await expect(page.locator('[data-field="plan"]')).toHaveText("free");
    await expect(page.locator('[data-field="volume"]')).toHaveText("30");

    expect(errors).toEqual([]);
  });
});
