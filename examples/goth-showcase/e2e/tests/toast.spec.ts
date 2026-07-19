import { test, expect } from "@playwright/test";

// GOTH-6.5 Toast (P64): real-browser proof across all three engines. The ONE
// gothToast-backed queue (the pre-provisioned GOTH-1.4 controller refined in place)
// owns announcement, timers, pause, overflow, dismissal, and actions. These specs prove
// the honest no-JS markup (a named role=region landmark holding readable toasts) and the
// enhanced behaviors: polite/assertive announcement through the shared live region,
// pause on hover/focus/hidden page, keyboard dismissal and actions, queue overflow, and
// the HTMX-triggered path.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

const region = '[data-slot="toaster"]';
const toast = '[data-slot="toast"]';

// liveText reads the shared visually-hidden live region for a politeness, returning ""
// when the region has not been created yet (so expect.poll retries instead of throwing).
async function liveText(
  page: import("@playwright/test").Page,
  politeness: "polite" | "assertive",
): Promise<string> {
  return page.evaluate((p) => {
    const el = document.querySelector(`.goth-sr-only[aria-live="${p}"]`);
    return el ? el.textContent || "" : "";
  }, politeness);
}
const politeText = (page: import("@playwright/test").Page) =>
  liveText(page, "polite");
const assertiveText = (page: import("@playwright/test").Page) =>
  liveText(page, "assertive");

test.describe("Toast (P64) — honest region + announcement", () => {
  test("the queue is a named, reachable, non-trapping region landmark", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-toast");

    const r = page.locator(region);
    await expect(r).toHaveAttribute("role", "region");
    await expect(r).toHaveAttribute("aria-label", "Notifications");
    await expect(r).toHaveAttribute("tabindex", "-1");
    // Enhanced marker present (controller bound), and the baseline toasts are readable.
    await expect(r).toHaveAttribute("data-enhanced", "true");
    await expect(page.locator("#toast-welcome")).toContainText("Welcome back");
    await expect(page.locator("#toast-alert")).toContainText("Storage almost full");

    expect(errors).toEqual([]);
  });

  test("server-rendered toasts announce once through the shared live region", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-toast");
    // The assertive baseline toast announced through the assertive shared region; the
    // polite one through the polite region. Announcement is the single channel — no
    // role=status/alert on the visible toasts.
    await expect.poll(() => assertiveText(page)).toContain("Storage almost full");
    await expect.poll(() => politeText(page)).toContain("Welcome back");
    for (const el of await page.locator(toast).all()) {
      expect(await el.getAttribute("role")).toBeNull();
    }
  });

  test("an HTMX-triggered assertive toast is appended and announced", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-toast");
    const before = await page.locator(toast).count();
    await page.locator('[data-slot="toast-assertive"]').click();
    await expect(page.locator(toast)).toHaveCount(before + 1);
    await expect
      .poll(() => assertiveText(page))
      .toContain("Action required");
  });
});

test.describe("Toast (P64) — timers, pause, dismissal, actions, overflow", () => {
  test("a timed toast auto-dismisses; hovering the region pauses the timer", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-toast");

    // Add a short timed toast, then hover it: hovering any toast pauses ALL timers
    // (the region is click-through, so pause rides the bubbling pointerover).
    await page.locator('[data-slot="toast-timed"]').click();
    const timed = page.locator('[data-slot="toast"][id^="toast-timed-"]').first();
    await expect(timed).toBeVisible();
    await timed.hover();
    await expect(page.locator(region)).toHaveAttribute("data-paused", "true");
    // Well past its 3s duration, the paused toast survives.
    await page.waitForTimeout(3500);
    await expect(timed).toBeVisible();

    // Move the pointer away: the timer resumes and the toast dismisses.
    await page.mouse.move(0, 0);
    await expect(page.locator(region)).not.toHaveAttribute("data-paused", "true");
    await expect(timed).toHaveCount(0, { timeout: 6000 });
  });

  test("a hidden page pauses timers", async ({ page }) => {
    await page.goto("/specimen/primitive-toast");
    await page.locator('[data-slot="toast-timed"]').click();
    const timed = page.locator('[data-slot="toast"][id^="toast-timed-"]').first();
    await expect(timed).toBeVisible();

    await page.evaluate(() => {
      Object.defineProperty(document, "hidden", {
        configurable: true,
        get: () => true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await expect(page.locator(region)).toHaveAttribute("data-paused", "true");
    await page.waitForTimeout(3500);
    await expect(timed).toBeVisible();
  });

  test("the close button dismisses via keyboard", async ({ page }) => {
    await page.goto("/specimen/primitive-toast");
    const welcome = page.locator("#toast-welcome");
    await welcome.locator('[data-slot="toast-close"]').focus();
    await page.keyboard.press("Enter");
    await expect(welcome).toHaveCount(0, { timeout: 6000 });
  });

  test("a button action dispatches goth:select and dismisses the toast", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-toast");
    await page.evaluate(() => {
      (window as unknown as { _selected: string[] })._selected = [];
      document.addEventListener("goth:select", (e) => {
        (window as unknown as { _selected: string[] })._selected.push(
          (e as CustomEvent).detail?.id ?? "",
        );
      });
    });
    await page.locator('[data-slot="toast-action"]').click();
    const actionToast = page
      .locator('[data-slot="toast"][id^="toast-action-"]')
      .first();
    await expect(actionToast).toBeVisible();
    await actionToast.locator('[data-slot="toast-action"]').click();
    await expect(actionToast).toHaveCount(0, { timeout: 6000 });
    expect(
      await page.evaluate(
        () => (window as unknown as { _selected: string[] })._selected.length,
      ),
    ).toBeGreaterThan(0);
  });

  test("the queue caps at data-max, overflowing the oldest out", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-toast");
    // Region caps at 3. Two persistent baseline toasts + several plain triggers.
    const plain = page.locator('[data-slot="toast-plain"]');
    for (let i = 0; i < 4; i++) {
      await plain.click();
      await page.waitForTimeout(100);
    }
    await expect
      .poll(async () => page.locator(toast).count())
      .toBe(3);
    // The oldest baseline toast overflowed out; the newest plain toast is present.
    await expect(page.locator("#toast-welcome")).toHaveCount(0);
    await expect(
      page.locator('[data-slot="toast"][id^="toast-plain-"]').last(),
    ).toBeVisible();
  });
});
