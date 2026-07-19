import { test, expect } from "@playwright/test";

// GOTH-6.2 Message Scroller (P60): real-browser proof across all three engines. The
// server owns the transcript and pagination; the gothMessageScroller controller (the
// tenth §8 controller, ratified 2026-07-18) manages only scroll position and the
// scroll/unread presentation state. These specs prove the no-JS-honest markup
// (role=log transcript, a real "load earlier" link, native #id jump anchors) and the
// four enhanced behaviors: live-edge following, HTMX prepend-without-jump,
// jump-to-message focus, and unread/scroll-state exposure.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

const viewport = '[data-slot="message-scroller-viewport"]';
const content = "#scroller-content";
const root = '[data-slot="message-scroller"]';

async function atBottom(page: import("@playwright/test").Page): Promise<boolean> {
  return page.$eval(viewport, (v) => v.scrollHeight - v.scrollTop - v.clientHeight <= 24);
}

test.describe("Message Scroller (P60) — server-owned transcript, honest markup", () => {
  test("the transcript is a named role=log with the earlier link and jump anchors", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-message-scroller");

    const log = page.locator(viewport);
    await expect(log).toHaveAttribute("role", "log");
    await expect(log).toHaveAttribute("aria-label", "Release thread transcript");
    await expect(log).toHaveAttribute("tabindex", "0");

    // "Load earlier" is a real link (the no-JS reload) marked for prepend anchoring.
    const earlier = page.locator('[data-slot="message-scroller-history"]');
    await expect(earlier).toHaveAttribute("href", "/message-scroller/earlier");
    await expect(earlier).toHaveAttribute("data-goth-history", "true");

    // Jump links are native in-page anchors.
    await expect(
      page.locator('[data-slot="message-scroller-jump"]').first(),
    ).toHaveAttribute("href", "#msg-1");

    expect(errors).toEqual([]);
  });

  test("opens at the live edge (latest visible)", async ({ page }) => {
    await page.goto("/specimen/primitive-message-scroller");
    await expect
      .poll(() => atBottom(page), { timeout: 5000 })
      .toBe(true);
    await expect(page.locator(root)).toHaveAttribute("data-at-edge", "true");
    await expect(page.locator(root)).toHaveAttribute("data-unread", "0");
  });
});

test.describe("Message Scroller (P60) — live-edge following and unread state", () => {
  test("an incoming message sticks to the bottom while at the live edge", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-message-scroller");
    await expect.poll(() => atBottom(page)).toBe(true);

    const before = await page.locator(`${content} > *`).count();
    await page.locator('[data-slot="scroller-incoming"]').click();
    await expect(page.locator(`${content} > *`)).toHaveCount(before + 1);

    // Followed: still at the bottom, no unread accrued.
    await expect.poll(() => atBottom(page)).toBe(true);
    await expect(page.locator(root)).toHaveAttribute("data-unread", "0");
  });

  test("scrolling up stops following and accrues unread; the status jumps back", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-message-scroller");
    await expect.poll(() => atBottom(page)).toBe(true);

    // Scroll to the top: following stops.
    await page.$eval(viewport, (v) => {
      v.scrollTop = 0;
    });
    await expect(page.locator(root)).toHaveAttribute("data-at-edge", "false");

    // Incoming message off-edge accrues unread and reveals the status affordance.
    await page.locator('[data-slot="scroller-incoming"]').click();
    await expect(page.locator(root)).toHaveAttribute("data-unread", "1");
    const status = page.locator('[data-slot="message-scroller-status"]');
    await expect(status).toBeVisible();
    await expect(status).toContainText("1 new message");

    // Clicking the status returns to the live edge and clears unread.
    await status.click();
    await expect.poll(() => atBottom(page)).toBe(true);
    await expect(page.locator(root)).toHaveAttribute("data-unread", "0");
    await expect(status).toBeHidden();
  });
});

test.describe("Message Scroller (P60) — history prepend without jump", () => {
  test("loading earlier history keeps the reading position anchored", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-message-scroller");
    await expect.poll(() => atBottom(page)).toBe(true);

    // Park the reader mid-transcript (a modest offset that will not clamp to the
    // bottom) on a known, visible message.
    await page.$eval(viewport, (v) => {
      v.scrollTop = 100;
    });
    const anchorId = "msg-4";
    const beforeTop = await page.$eval(
      `#${anchorId}`,
      (el) => el.getBoundingClientRect().top,
    );

    await page.locator('[data-slot="message-scroller-history"]').click();
    // The prepended earlier turns arrive at the top of the content.
    await expect(page.locator("#msg-earlier-1")).toBeAttached();

    // The controller re-anchors on HTMX afterSettle (after HTMX's own settle scroll):
    // poll until the anchored message returns to ~the same viewport position (no jump).
    // A small tolerance covers sub-pixel layout differences across engines.
    await expect
      .poll(
        async () =>
          Math.abs(
            (await page.$eval(`#${anchorId}`, (el) => el.getBoundingClientRect().top)) -
              beforeTop,
          ),
        { timeout: 3000 },
      )
      .toBeLessThan(4);

    // And the viewport did not snap to the very top.
    expect(await page.$eval(viewport, (v) => v.scrollTop)).toBeGreaterThan(0);
  });
});

test.describe("Message Scroller (P60) — jump to message", () => {
  test("a jump link scrolls to the target and moves focus to it", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-message-scroller");
    await expect.poll(() => atBottom(page)).toBe(true);

    await page.locator('[data-slot="message-scroller-jump"]').first().click();

    // The target message receives focus (reader intent preserved) and is made
    // programmatically focusable without joining the tab order.
    await expect(page.locator("#msg-1")).toBeFocused();
    await expect(page.locator("#msg-1")).toHaveAttribute("tabindex", "-1");
    await expect(page.locator(root)).toHaveAttribute("data-jumped", "msg-1");

    expect(errors).toEqual([]);
  });
});
