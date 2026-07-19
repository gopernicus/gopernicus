import { test, expect } from "@playwright/test";

// GOTH-6.5 Sonner (P63): real-browser proof across all three engines. Sonner is the
// opinionated facade over the SAME gothToast queue — no separate runtime. These specs
// prove the honest baseline (a data-sonner region holding readable variant toasts), the
// variant → priority mapping (error announces assertively, success politely), the
// opinionated stacked presentation, and the HTMX-triggered path.

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
const assertiveText = (page: import("@playwright/test").Page) =>
  liveText(page, "assertive");
const politeText = (page: import("@playwright/test").Page) =>
  liveText(page, "polite");

test.describe("Sonner (P63) — opinionated facade over the shared queue", () => {
  test("the region is a data-sonner Toaster and rides the gothToast queue", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sonner");

    const r = page.locator(region);
    await expect(r).toHaveAttribute("data-sonner", "true");
    await expect(r).toHaveAttribute("role", "region");
    await expect(r).toHaveAttribute("data-enhanced", "true");
    // Opinionated max default (3).
    await expect(r).toHaveAttribute("data-max", "3");

    // Baseline variant toasts are readable with a status accent.
    await expect(page.locator("#sonner-saved")).toContainText("Changes saved");
    await expect(page.locator("#sonner-saved")).toHaveAttribute(
      "data-variant",
      "success",
    );
    await expect(
      page.locator('#sonner-saved [data-slot="sonner-accent"]'),
    ).toBeAttached();

    expect(errors).toEqual([]);
  });

  test("a success variant is polite and announced through the polite region", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-sonner");
    // The baseline success toast is polite by variant mapping.
    await expect(page.locator("#sonner-saved")).toHaveAttribute(
      "data-priority",
      "polite",
    );
    // A single HTMX-triggered success toast announces deterministically (one announce)
    // through the shared polite region.
    await page.locator('[data-slot="sonner-success"]').click();
    const ok = page.locator('[data-slot="toast"][id^="sonner-success-"]').first();
    await expect(ok).toBeVisible();
    await expect(ok).toHaveAttribute("data-priority", "polite");
    await expect.poll(() => politeText(page)).toContain("Uploaded");
  });

  test("an HTMX-triggered error toast is assertive and announced", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-sonner");
    await page.locator('[data-slot="sonner-error"]').click();

    const err = page
      .locator('[data-slot="toast"][id^="sonner-error-"]')
      .first();
    await expect(err).toBeVisible();
    await expect(err).toHaveAttribute("data-variant", "error");
    await expect(err).toHaveAttribute("data-priority", "assertive");
    await expect.poll(() => assertiveText(page)).toContain("Upload failed");
  });

  test("no Sonner part emits an inline style", async ({ page }) => {
    await page.goto("/specimen/primitive-sonner");
    for (const el of await page
      .locator(`${region}, ${toast}, [data-slot="sonner-accent"]`)
      .all()) {
      expect(await el.getAttribute("style")).toBeNull();
    }
  });
});
