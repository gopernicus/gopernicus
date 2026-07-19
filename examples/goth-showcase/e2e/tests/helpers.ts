import { Page } from "@playwright/test";

// specimenLinks crawls the showcase index for every registered specimen href plus
// the combined /all "kitchen sink" page, so new specimens — and the single-page
// gallery — are covered automatically (axe, strict-CSP) without editing the specs.
export async function specimenLinks(page: Page): Promise<string[]> {
  await page.goto("/");
  return page
    .locator("a[data-specimen], a[data-all-link]")
    .evaluateAll((els) =>
      els.map((e) => (e as HTMLAnchorElement).getAttribute("href")!),
    );
}
