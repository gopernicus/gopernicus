import { test, expect } from "@playwright/test";

// GOTH-4.4 menu primitives (P38 Context Menu, P41 Dropdown Menu, P43 Menubar, P44
// Navigation Menu): real-browser proof across all three engines. Dropdown/Context/
// Menubar compose the frozen gothMenu mechanics (anchored open, roving focus,
// typeahead, submenu, Escape unwind, outside dismiss). Navigation Menu is
// link-first + native <details> and works with no JavaScript. A zero-console/CSP
// guard runs on every case.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Dropdown Menu (P41)", () => {
  test("opens anchored, roves, typeahead, and the item roles are correct", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dropdown-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    const trigger = page.locator("#dm-trigger");
    await expect(content).toHaveAttribute("data-state", "closed");
    await expect(trigger).toHaveAttribute("aria-expanded", "false");

    await trigger.click();
    await expect(content).toHaveAttribute("data-state", "open");
    await expect(trigger).toHaveAttribute("aria-expanded", "true");
    // Anchored placement writes the CSSOM offset (CSP-safe). data-state flips
    // synchronously in show() before the $nextTick anchor write, so poll until
    // the anchor activation has settled rather than reading once (deflake).
    await expect
      .poll(() =>
        content.evaluate((el) =>
          getComputedStyle(el).getPropertyValue("--goth-anchor-top").trim(),
        ),
      )
      .not.toBe("");
    await expect(content).toHaveAttribute("data-side", "bottom");

    // The first item is focused on open; ArrowDown advances.
    await expect(page.locator("#dm-new")).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator('[data-value="open"]')).toBeFocused();

    // Checkbox/radio menu-item roles are present with aria-checked.
    await expect(page.locator('[data-value="grid"]')).toHaveAttribute(
      "role",
      "menuitemcheckbox",
    );
    await expect(page.locator('[data-value="grid"]')).toHaveAttribute(
      "aria-checked",
      "true",
    );
    await expect(page.locator('[data-value="sm"]')).toHaveAttribute(
      "role",
      "menuitemradio",
    );

    // Typeahead: "l" jumps to the "Large" radio item (the only item starting l).
    await page.keyboard.press("l");
    await expect(page.locator('[data-value="lg"]')).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("submenu opens with ArrowRight and Escape unwinds nested-first", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dropdown-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    const submenu = page.locator('[data-slot="submenu"]');
    await page.locator("#dm-trigger").click();
    await expect(content.locator('[data-slot="item"]').first()).toBeFocused();

    await page.locator("#dm-share").focus();
    await page.keyboard.press("ArrowRight");
    await expect(submenu).toHaveAttribute("data-state", "open");
    await expect(page.locator("#dm-email")).toBeFocused();

    // Escape closes only the submenu; focus returns to its trigger, menu open.
    await page.keyboard.press("Escape");
    await expect(submenu).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#dm-share")).toBeFocused();
    await expect(content).toHaveAttribute("data-state", "open");

    // Escape again closes the menu, focus back to the trigger.
    await page.keyboard.press("Escape");
    await expect(content).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#dm-trigger")).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("an outside press dismisses the menu", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dropdown-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    await page.locator("#dm-trigger").click();
    await expect(content).toHaveAttribute("data-state", "open");
    // Wait for the open focus + dismisser to settle before the outside press, then
    // click a concrete outside element (the page heading) so the press is reliable
    // under heavy parallel load.
    await expect(page.locator("#dm-new")).toBeFocused();
    await page.locator("main h1").click();
    await expect(content).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("no-JS: a server-open dropdown is readable with Alpine absent", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dropdown-menu-open");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const content = page.locator('[data-slot="menu"] [data-slot="content"]');
    await expect(content).toHaveAttribute("data-state", "open");
    await expect(content).toBeVisible();
    await expect(page.locator("#dm-new")).toBeVisible();

    expect(errors).toEqual([]);
  });
});

