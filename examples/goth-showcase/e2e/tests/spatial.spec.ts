import { test, expect } from "@playwright/test";

// GOTH-6.3 spatial primitives (P57 Carousel, P61 Resizable, P62 Scroll Area):
// real-browser proof across all three engines. Carousel and Scroll Area ship
// native/no-controller, so those assertions hold with Alpine absent. Resizable has a
// server-owned StylesOnly split baseline (no JS, no server-rendered style attribute)
// enhanced by the gothResizable controller: pointer drag, APG keyboard resize,
// bounds clamping, and RTL-aware arrow direction, all writing geometry through the
// CSSOM custom property.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Scroll Area (P62) — native overflow, no JavaScript", () => {
  test("regions are focusable, named, and actually overflow", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-scroll-area");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const vertical = page.locator(
      '[data-slot="scroll-area"][data-orientation="vertical"]',
    );
    const horizontal = page.locator(
      '[data-slot="scroll-area"][data-orientation="horizontal"]',
    );
    await expect(vertical).toHaveAttribute("tabindex", "0");
    await expect(vertical).toHaveAttribute("aria-label", "Release notes");
    await expect(horizontal).toHaveAttribute("aria-label", "Tags");

    // The vertical region overflows on the block axis, the horizontal on the
    // inline axis.
    expect(
      await vertical.evaluate((el) => el.scrollHeight > el.clientHeight),
    ).toBe(true);
    expect(
      await horizontal.evaluate((el) => el.scrollWidth > el.clientWidth),
    ).toBe(true);

    // No primitive emits an inline style=.
    expect(await page.locator("[style]").count()).toBe(0);
    expect(errors).toEqual([]);
  });

  test("the region scrolls with the keyboard", async ({ page }) => {
    await page.goto("/specimen/primitive-scroll-area");
    const vertical = page.locator(
      '[data-slot="scroll-area"][data-orientation="vertical"]',
    );
    await vertical.focus();
    await expect(vertical).toBeFocused();
    const before = await vertical.evaluate((el) => el.scrollTop);
    await page.keyboard.press("End");
    await expect
      .poll(async () => vertical.evaluate((el) => el.scrollTop))
      .toBeGreaterThan(before);
  });
});

test.describe("Carousel (P57) — scroll-snap + no-JS anchor dots", () => {
  test("the track is a focusable named snap region that overflows", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-carousel");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const track = page
      .locator('[data-slot="carousel-content"]')
      .first();
    await expect(track).toHaveAttribute("tabindex", "0");
    await expect(track).toHaveAttribute("aria-label", "Slides");

    // The carousel region announces itself and holds slides.
    const region = page.locator('[data-slot="carousel"]').first();
    await expect(region).toHaveAttribute("aria-roledescription", "carousel");
    await expect(
      page.locator('[data-slot="carousel-item"]').first(),
    ).toHaveAttribute("aria-roledescription", "slide");

    // The horizontal track overflows on the inline axis and uses mandatory snap.
    expect(
      await track.evaluate((el) => el.scrollWidth > el.clientWidth),
    ).toBe(true);
    expect(
      await track.evaluate((el) =>
        getComputedStyle(el).scrollSnapType.startsWith("x"),
      ),
    ).toBe(true);

    expect(await page.locator("[style]").count()).toBe(0);
    expect(errors).toEqual([]);
  });

  test("the current dot is marked and a dot link scrolls its slide into view", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-carousel");
    const track = page.locator('[data-slot="carousel-content"]').first();

    // The server-chosen current dot carries aria-current.
    await expect(
      page.locator('[data-slot="carousel-dot"][data-current="true"]'),
    ).toHaveAttribute("aria-current", "true");

    // Clicking the third dot navigates to #carousel-slide-3 with no JavaScript and
    // scrolls the track so that slide is in view.
    const before = await track.evaluate((el) => el.scrollLeft);
    await page.getByRole("link", { name: "Go to slide 3" }).click();
    await expect(page).toHaveURL(/#carousel-slide-3$/);
    await expect
      .poll(async () => track.evaluate((el) => el.scrollLeft))
      .toBeGreaterThan(before);
  });
});

