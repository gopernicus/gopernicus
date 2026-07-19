import { test, expect } from "@playwright/test";

// GOTH-6.4 Chart (P58): real-browser proof across all three engines. The chart is
// server-rendered SVG with no controller and no charting library, so every
// assertion holds with Alpine absent on the StylesOnly profile. The geometry rides
// sanctioned SVG presentation attributes and the series color rides the data-series
// hook + the frozen chart-N tokens — never a server-rendered style attribute.

function guard(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  return errors;
}

test.describe("Chart (P58) — server-rendered SVG, no JavaScript", () => {
  test("the bar chart is a named role=img SVG that renders with no JS or style attribute", async ({
    page,
  }) => {
    const errors = guard(page);
    await page.goto("/specimen/primitive-chart");
    expect(await page.evaluate(() => "Alpine" in window)).toBe(false);

    const svg = page.locator('#chart-bar [data-slot="chart-svg"]');
    await expect(svg).toHaveAttribute("role", "img");
    await expect(svg).toHaveAttribute(
      "aria-labelledby",
      "chart-bar-title chart-bar-desc",
    );
    // The <title>/<desc> name the graphic.
    await expect(svg.locator("title#chart-bar-title")).toHaveText(
      "Quarterly revenue (bar)",
    );

    // Eight bars (two series × four quarters), all with geometry attributes, keyed
    // to the chart-N series tokens.
    const bars = svg.locator("rect.goth-chart-bar");
    await expect(bars).toHaveCount(8);
    const first = bars.first();
    for (const attr of ["x", "y", "width", "height"]) {
      expect(await first.getAttribute(attr)).not.toBeNull();
    }

    // Native per-mark tooltip.
    await expect(bars.first().locator("title")).toHaveText(/This year — Q1: 40/);

    // No server-rendered style attribute anywhere on the page.
    expect(await page.locator("[style]").count()).toBe(0);
    expect(errors).toEqual([]);
  });

  test("the line and area charts emit polylines, an area path, and data dots", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-chart");

    const line = page.locator("#chart-line [data-slot='chart-svg']");
    await expect(line.locator("polyline.goth-chart-line")).toHaveCount(2);
    await expect(line.locator("circle.goth-chart-dot")).toHaveCount(8);
    await expect(line.locator("path.goth-chart-area")).toHaveCount(0);

    const area = page.locator("#chart-area [data-slot='chart-svg']");
    await expect(area.locator("path.goth-chart-area")).toHaveCount(2);
    await expect(area.locator("polyline.goth-chart-line")).toHaveCount(2);
  });

  test("series color comes from the chart-N tokens via CSS, not a rendered fill", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-chart");
    const svg = page.locator('#chart-bar [data-slot="chart-svg"]');

    // No per-mark fill= attribute; color is CSS-only through data-series.
    const s1 = svg.locator('rect[data-series="1"]').first();
    const s2 = svg.locator('rect[data-series="2"]').first();
    expect(await s1.getAttribute("fill")).toBeNull();

    const fill1 = await s1.evaluate((el) => getComputedStyle(el).fill);
    const fill2 = await s2.evaluate((el) => getComputedStyle(el).fill);
    // Both are real colors and the two series differ (distinct chart tokens).
    expect(fill1).not.toBe("none");
    expect(fill1).not.toBe("rgb(0, 0, 0)");
    expect(fill1).not.toBe(fill2);
  });

  test("the legend keys the series and the table fallback discloses the data with no JS", async ({
    page,
  }) => {
    await page.goto("/specimen/primitive-chart");
    const chart = page.locator("#chart-bar");

    // Legend items are keyed to the same series tokens as the bars.
    await expect(
      chart.locator('[data-slot="chart-legend-item"]'),
    ).toHaveCount(2);
    await expect(
      chart.locator('[data-slot="chart-legend-item"][data-series="1"]'),
    ).toContainText("This year");

    // The tabular fallback is a native <details> — closed initially, opened by the
    // summary with no JavaScript, exposing the same numbers as the SVG.
    const details = chart.locator("details.goth-chart-table");
    await expect(details).not.toHaveAttribute("open", "");
    const table = details.locator("table");
    await expect(table).toBeHidden();
    await chart.getByText("View data as table").click();
    await expect(table).toBeVisible();
    await expect(table.locator("tbody tr")).toHaveCount(4);
    await expect(table).toContainText("100");
  });
});
