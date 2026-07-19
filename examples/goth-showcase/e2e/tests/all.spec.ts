import { test, expect } from "@playwright/test";

// The combined /all "kitchen sink" page renders every registered specimen on one
// scrollable page (host-only showcase enhancement). These proofs run in all three
// engines: the page renders and reuses the registry bodies, its table-of-contents
// anchors navigate, and a real interactive widget (the Dialog) still opens and
// closes on the combined page — proving the per-specimen id namespacing keeps the
// controllers working when every specimen shares one document. The axe + strict-CSP
// crawls (axe.spec.ts / csp.spec.ts) also cover /all automatically via the index
// all-link.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("combined /all page", () => {
  test("renders every specimen once, grouped by section, with a table of contents", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/all");

    await expect(
      page.getByRole("heading", { level: 1, name: /all specimens/i }),
    ).toBeVisible();

    // The table of contents links each specimen; every link points at a section
    // anchor that exists exactly once on the page.
    const toc = page.locator('nav[data-slot="all-toc"]');
    await expect(toc).toBeVisible();
    const anchors = await toc
      .locator("a[href^='#']")
      .evaluateAll((els) =>
        els.map((e) => (e as HTMLAnchorElement).getAttribute("href")!),
      );
    expect(anchors.length).toBeGreaterThan(50);

    const sections = page.locator("section[data-all-specimen]");
    await expect(sections).toHaveCount(anchors.length);

    // Bodies are reused from the registry: the data-table specimen renders here.
    await expect(
      page.locator(
        'section[data-all-specimen="primitive-data-table"] table',
      ),
    ).toBeVisible();

    expect(errors, errors.join("\n")).toEqual([]);
  });

  test("table-of-contents anchors navigate to the matching specimen section", async ({
    page,
  }) => {
    await page.goto("/all");

    const target = "#all-primitive-data-table";
    await page.locator(`nav[data-slot="all-toc"] a[href="${target}"]`).click();

    await expect(page).toHaveURL(new RegExp(`${target}$`));
    const section = page.locator(
      'section[data-all-specimen="primitive-data-table"]',
    );
    await expect(section).toBeInViewport();
  });

  test("an interactive Dialog opens and closes on the combined page", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/all");
    await page.waitForFunction(() => "Alpine" in window);

    const cell = page.locator(
      'section[data-all-specimen="primitive-dialog"]',
    );
    const dialog = cell.locator('[data-slot="dialog"]').first();
    const trigger = cell.locator('[data-slot="trigger"]').first();

    await expect(dialog).toHaveAttribute("data-state", "closed");

    // Open via keyboard so focus stays on the trigger (WebKit does not focus a
    // <button> on pointer click).
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(dialog).toHaveAttribute("data-state", "open");

    // Escape dismisses.
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveAttribute("data-state", "closed");

    expect(errors, errors.join("\n")).toEqual([]);
  });
});