test.describe("Resizable (P61) — server split baseline, no JavaScript", () => {
  test("the split renders from server data with no JavaScript and no style attribute", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-resizable");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const root = page.locator('[data-slot="resizable"]');
    await expect(root).toHaveAttribute("data-default-size", "40");
    // No controller ran: no data-enhanced, and no server-rendered style attribute
    // (the geometry comes from data-default-size + external CSS).
    await expect(root).not.toHaveAttribute("data-enhanced", "true");
    expect(await page.locator("[style]").count()).toBe(0);

    const handle = page.locator('[data-slot="resizable-handle"]');
    await expect(handle).toHaveAttribute("role", "separator");
    await expect(handle).toHaveAttribute("aria-valuenow", "40");
    await expect(handle).toHaveAttribute("aria-controls", "rz-baseline-primary");

    // The primary pane really occupies ~40% of the container width.
    const ratio = await root.evaluate((el) => {
      const primary = el.querySelector('[data-slot="resizable-pane"]');
      return primary.getBoundingClientRect().width / el.getBoundingClientRect().width;
    });
    expect(ratio).toBeGreaterThan(0.3);
    expect(ratio).toBeLessThan(0.5);

    expect(errors).toEqual([]);
  });
});

test.describe("Resizable (P61) — gothResizable drag + keyboard", () => {
  test("keyboard resizes the split and clamps to the bounds", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-resizable-drag");
    const root = page.locator("#rz-h");
    await expect(root).toHaveAttribute("data-enhanced", "true");
    const handle = root.locator('[data-slot="resizable-handle"]');

    await handle.focus();
    await expect(handle).toBeFocused();

    // ArrowRight grows the primary (left) pane in LTR; ArrowLeft shrinks it.
    await handle.press("ArrowRight");
    await expect(handle).toHaveAttribute("aria-valuenow", "45");
    await handle.press("ArrowLeft");
    await handle.press("ArrowLeft");
    await expect(handle).toHaveAttribute("aria-valuenow", "35");

    // End clamps to the max bound, Home to the min bound (15%–85%).
    await handle.press("End");
    await expect(handle).toHaveAttribute("aria-valuenow", "85");
    await handle.press("ArrowRight"); // already at max — stays clamped
    await expect(handle).toHaveAttribute("aria-valuenow", "85");
    await handle.press("Home");
    await expect(handle).toHaveAttribute("aria-valuenow", "15");

    expect(errors).toEqual([]);
  });

  test("pointer drag resizes the split via the CSSOM custom property", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-resizable-drag");
    const root = page.locator("#rz-h");
    await expect(root).toHaveAttribute("data-enhanced", "true");
    const handle = root.locator('[data-slot="resizable-handle"]');

    const before = Number(await handle.getAttribute("aria-valuenow"));
    const box = await handle.boundingBox();
    if (!box) throw new Error("no handle box");
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 120, box.y + box.height / 2, {
      steps: 8,
    });
    await page.mouse.up();

    // A rightward drag grew the primary pane; the geometry lives in the CSSOM
    // custom property (a controller-owned write, not a server-rendered style).
    const after = Number(await handle.getAttribute("aria-valuenow"));
    expect(after).toBeGreaterThan(before);
    const basis = await root.evaluate((el) =>
      el.style.getPropertyValue("--goth-resize-basis"),
    );
    expect(basis).toMatch(/%$/);
  });

  test("the vertical split resizes with ArrowUp/ArrowDown", async ({ page }) => {
    await page.goto("/specimen/primitive-resizable-drag");
    const root = page.locator("#rz-v");
    await expect(root).toHaveAttribute("data-enhanced", "true");
    const handle = root.locator('[data-slot="resizable-handle"]');
    await handle.focus();
    await handle.press("ArrowDown");
    await expect(handle).toHaveAttribute("aria-valuenow", "55");
    await handle.press("ArrowUp");
    await handle.press("ArrowUp");
    await expect(handle).toHaveAttribute("aria-valuenow", "45");
  });
});

test.describe("Resizable (P61) — RTL arrow direction", () => {
  test("ArrowLeft grows the primary pane under dir=rtl", async ({ page }) => {
    await page.goto("/specimen/primitive-resizable-rtl");
    const root = page.locator("#rz-rtl");
    await expect(root).toHaveAttribute("data-enhanced", "true");
    const handle = root.locator('[data-slot="resizable-handle"]');
    await handle.focus();

    // In RTL the primary pane is on the inline-end (right) edge; ArrowLeft moves the
    // separator visually left, which grows the right-side primary.
    await handle.press("ArrowLeft");
    await expect(handle).toHaveAttribute("aria-valuenow", "45");
    await handle.press("ArrowRight");
    await handle.press("ArrowRight");
    await expect(handle).toHaveAttribute("aria-valuenow", "35");
  });
});
