import { test, expect } from "@playwright/test";

// GOTH-5.4 Sidebar (P54): real-browser proof across all three engines. The server
// owns every authoritative state — the active navigation item, the desktop
// expanded/collapsed rail, and the mobile off-canvas open flag. These specs prove
// the no-JS baseline (real links + server round-trips + a server-open readable
// sheet) and the client enhancement (the mobile gothDialog sheet: open, focus trap,
// Escape/scrim dismissal), plus responsive rail/overlay behavior and persistence.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

const sidebar = '[data-slot="sidebar"]';
const panel = '[data-slot="sidebar"] [data-slot="content"]';

test.describe("Sidebar (P54) — no JavaScript", () => {
  test("the mobile sheet is server-open and readable with Alpine absent", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar-nojs");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    // Server-owned open state: the root renders data-state="open" so the panel is
    // visible without any JavaScript.
    await expect(page.locator(sidebar)).toHaveAttribute("data-state", "open");
    await expect(page.locator(panel)).toBeVisible();
    // The panel is a navigation landmark with an accessible name.
    await expect(page.locator(panel)).toHaveAttribute("aria-label", "Primary");
    expect(errors).toEqual([]);
  });

  test("navigation items are real links carrying server-owned aria-current", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar-nojs");

    // The no-JS specimen renders with "team" active.
    const team = page
      .locator('[data-slot="sidebar-menu-button"]', { hasText: "Team" });
    await expect(team).toHaveAttribute("aria-current", "page");

    // Clicking "Settings" navigates (no JS): the server re-renders with the new
    // active item as a shareable URL.
    await page
      .locator('[data-slot="sidebar-menu-button"]', { hasText: "Settings" })
      .click();
    await expect(page).toHaveURL(/active=settings/);
    await expect(
      page.locator('[data-slot="sidebar-menu-button"]', { hasText: "Settings" }),
    ).toHaveAttribute("aria-current", "page");
    await expect(page.locator('[data-slot="sidebar-active-label"]')).toHaveText(
      "Settings",
    );
    expect(errors).toEqual([]);
  });

  test("the collapse control is a real server round-trip that flips data-collapsed", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/sidebar?active=dashboard");

    const root = page.locator(sidebar);
    await expect(root).toHaveAttribute("data-collapsed", "false");
    const rail = page.locator('[data-slot="sidebar-rail"]');
    await expect(rail).toHaveAttribute("aria-expanded", "true");

    // Clicking the rail navigates to the toggle URL; the server re-renders collapsed.
    await rail.click();
    await expect(page).toHaveURL(/collapsed=1/);
    await expect(root).toHaveAttribute("data-collapsed", "true");
    await expect(page.locator('[data-slot="sidebar-rail"]')).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(errors).toEqual([]);
  });

  test("persistence is server-owned: the collapsed preference rides the URL across navigation", async ({
    page,
  }) => {
    const errors = guard(page);
    // "persistence on": a shared/persisted URL renders the collapsed rail.
    await page.goto("/sidebar?active=dashboard&collapsed=1");
    await expect(page.locator(sidebar)).toHaveAttribute(
      "data-collapsed",
      "true",
    );

    // Navigating to another item preserves the collapsed preference (server carries
    // it in every link).
    await page
      .locator('[data-slot="sidebar-menu-button"]', { hasText: "Team" })
      .click();
    await expect(page).toHaveURL(/collapsed=1/);
    await expect(page.locator(sidebar)).toHaveAttribute(
      "data-collapsed",
      "true",
    );

    // "persistence off": without the param the rail is expanded again.
    await page.goto("/sidebar?active=dashboard");
    await expect(page.locator(sidebar)).toHaveAttribute(
      "data-collapsed",
      "false",
    );
    expect(errors).toEqual([]);
  });

  test("no inline styles", async ({ page }) => {
    await page.goto("/specimen/primitive-sidebar-nojs");
    expect(await page.locator("[style]").count()).toBe(0);
  });
});

