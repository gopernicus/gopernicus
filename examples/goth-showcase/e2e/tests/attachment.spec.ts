import { test, expect } from "@playwright/test";

// GOTH-6.1 Attachment (P55): real-browser proof across all three engines. The
// primitive DISPLAYS caller-owned upload state — it selects no file and owns no
// route. These specs prove the no-JS static gallery (states with meaning beyond
// color, sizes/orientation, group scrolling, full-card trigger with independently
// focusable actions) and a REAL multipart no-JS upload round-trip where the SERVER
// decides each attachment's state.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Attachment (P55) — static gallery, no JavaScript", () => {
  test("every state renders a text status label (meaning beyond color)", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-attachment");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    for (const [state, label] of [
      ["uploading", "Uploading 60%"],
      ["processing", "Processing"],
      ["error", "Upload failed: file too large"],
      ["done", "Uploaded"],
    ]) {
      const card = page
        .locator(`[data-slot="attachment"][data-state="${state}"]`)
        .first();
      await expect(
        card.locator('[data-slot="attachment-status"]'),
      ).toContainText(label);
    }

    // The uploading card exposes a determinate native <progress> (not an inline
    // style-driven bar).
    const bar = page
      .locator('[data-slot="attachment"][data-state="uploading"] progress')
      .first();
    await expect(bar).toHaveAttribute("value", "60");

    expect(errors).toEqual([]);
  });

  test("sizes and orientations are data-attribute driven, no inline styles", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-attachment");

    await expect(
      page.locator('[data-slot="attachment"][data-size="sm"]').first(),
    ).toBeVisible();
    await expect(
      page.locator('[data-slot="attachment"][data-size="lg"]').first(),
    ).toBeVisible();
    await expect(
      page
        .locator('[data-slot="attachment"][data-orientation="vertical"]')
        .first(),
    ).toBeVisible();

    // Invariant (a): no primitive emits an inline style=.
    expect(await page.locator("[style]").count()).toBe(0);
  });

  test("the group overflows and scrolls horizontally", async ({ page }) => {
    await page.goto("/specimen/primitive-attachment");
    const group = page
      .locator('[data-slot="attachment-group"][aria-label="Recent attachments"]')
      .first();
    const canScroll = await group.evaluate(
      (el) => el.scrollWidth > el.clientWidth,
    );
    expect(canScroll).toBe(true);
  });

  test("full-card trigger and actions are independently focusable in DOM order", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-attachment");
    const trigger = page.locator('[data-slot="attachment-trigger"]');
    const remove = page.getByRole("button", { name: "Remove report.pdf" });
    await expect(trigger).toHaveAttribute("aria-label", "Open report.pdf");

    // Both are independently focusable: the stretched trigger does not swallow the
    // action. (Tab order is asserted via DOM position below rather than a Tab press,
    // because WebKit gates Tab-to-button focus behind Full Keyboard Access.)
    await trigger.focus();
    await expect(trigger).toBeFocused();
    await remove.focus();
    await expect(remove).toBeFocused();

    // Keyboard order follows DOM order: the trigger precedes the action.
    const triggerBeforeAction = await page.evaluate(() => {
      const t = document.querySelector('[data-slot="attachment-trigger"]')!;
      const a = document.querySelector(
        '[data-slot="attachment-actions"] button',
      )!;
      return !!(
        t.compareDocumentPosition(a) & Node.DOCUMENT_POSITION_FOLLOWING
      );
    });
    expect(triggerBeforeAction).toBe(true);
  });
});

test.describe("Attachment (P55) — real multipart upload, no JavaScript", () => {
  test("a valid file uploads and the server renders it in the done state", async ({
    page,
    browserName,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-attachment-upload");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const name = `notes-${browserName}.txt`;
    await page.locator("#att-file").setInputFiles({
      name,
      mimeType: "text/plain",
      buffer: Buffer.from("hello from the no-JS multipart round-trip"),
    });
    await page.getByRole("button", { name: "Upload" }).click();

    // Full-document redirect back to the specimen (no JS): the server-decided card
    // appears in the done state with the filename.
    await expect(page).toHaveURL(/primitive-attachment-upload/);
    const card = page.locator(
      `[data-slot="attachment"][data-state="done"]`,
      { hasText: name },
    );
    await expect(card).toBeVisible();

    expect(errors).toEqual([]);
  });

  test("an oversized file is rejected by the server into the error state", async ({
    page,
    browserName,
  }) => {
    await page.goto("/specimen/primitive-attachment-upload");

    const name = `big-${browserName}.bin`;
    await page.locator("#att-file").setInputFiles({
      name,
      mimeType: "application/octet-stream",
      buffer: Buffer.alloc(600 * 1024, 1), // > 512 KB demo limit
    });
    await page.getByRole("button", { name: "Upload" }).click();

    const card = page.locator(
      `[data-slot="attachment"][data-state="error"]`,
      { hasText: name },
    );
    await expect(card).toBeVisible();
    await expect(card.locator('[data-slot="attachment-status"]')).toContainText(
      "exceeds 512 KB",
    );
  });
});
