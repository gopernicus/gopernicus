import { test, expect } from "@playwright/test";

// GOTH-4.1 shared overlay/menu mechanics: real-browser proof across all three
// engines, before any Phase-4 primitive wrapper is built. These exercise the
// frozen mechanics through the gothDialog / gothMenu controllers: focus
// trap/restore, nested-aware Escape/outside dismissal, scroll lock, background
// inert, anchored placement, roving focus, typeahead, and submenu hierarchy.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Dialog mechanics", () => {
  test("focus trap + restore, scroll lock, and background inert", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/mechanics-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="dialog"]');
    const trigger = page.locator("#dialog-trigger");
    const html = page.locator("html");

    await expect(dialog).toHaveAttribute("data-state", "closed");
    await expect(html).not.toHaveClass(/goth-scroll-locked/);

    // Open via keyboard so focus stays on the trigger across engines (WebKit does
    // not focus a <button> on pointer click), making the restore target reliable.
    await trigger.focus();
    await page.keyboard.press("Enter");

    // Opened: scroll locked, focus pulled into the panel's first focusable.
    await expect(dialog).toHaveAttribute("data-state", "open");
    await expect(html).toHaveClass(/goth-scroll-locked/);
    await expect(page.locator("#dialog-input")).toBeFocused();

    // Background outside the dialog's ancestor chain is inert.
    await expect(page.locator('[data-slot="scroll-probe"]')).toHaveAttribute(
      "inert",
      "",
    );

    // Escape dismisses and restores focus to the trigger; lock + inert release.
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveAttribute("data-state", "closed");
    await expect(html).not.toHaveClass(/goth-scroll-locked/);
    await expect(trigger).toBeFocused();
    expect(
      await page
        .locator('[data-slot="scroll-probe"]')
        .getAttribute("inert"),
    ).toBeNull();

    expect(errors).toEqual([]);
  });

  test("outside pointer on the scrim dismisses the dialog", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/mechanics-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="dialog"]');
    await page.locator("#dialog-trigger").click();
    await expect(dialog).toHaveAttribute("data-state", "open");

    // Click the scrim corner (outside the centered panel) — an outside press.
    // Direct-child selector so only the OUTER scrim matches (not the nested one).
    await page
      .locator('[data-slot="dialog"] > .goth-overlay > [data-slot="scrim"]')
      .click({ position: { x: 4, y: 4 } });
    await expect(dialog).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("nested dialog: Escape unwinds exactly one level per press", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/mechanics-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const outer = page.locator('[data-slot="dialog"]');
    const inner = page.locator('[data-slot="nested-dialog"]');

    await page.locator("#dialog-trigger").click();
    await expect(outer).toHaveAttribute("data-state", "open");

    await page.locator("#nested-trigger").click();
    await expect(inner).toHaveAttribute("data-state", "open");
    // The inner trap pulls focus into the nested panel's first focusable.
    await expect(page.locator("#nested-input")).toBeFocused();

    // First Escape closes only the inner dialog; the outer stays open.
    await page.keyboard.press("Escape");
    await expect(inner).toHaveAttribute("data-state", "closed");
    await expect(outer).toHaveAttribute("data-state", "open");

    // Second Escape closes the outer dialog.
    await page.keyboard.press("Escape");
    await expect(outer).toHaveAttribute("data-state", "closed");
    await expect(page.locator("html")).not.toHaveClass(/goth-scroll-locked/);

    expect(errors).toEqual([]);
  });
});

test.describe("Menu mechanics", () => {
  test("anchored open, roving focus, and typeahead", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/mechanics-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    await expect(content).toHaveAttribute("data-state", "closed");

    await page.locator("#menu-trigger").click();
    await expect(content).toHaveAttribute("data-state", "open");

    // Anchored placement writes the numeric offsets via the CSSOM (CSP-safe).
    // gothMenu flips data-state="open" synchronously in show() but writes
    // --goth-anchor-top inside $nextTick (after the panel is measured), so a
    // single read right after the data-state assertion races the anchor write
    // under parallel load. Poll until the anchor activation has settled.
    await expect
      .poll(() =>
        content.evaluate((el) =>
          getComputedStyle(el).getPropertyValue("--goth-anchor-top").trim(),
        ),
      )
      .not.toBe("");
    await expect(content).toHaveAttribute("data-side", "bottom");

    // Roving: the first item is focused on open; ArrowDown advances.
    const items = content.locator('[data-slot="item"]');
    await expect(items.first()).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator('[data-value="open"]')).toBeFocused();

    // Typeahead: "d" jumps to Delete (the only item starting with d).
    await page.keyboard.press("d");
    await expect(page.locator('[data-value="delete"]')).toBeFocused();

    // Home returns to the first item.
    await page.keyboard.press("Home");
    await expect(items.first()).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("submenu: ArrowRight opens, focuses first item, Escape unwinds nested-first", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/mechanics-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    const submenu = page.locator('[data-slot="submenu"]');

    await page.locator("#menu-trigger").click();
    await expect(content).toHaveAttribute("data-state", "open");
    // Wait for the open focus to settle on the first item before driving keys.
    await expect(content.locator('[data-slot="item"]').first()).toBeFocused();

    // Move roving focus down to the submenu trigger (New file -> Open recent -> Share).
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await expect(page.locator("#submenu-trigger")).toBeFocused();

    // ArrowRight opens the submenu and focuses its first item.
    await page.keyboard.press("ArrowRight");
    await expect(submenu).toHaveAttribute("data-state", "open");
    await expect(page.locator("#submenu-email")).toBeFocused();

    // Escape closes only the submenu; focus returns to its trigger, menu open.
    await page.keyboard.press("Escape");
    await expect(submenu).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#submenu-trigger")).toBeFocused();
    await expect(content).toHaveAttribute("data-state", "open");

    // Escape again closes the menu, focus back to the menu trigger.
    await page.keyboard.press("Escape");
    await expect(content).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#menu-trigger")).toBeFocused();

    expect(errors).toEqual([]);
  });
});