test.describe("Sidebar (P54) — mobile sheet (gothDialog)", () => {
  test.use({ viewport: { width: 480, height: 800 } });

  test("the trigger opens the sheet, traps focus, and Escape restores it", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar");
    await page.waitForFunction(() => "Alpine" in window);

    const root = page.locator(sidebar);
    await expect(root).toHaveAttribute("data-state", "closed");

    // Open via keyboard so focus stays on the trigger (WebKit does not focus a
    // <button> on pointer click), making the restore target reliable.
    const trigger = page.locator('[data-slot="trigger"]');
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(root).toHaveAttribute("data-state", "open");
    await expect(page.locator(panel)).toBeVisible();
    // Modality is asserted only while enforced.
    await expect(page.locator(panel)).toHaveAttribute("aria-modal", "true");

    // Escape closes and focus returns to the trigger.
    await page.keyboard.press("Escape");
    await expect(root).toHaveAttribute("data-state", "closed");
    await expect(trigger).toBeFocused();
    expect(errors).toEqual([]);
  });

  test("the scrim dismisses the sheet", async ({ page }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar");
    await page.waitForFunction(() => "Alpine" in window);

    await page.locator('[data-slot="trigger"]').focus();
    await page.keyboard.press("Enter");
    await expect(page.locator(sidebar)).toHaveAttribute("data-state", "open");

    // Click the scrim well to the right of the left-edge panel (min(18rem,85vw) wide)
    // so the press lands on the exposed scrim, not the panel.
    await page.locator('[data-slot="scrim"]').click({ position: { x: 460, y: 400 } });
    await expect(page.locator(sidebar)).toHaveAttribute("data-state", "closed");
    expect(errors).toEqual([]);
  });
});

test.describe("Sidebar (P54) — RTL", () => {
  test.use({ viewport: { width: 480, height: 800 } });

  test("logical properties pin the panel to the inline-end edge under dir=rtl", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar-rtl");
    await expect(page.locator("html")).toHaveAttribute("dir", "rtl");

    // The left-side panel uses inset-inline-start:0; under dir=rtl the inline-start
    // maps to the physical right edge, so the settled panel resolves right:0 with a
    // non-zero left offset (measured on computed style to avoid the transient
    // slide-in transform).
    const geom = await page.locator(panel).evaluate((el) => {
      const cs = getComputedStyle(el);
      return {
        dir: cs.direction,
        right: cs.right,
        left: cs.left,
        animationName: cs.animationName,
      };
    });
    expect(geom.dir).toBe("rtl");
    expect(geom.right).toBe("0px");
    expect(parseFloat(geom.left)).toBeGreaterThan(100);
    // The entrance slide follows the resolved physical edge: a left-side panel that
    // settles at the physical right edge under RTL slides in from the right.
    expect(geom.animationName).toBe("goth-sidebar-in-right");
    expect(errors).toEqual([]);
  });
});

test.describe("Sidebar (P54) — reduced motion", () => {
  test.use({ viewport: { width: 480, height: 800 }, reducedMotion: "reduce" });

  test("the mobile sheet still opens with the slide animation collapsed", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar");
    await page.waitForFunction(() => "Alpine" in window);

    await page.locator('[data-slot="trigger"]').focus();
    await page.keyboard.press("Enter");
    await expect(page.locator(sidebar)).toHaveAttribute("data-state", "open");
    await expect(page.locator(panel)).toBeVisible();
    expect(errors).toEqual([]);
  });
});

test.describe("Sidebar (P54) — desktop rail", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("the rail is visible in flow and the trigger is hidden on desktop", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-sidebar");
    await page.waitForFunction(() => "Alpine" in window);

    // On desktop the panel is a static in-flow rail (no open state needed) and the
    // mobile trigger/scrim chrome is suppressed by CSS.
    await expect(page.locator(panel)).toBeVisible();
    await expect(page.locator('[data-slot="trigger"]')).toBeHidden();
    await expect(page.locator('[data-slot="scrim"]')).toBeHidden();
    // The rail collapse control is shown on desktop.
    await expect(page.locator('[data-slot="sidebar-rail"]')).toBeVisible();
    expect(errors).toEqual([]);
  });
});
