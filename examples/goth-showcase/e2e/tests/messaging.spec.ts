import { test, expect } from "@playwright/test";

// GOTH-6.2 Bubble (P56) + Message (P59): real-browser proof across all three
// engines. Both primitives are StylesOnly (no controller, no JavaScript). These
// specs prove the seven bubble variants, start/end alignment, grouped threads,
// interactive reaction chips (button + link with accessible names and toggled
// state), interactive link content, the aligned message row with
// avatar/header/body/footer, a Message composing a Bubble, and grouped messages
// under one avatar. P60 Message Scroller is intentionally absent (blocked on an
// owner §8 reopen — see plan.md GOTH-6.2 evidence).

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Bubble (P56) — no JavaScript", () => {
  test("renders the seven variants with data-variant hooks", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-bubble");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    for (const v of [
      "default",
      "primary",
      "secondary",
      "muted",
      "accent",
      "destructive",
      "outline",
    ]) {
      await expect(
        page.locator(`[data-slot="bubble-content"][data-variant="${v}"]`).first(),
      ).toBeVisible();
    }
    expect(errors).toEqual([]);
  });

  test("alignment is data-driven (start/end) with no inline style", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-bubble");
    const align = page.locator(
      '[data-slot="bubble-alignment"] [data-slot="bubble"]',
    );
    await expect(align.nth(0)).toHaveAttribute("data-align", "start");
    await expect(align.nth(1)).toHaveAttribute("data-align", "end");
    for (const el of await page
      .locator('[data-slot="bubble"], [data-slot="bubble-content"]')
      .all()) {
      expect(await el.getAttribute("style")).toBeNull();
    }
  });

  test("grouped thread aligns its bubbles to one edge", async ({ page }) => {
    await page.goto("/specimen/primitive-bubble");
    const group = page.locator('[data-slot="bubble-group"]');
    await expect(group).toHaveAttribute("data-align", "end");
    await expect(group.locator('[data-slot="bubble"]')).toHaveCount(3);
  });

  test("reactions are interactive with accessible names and a toggled state", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-bubble");
    const reactions = page.locator('[data-slot="bubble-reactions"]');
    await expect(reactions).toHaveAttribute("role", "group");

    // Pressed button reaction.
    const pressed = reactions.locator(
      'button[data-slot="bubble-reaction"][aria-pressed="true"]',
    );
    await expect(pressed).toHaveAttribute("aria-label", "Thumbs up");

    // Unpressed button reaction is keyboard-focusable and toggles nothing on the
    // server-owned side (no controller); it is a real button.
    const unpressed = reactions.locator(
      'button[data-slot="bubble-reaction"][aria-pressed="false"]',
    );
    await expect(unpressed).toHaveAttribute("aria-label", "Celebrate");
    await unpressed.focus();
    await expect(unpressed).toBeFocused();

    // Link reaction (permalink form) is an anchor with an accessible name.
    const link = reactions.locator('a[data-slot="bubble-reaction"]');
    await expect(link).toHaveAttribute("aria-label", "Loved this");
    await expect(link).toHaveAttribute("href", "/specimen/primitive-bubble");
  });

  test("interactive link content inside a bubble is a real link", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-bubble");
    const link = page.locator(
      '[data-slot="bubble-interactive"] [data-slot="bubble-content"] a',
    );
    await expect(link).toHaveAttribute("href", "/specimen/primitive-message");
  });
});

test.describe("Message (P59) — no JavaScript", () => {
  test("aligned rows carry avatar, header, body, and footer", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-message");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const rows = page.locator('[data-slot="message-rows"] [data-slot="message"]');
    await expect(rows.nth(0)).toHaveAttribute("data-align", "start");
    await expect(rows.nth(1)).toHaveAttribute("data-align", "end");

    const incoming = rows.nth(0);
    await expect(incoming.locator('[data-slot="message-avatar"]')).toBeVisible();
    await expect(incoming.locator('[data-slot="message-header"]')).toContainText(
      "Ada Lovelace",
    );
    await expect(incoming.locator('[data-slot="message-body"]')).toContainText(
      "analytical engine",
    );
    await expect(incoming.locator('[data-slot="message-footer"]')).toContainText(
      "Delivered",
    );
    expect(errors).toEqual([]);
  });

  test("a Message can compose a Bubble as its body", async ({ page }) => {
    await page.goto("/specimen/primitive-message");
    const bubble = page.locator(
      '[data-slot="message-bubble"] [data-slot="message-body"] [data-slot="bubble-content"]',
    );
    await expect(bubble).toHaveAttribute("data-variant", "primary");
    await expect(bubble).toContainText("Bubble (P56)");
  });

  test("grouped messages are headed by a single avatar", async ({ page }) => {
    await page.goto("/specimen/primitive-message");
    const group = page.locator('[data-slot="message-group"]');
    await expect(group).toHaveAttribute("data-align", "start");
    await expect(group.locator('[data-slot="message-avatar"]')).toHaveCount(1);
    await expect(group.locator('[data-slot="message-content"]')).toHaveCount(3);
  });

  test("no message part emits an inline style", async ({ page }) => {
    await page.goto("/specimen/primitive-message");
    for (const el of await page
      .locator(
        '[data-slot="message"], [data-slot="message-content"], [data-slot="message-group"]',
      )
      .all()) {
      expect(await el.getAttribute("style")).toBeNull();
    }
  });
});
