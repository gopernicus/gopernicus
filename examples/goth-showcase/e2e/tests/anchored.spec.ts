import { test, expect } from "@playwright/test";

// GOTH-4.3 anchored information/selection primitives (P42 Hover Card, P45
// Popover, P46 Select, P48 Tooltip): real-browser proof across all three engines.
// Each drives the REAL primitive components rendered by the showcase from
// ui/goth/primitives. Tooltip/Hover Card compose the frozen mechanics through the
// new gothTooltip/gothHoverCard controllers (hover-intent delay, focus-shows,
// Escape-hides, no focus trap, describedby wiring). Popover rides the native
// popover API (no-JS baseline) plus the runtime anchor enhancement. Select is a
// styled native <select> (native form value, no-JS submission). A zero-console/CSP
// guard runs on every case.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Tooltip (P48)", () => {
  test("focus shows, Escape hides, and it does not trap focus", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-tooltip");
    await page.waitForFunction(() => "Alpine" in window);

    const tooltip = page.locator('[data-slot="tooltip"]');
    const trigger = page.locator("#tooltip-trigger");

    // describedby wiring: the trigger points at the content id.
    await expect(trigger).toHaveAttribute("aria-describedby", "tooltip-desc");
    await expect(page.locator("#tooltip-desc")).toHaveAttribute(
      "role",
      "tooltip",
    );
    await expect(tooltip).toHaveAttribute("data-state", "closed");

    // Focus shows the tooltip immediately (no artificial keyboard delay).
    await trigger.focus();
    await expect(tooltip).toHaveAttribute("data-state", "open");
    // Focus stays on the trigger — the tooltip never moves/traps focus.
    await expect(trigger).toBeFocused();

    // Escape hides it (and focus remains on the trigger).
    await page.keyboard.press("Escape");
    await expect(tooltip).toHaveAttribute("data-state", "closed");
    await expect(trigger).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("hover opens after the intent delay and leave closes it", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-tooltip");
    await page.waitForFunction(() => "Alpine" in window);

    const tooltip = page.locator('[data-slot="tooltip"]');
    await page.locator("#tooltip-trigger").hover();
    await expect(tooltip).toHaveAttribute("data-state", "open");

    // Move the pointer away; the tooltip closes after the grace delay.
    await page.mouse.move(0, 0);
    await expect(tooltip).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("CSS baseline reveals on hover with no JavaScript (StylesOnly)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-tooltip-css");
    // No Alpine on a StylesOnly page: the reveal is pure CSS :hover/:focus-within.
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const content = page.locator("#tooltip-desc");
    await expect(content).toBeHidden();
    await page.locator("#tooltip-trigger").hover();
    await expect(content).toBeVisible();

    expect(errors).toEqual([]);
  });
});

test.describe("Hover Card (P42)", () => {
  test("hover/focus opens, Escape closes, and the link trigger stays a link", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-hover-card");
    await page.waitForFunction(() => "Alpine" in window);

    const card = page.locator('[data-slot="hover-card"]');
    const trigger = page.locator("#hover-card-trigger");

    // Links remain links: the trigger is an anchor with a real href.
    await expect(trigger).toHaveJSProperty("tagName", "A");
    await expect(trigger).toHaveAttribute("href", "/users/ada");
    await expect(card).toHaveAttribute("data-state", "closed");

    await trigger.hover();
    await expect(card).toHaveAttribute("data-state", "open");

    // The panel content is reachable (not trapped): its link is visible.
    await expect(page.locator('[data-slot="hovercard-link"]')).toBeVisible();

    // Escape closes it.
    await page.keyboard.press("Escape");
    await expect(card).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("an outside press dismisses the card (touch-safe)", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-hover-card");
    await page.waitForFunction(() => "Alpine" in window);

    const card = page.locator('[data-slot="hover-card"]');
    await page.locator("#hover-card-trigger").focus();
    await expect(card).toHaveAttribute("data-state", "open");

    await page.mouse.click(5, 5);
    await expect(card).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });
});

test.describe("Popover (P45)", () => {
  test("native popover toggles, anchors to the trigger, and Escape closes", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-popover");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator("#popover-content");
    await expect(content).toBeHidden();

    await page.locator("#popover-trigger").click();
    await expect(content).toBeVisible();
    // The runtime anchor enhancement positioned it against the trigger.
    await expect(content).toHaveAttribute("data-side", /top|bottom|left|right/);

    // Escape closes the native popover.
    await page.keyboard.press("Escape");
    await expect(content).toBeHidden();

    expect(errors).toEqual([]);
  });

  test("an outside press light-dismisses the popover", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-popover");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator("#popover-content");
    await page.locator("#popover-trigger").click();
    await expect(content).toBeVisible();
    await page.mouse.click(5, 5);
    await expect(content).toBeHidden();

    expect(errors).toEqual([]);
  });

  test("no-JS: native popover opens and Escape closes with Alpine absent (StylesOnly)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-popover-nojs");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const content = page.locator("#popover-content");
    await expect(content).toBeHidden();
    await page.locator("#popover-trigger").click();
    await expect(content).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(content).toBeHidden();

    expect(errors).toEqual([]);
  });
});

test.describe("Select (P46)", () => {
  test("styled native select selects a value", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-select");
    const select = page.locator("#fruit-select");
    await expect(select).toHaveJSProperty("tagName", "SELECT");
    await select.selectOption("apple");
    await expect(select).toHaveValue("apple");
    expect(errors).toEqual([]);
  });

  test("no-JS form submits the native select value with GET", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/select-form");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    await page.locator("#f-fruit").selectOption("pear");
    await page.locator('[data-slot="select-form"] button[type="submit"]').click();

    await expect(page).toHaveURL(/\/anchored\/echo\?fruit=pear/);
    await expect(page.locator('[data-field="fruit"]')).toHaveText("pear");

    expect(errors).toEqual([]);
  });
});
