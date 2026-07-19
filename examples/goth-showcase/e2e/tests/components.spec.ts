import { test, expect } from "@playwright/test";

// GOTH-7.1 components layer: real-browser proof across all three engines that the
// opinionated compositions (layouts / forms / feedback / data) behave correctly on
// the actual emitted surface. Each specimen renders a REAL ui/goth component
// composing primitives — no hand-written markup — so these assertions cover the
// shipped API. A zero-console/CSP-error guard runs on every case.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("ConfirmDialog (feedback) — gothDialog workflow", () => {
  test("trigger opens a modal alertdialog, traps focus, Escape cancels + restores", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/component-confirm-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="alert-dialog"]');
    const content = dialog.locator('.goth-overlay > [data-slot="content"]');
    const trigger = dialog.locator('[data-slot="trigger"]');
    const html = page.locator("html");

    await expect(dialog).toHaveAttribute("data-state", "closed");

    // Open via keyboard so focus starts on the trigger (reliable restore target).
    await trigger.focus();
    await page.keyboard.press("Enter");

    await expect(dialog).toHaveAttribute("data-state", "open");
    await expect(content).toHaveAttribute("role", "alertdialog");
    await expect(content).toHaveAttribute("aria-modal", "true");
    await expect(html).toHaveClass(/goth-scroll-locked/);
    // The panel names/describes itself from the derived ids.
    await expect(content).toHaveAttribute(
      "aria-labelledby",
      "confirm-delete-title",
    );
    await expect(content).toHaveAttribute(
      "aria-describedby",
      "confirm-delete-description",
    );

    // Escape cancels (APG alertdialog) and restores focus to the trigger.
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveAttribute("data-state", "closed");
    await expect(html).not.toHaveClass(/goth-scroll-locked/);
    await expect(trigger).toBeFocused();

    expect(errors).toEqual([]);
  });

  test("scrim does not dismiss; cancel dismisses; confirm submits the server form", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/component-confirm-dialog");
    await page.waitForFunction(() => "Alpine" in window);

    const dialog = page.locator('[data-slot="alert-dialog"]');
    await dialog.locator('[data-slot="trigger"]').click();
    await expect(dialog).toHaveAttribute("data-state", "open");

    // Alert dialog: an outside/scrim press must NOT dismiss a destructive decision.
    await dialog
      .locator('.goth-overlay > [data-slot="scrim"]')
      .click({ position: { x: 4, y: 4 } });
    await expect(dialog).toHaveAttribute("data-state", "open");

    // Cancel dismisses with no side effect.
    await dialog.locator('[data-slot="cancel"]').click();
    await expect(dialog).toHaveAttribute("data-state", "closed");

    // Re-open and confirm: the confirm button submits the POST form to the server,
    // which renders the confirmation page (workflow proven end to end).
    await dialog.locator('[data-slot="trigger"]').click();
    await expect(dialog).toHaveAttribute("data-state", "open");
    await dialog.locator('[data-slot="action"]').click();
    await expect(page).toHaveURL(/\/components\/confirm$/);
    await expect(page.locator('[data-slot="confirm-outcome"]')).toBeVisible();

    expect(errors).toEqual([]);
  });
});

test.describe("FormField (forms) — no JavaScript", () => {
  test("label associates the control and an error wires aria-describedby", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/component-form-field");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    // Clicking the label focuses its control (native for/id association).
    await page.locator('label[for="ff-email"]').click();
    await expect(page.locator("#ff-email")).toBeFocused();

    // The invalid field flags data-invalid and the derived error id is present and
    // referenced by the control's aria-describedby.
    const invalidField = page
      .locator('[data-slot="field"][data-invalid="true"]')
      .filter({ has: page.locator("#ff-handle") });
    await expect(invalidField).toBeVisible();
    await expect(page.locator("#ff-handle-error")).toContainText(
      "Only letters",
    );
    await expect(page.locator("#ff-handle")).toHaveAttribute(
      "aria-describedby",
      "ff-handle-error",
    );

    expect(errors).toEqual([]);
  });
});

test.describe("ErrorSummary (forms) — no JavaScript", () => {
  test("a summary link points at and focuses its field", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/component-error-summary");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const summary = page.locator('[data-slot="error-summary-list"]');
    await expect(summary).toBeVisible();

    await page.locator('[data-slot="error-summary-list"] a[href="#es-email"]').click();
    await expect(page).toHaveURL(/#es-email$/);
    await expect(page.locator("#es-email")).toBeFocused();

    expect(errors).toEqual([]);
  });
});

test.describe("TableToolbar (data) — no JavaScript", () => {
  test("search is a role=search form GET producing a shareable URL", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/component-table-toolbar");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    await expect(page.locator('[data-slot="table-toolbar-form"]')).toHaveAttribute(
      "role",
      "search",
    );

    await page.locator("#table-toolbar-search").fill("ada");
    await page.getByRole("button", { name: "Search" }).click();
    await expect(page).toHaveURL(/[?&]q=ada/);

    expect(errors).toEqual([]);
  });
});

test.describe("layout shells (layouts) — no JavaScript", () => {
  test("shells render their regions with no inline style", async ({ page }) => {
    const errors = guard(page);

    for (const id of [
      "component-document-shell",
      "component-app-shell",
      "component-auth-shell",
      "component-page-header",
      "component-action-bar",
    ]) {
      await page.goto(`/specimen/${id}`);
      expect(await page.locator("[style]").count()).toBe(0);
    }

    // AppShell exposes the three regions; PageHeader exposes an h1 title; ActionBar
    // is a named toolbar.
    await page.goto("/specimen/component-app-shell");
    await expect(page.locator('[data-slot="app-shell-sidebar"]')).toBeVisible();
    await expect(page.locator('[data-slot="app-shell-main"]')).toBeVisible();

    await page.goto("/specimen/component-page-header");
    await expect(
      page.locator('h1[data-slot="page-header-title"]'),
    ).toHaveText("People");

    await page.goto("/specimen/component-action-bar");
    await expect(page.locator('[role="toolbar"]')).toHaveAttribute(
      "aria-label",
      "Row actions",
    );

    expect(errors).toEqual([]);
  });
});

test.describe("feedback panels (feedback) — no JavaScript", () => {
  test("empty/loading/error panels expose their tone and roles", async ({
    page,
  }) => {
    const errors = guard(page);

    await page.goto("/specimen/component-empty-panel");
    await expect(
      page.locator('[data-slot="feedback-panel"][data-tone="empty"]'),
    ).toBeVisible();

    await page.goto("/specimen/component-loading-panel");
    const loading = page.locator(
      '[data-slot="feedback-panel"][data-tone="loading"]',
    );
    await expect(loading).toHaveAttribute("role", "status");
    await expect(loading).toHaveAttribute("aria-busy", "true");

    await page.goto("/specimen/component-error-panel");
    await expect(
      page.locator('[data-slot="feedback-panel"][data-tone="error"]'),
    ).toHaveAttribute("role", "alert");

    expect(errors).toEqual([]);
  });
});
