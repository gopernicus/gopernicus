import { test, expect } from "@playwright/test";

// GOTH-3.1 disclosure primitives: real-browser interaction proof for Accordion
// (P27), Collapsible (P29), and Tabs (P34) across all three engines. These
// complement the crawl-based axe/CSP specs with the interaction assertions the
// Phase 3 gate demands: native no-JS baseline, keyboard, focus, and RTL.

// Collapse a page-error/CSP guard used by every test here.
function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Collapsible (P29)", () => {
  test("native <details> toggles and gothCollapse mirrors data-state", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-collapsible");
    await page.waitForFunction(() => "Alpine" in window);

    const first = page.locator('[data-slot="collapsible"]').first();
    const content = first.locator('[data-slot="collapsible-content"]');
    await expect(first).toHaveAttribute("data-state", "closed");
    await expect(content).toBeHidden();

    // Native summary click toggles the region; the controller reflects state.
    await first.locator('[data-slot="collapsible-trigger"]').click();
    await expect(first).toHaveAttribute("data-state", "open");
    await expect(content).toBeVisible();

    // Keyboard: focus the summary and toggle with Enter (native behavior).
    await first.locator('[data-slot="collapsible-trigger"]').focus();
    await page.keyboard.press("Enter");
    await expect(first).toHaveAttribute("data-state", "closed");
    await expect(content).toBeHidden();

    // The second specimen is server-rendered open (no-JS baseline readable).
    const open = page.locator('[data-slot="collapsible"]').nth(1);
    await expect(open).toHaveAttribute("data-state", "open");
    await expect(open.locator('[data-slot="collapsible-content"]')).toBeVisible();

    expect(errors).toEqual([]);
  });
});

test.describe("Accordion (P27)", () => {
  test("single-mode items are natively exclusive (name attribute)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-accordion");
    await page.waitForFunction(() => "Alpine" in window);

    const items = page.locator(
      '[data-slot="accordion"]:has([name="faq"]) [data-slot="accordion-item"]',
    );
    const first = items.nth(0);
    const second = items.nth(1);

    // The first item starts open (server-rendered).
    await expect(first).toHaveAttribute("data-state", "open");

    // Opening the second closes the first — native single-open, no JS needed for
    // the exclusivity itself; the controller keeps data-state honest on both.
    await second.locator('[data-slot="accordion-trigger"]').click();
    await expect(second).toHaveAttribute("data-state", "open");
    await expect(first).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });
});

test.describe("Tabs (P34)", () => {
  test("roving focus + automatic activation switches panels", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-tabs");
    await page.waitForFunction(() => "Alpine" in window);

    const account = page.locator("#tab-account");
    const password = page.locator("#tab-password");

    // No-JS baseline is server-rendered: first tab active, its panel visible.
    await expect(account).toHaveAttribute("aria-selected", "true");
    await expect(page.locator("#panel-account")).toBeVisible();
    await expect(page.locator("#panel-password")).toBeHidden();

    // Roving focus: only the active tab is in the tab order.
    await expect(account).toHaveAttribute("tabindex", "0");
    await expect(password).toHaveAttribute("tabindex", "-1");

    // ArrowRight moves focus and (automatic activation) selects the next tab.
    await account.focus();
    await page.keyboard.press("ArrowRight");
    await expect(password).toBeFocused();
    await expect(password).toHaveAttribute("aria-selected", "true");
    await expect(account).toHaveAttribute("aria-selected", "false");
    await expect(page.locator("#panel-password")).toBeVisible();
    await expect(page.locator("#panel-account")).toBeHidden();

    // Home returns to the first tab.
    await page.keyboard.press("Home");
    await expect(account).toBeFocused();
    await expect(account).toHaveAttribute("aria-selected", "true");

    // Pointer activation also switches.
    await page.locator("#tab-notifications").click();
    await expect(page.locator("#panel-notifications")).toBeVisible();

    expect(errors).toEqual([]);
  });

  test("RTL reverses the roving arrow direction (ArrowLeft advances)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-tabs-rtl");
    await page.waitForFunction(() => "Alpine" in window);

    // The document is RTL; the horizontal tab list lays out right-to-left, so the
    // visually-next tab is to the LEFT. ArrowLeft must advance (Radix/APG RTL
    // parity), ArrowRight must retreat — the mirror of the LTR test above.
    const account = page.locator("#tab-account");
    const password = page.locator("#tab-password");
    await expect(account).toHaveAttribute("aria-selected", "true");

    await account.focus();
    await page.keyboard.press("ArrowLeft");
    await expect(password).toBeFocused();
    await expect(password).toHaveAttribute("aria-selected", "true");
    await expect(account).toHaveAttribute("aria-selected", "false");
    await expect(page.locator("#panel-password")).toBeVisible();

    // ArrowRight goes back toward the start in RTL.
    await page.keyboard.press("ArrowRight");
    await expect(account).toBeFocused();
    await expect(account).toHaveAttribute("aria-selected", "true");

    expect(errors).toEqual([]);
  });
});
