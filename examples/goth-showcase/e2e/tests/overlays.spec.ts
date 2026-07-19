import { test, expect } from "@playwright/test";

// GOTH-4.2 modal/panel primitives (P37 Alert Dialog, P39 Dialog, P40 Drawer, P47
// Sheet): real-browser proof across all three engines. These drive the REAL
// primitive components (rendered by the showcase from ui/goth/primitives), proving
// each composes the frozen GOTH-4.1 overlay mechanics through the single
// gothDialog controller: focus trap/restore, nested-aware Escape/outside
// dismissal, background scroll lock + inert, and the Alert Dialog decision
// contract (no outside dismiss). A zero-console/CSP-error guard runs on every case.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Dialog (P39)", () => {
  test("focus trap + restore, scroll lock, inert, and aria-modal lifecycle", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="dialog"]').first();
    const content = dialog.locator('> .goth-overlay > [data-slot="content"]');
    const trigger = page.locator("#dialog-trigger");
    const html = page.locator("html");

    await expect(dialog).toHaveAttribute("data-state", "closed");
    await expect(html).not.toHaveClass(/goth-scroll-locked/);

    // Open via keyboard so focus stays on the trigger (WebKit does not focus a
    // <button> on pointer click), making the restore target reliable.
    await trigger.focus();
    await page.keyboard.press("Enter");

    await expect(dialog).toHaveAttribute("data-state", "open");
    await expect(html).toHaveClass(/goth-scroll-locked/);
    await expect(content).toHaveAttribute("aria-modal", "true");
    await expect(page.locator("#dialog-input")).toBeFocused();
    await expect(page.locator('[data-slot="scroll-probe"]')).toHaveAttribute(
      "inert",
      "",
    );

    // Escape dismisses, restores focus to the trigger, and releases lock/inert/
    // aria-modal.
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveAttribute("data-state", "closed");
    await expect(html).not.toHaveClass(/goth-scroll-locked/);
    await expect(trigger).toBeFocused();
    expect(await content.getAttribute("aria-modal")).toBeNull();
    expect(
      await page.locator('[data-slot="scroll-probe"]').getAttribute("inert"),
    ).toBeNull();

    expect(errors).toEqual([]);
  });

  test("scrim click dismisses a plain Dialog", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="dialog"]').first();
    await page.locator("#dialog-trigger").click();
    await expect(dialog).toHaveAttribute("data-state", "open");

    // Scope to the outer dialog's own scrim: the (closed) nested dialog also
    // carries a data-slot="scrim", so an unscoped selector is ambiguous.
    await dialog
      .locator('> .goth-overlay > [data-slot="scrim"]')
      .click({ position: { x: 4, y: 4 } });
    await expect(dialog).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("close button dismisses the Dialog", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="dialog"]').first();
    await page.locator("#dialog-trigger").click();
    await expect(dialog).toHaveAttribute("data-state", "open");
    await page.locator("#dialog-close").click();
    await expect(dialog).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });

  test("nested Dialog: Escape unwinds exactly one level per press", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const outer = page.locator('[data-slot="dialog"]').first();
    const inner = page.locator('[data-slot="dialog"] [data-slot="dialog"]');

    await page.locator("#dialog-trigger").click();
    await expect(outer).toHaveAttribute("data-state", "open");
    await page.locator("#nested-trigger").click();
    await expect(inner).toHaveAttribute("data-state", "open");
    await expect(page.locator("#nested-input")).toBeFocused();

    // First Escape closes only the inner dialog; the outer stays open.
    await page.keyboard.press("Escape");
    await expect(inner).toHaveAttribute("data-state", "closed");
    await expect(outer).toHaveAttribute("data-state", "open");

    // Second Escape closes the outer dialog and releases the scroll lock.
    await page.keyboard.press("Escape");
    await expect(outer).toHaveAttribute("data-state", "closed");
    await expect(page.locator("html")).not.toHaveClass(/goth-scroll-locked/);

    expect(errors).toEqual([]);
  });

  test("server-open Dialog is readable with no JavaScript (StylesOnly)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-dialog-open");
    // No Alpine on a StylesOnly page: the content is visible purely from the
    // server-rendered data-state="open" + CSS.
    const dialog = page.locator('[data-slot="dialog"]').first();
    await expect(dialog).toHaveAttribute("data-state", "open");
    await expect(
      page.locator('[data-slot="content"] [data-slot="title"]'),
    ).toBeVisible();
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);
    expect(errors).toEqual([]);
  });
});

test.describe("Alert Dialog (P37)", () => {
  test("decision contract: scrim does NOT dismiss, Escape and buttons do", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-alert-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const alert = page.locator('[data-slot="alert-dialog"]');
    const content = alert.locator('> .goth-overlay > [data-slot="content"]');

    await page.locator("#alert-trigger").focus();
    await page.keyboard.press("Enter");
    await expect(alert).toHaveAttribute("data-state", "open");
    await expect(content).toHaveAttribute("role", "alertdialog");
    // Cancel is the first focusable (the safe default for a destructive prompt).
    await expect(page.locator("#alert-cancel")).toBeFocused();

    // A scrim press must NOT dismiss an alert dialog (the decision must be made).
    await page
      .locator(
        '[data-slot="alert-dialog"] > .goth-overlay > [data-slot="scrim"]',
      )
      .click({ position: { x: 4, y: 4 } });
    await expect(alert).toHaveAttribute("data-state", "open");

    // Escape still cancels (APG alertdialog) and restores focus to the trigger.
    await page.keyboard.press("Escape");
    await expect(alert).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#alert-trigger")).toBeFocused();

    // Reopen and confirm the destructive Action closes the dialog.
    await page.locator("#alert-trigger").click();
    await expect(alert).toHaveAttribute("data-state", "open");
    await page.locator("#alert-action").click();
    await expect(alert).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });
});

test.describe("Sheet (P47)", () => {
  test("edge panel opens with focus trap and closes", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sheet");
    await page.waitForFunction(() => "Alpine" in window);

    const sheet = page.locator('[data-slot="sheet"]');
    const content = sheet.locator('> .goth-overlay > [data-slot="content"]');

    await expect(content).toHaveAttribute("data-side", "right");
    await page.locator("#sheet-trigger").focus();
    await page.keyboard.press("Enter");

    await expect(sheet).toHaveAttribute("data-state", "open");
    await expect(page.locator("html")).toHaveClass(/goth-scroll-locked/);
    await expect(page.locator("#sheet-input")).toBeFocused();

    await page.keyboard.press("Escape");
    await expect(sheet).toHaveAttribute("data-state", "closed");
    await expect(page.locator("#sheet-trigger")).toBeFocused();

    expect(errors).toEqual([]);
  });
});

test.describe("Drawer (P40)", () => {
  test("bottom drawer opens with a handle, traps focus, and closes", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-drawer");
    await page.waitForFunction(() => "Alpine" in window);

    const drawer = page.locator('[data-slot="drawer"]');
    const content = drawer.locator('> .goth-overlay > [data-slot="content"]');

    await expect(content).toHaveAttribute("data-side", "bottom");
    await expect(content.locator('[data-slot="handle"]')).toHaveCount(1);

    await page.locator("#drawer-trigger").click();
    await expect(drawer).toHaveAttribute("data-state", "open");
    await expect(page.locator("#drawer-input")).toBeFocused();

    await page.locator("#drawer-close").click();
    await expect(drawer).toHaveAttribute("data-state", "closed");

    expect(errors).toEqual([]);
  });
});