test.describe("Context Menu (P38)", () => {
  test("right-click opens the menu at the pointer; Escape restores focus", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-context-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="context-menu"] [data-slot="content"]');
    const region = page.locator("#context-region");
    await expect(content).toHaveAttribute("data-state", "closed");

    await region.click({ button: "right" });
    await expect(content).toHaveAttribute("data-state", "open");
    await expect(content).toHaveAttribute("data-side", /top|bottom|left|right/);
    await expect(page.locator("#cm-copy")).toBeFocused();

    // Escape closes and returns focus to the region.
    await page.keyboard.press("Escape");
    await expect(content).toHaveAttribute("data-state", "closed");
    await expect(region).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("keyboard: the ContextMenu key opens it from the focused region", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-context-menu");
    await page.waitForFunction(() => "Alpine" in window);

    const content = page.locator('[data-slot="context-menu"] [data-slot="content"]');
    await page.locator("#context-region").focus();
    await page.keyboard.press("ContextMenu");
    await expect(content).toHaveAttribute("data-state", "open");
    await expect(page.locator("#cm-copy")).toBeFocused();

    expect(errors).toEqual([]);
  });
});

test.describe("Menubar (P43)", () => {
  test("horizontal roving across the triggers and open on ArrowDown", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-menubar");
    await page.waitForFunction(() => "Alpine" in window);

    await page.locator("#mb-file").focus();
    // ArrowRight moves the single tab stop to the next trigger.
    await page.keyboard.press("ArrowRight");
    await expect(page.locator("#mb-edit")).toBeFocused();
    await page.keyboard.press("ArrowLeft");
    await expect(page.locator("#mb-file")).toBeFocused();

    // ArrowDown opens the File menu and focuses its first item.
    await page.keyboard.press("ArrowDown");
    const fileContent = page.locator(
      '[data-slot="menubar-menu"]:has(#mb-file) [data-slot="content"]',
    );
    await expect(fileContent).toHaveAttribute("data-state", "open");
    await expect(page.locator("#mb-file-new")).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("ArrowRight while open switches to the adjacent menu; Escape closes", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-menubar");
    await page.waitForFunction(() => "Alpine" in window);

    await page.locator("#mb-file").focus();
    await page.keyboard.press("ArrowDown");
    await expect(page.locator("#mb-file-new")).toBeFocused();

    // ArrowRight closes File, opens Edit, and focuses Edit's first item.
    await page.keyboard.press("ArrowRight");
    const editContent = page.locator(
      '[data-slot="menubar-menu"]:has(#mb-edit) [data-slot="content"]',
    );
    await expect(editContent).toHaveAttribute("data-state", "open");
    await expect(page.locator("#mb-edit-undo")).toBeFocused();

    // Escape closes the open menu and restores focus to its trigger.
    await page.keyboard.press("Escape");
    await expect(editContent).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#mb-edit")).toBeFocused();

    expect(errors).toEqual([]);
  });
});

test.describe("Navigation Menu (P44)", () => {
  test("no-JS: links are real links and the native details discloses sublinks", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-navigation-menu");
    // Link-first + native <details>: no controller/JS on a StylesOnly page.
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    // Real links: hrefs are present and the active link carries aria-current.
    await expect(page.locator("#nav-home")).toHaveJSProperty("tagName", "A");
    await expect(page.locator("#nav-home")).toHaveAttribute("href", "/");
    await expect(page.locator("#nav-home")).toHaveAttribute(
      "aria-current",
      "page",
    );

    // The disclosure is a native <details>: collapsed until the summary toggles.
    const sub = page.locator('[data-slot="navigation-menu-sub"]');
    const analytics = page.locator("#nav-analytics");
    await expect(analytics).toBeHidden();
    await expect(sub).not.toHaveJSProperty("open", true);

    await page.locator("#nav-products").click();
    await expect(sub).toHaveJSProperty("open", true);
    await expect(analytics).toBeVisible();
    await expect(analytics).toHaveAttribute("href", "/products/analytics");

    expect(errors).toEqual([]);
  });
});
