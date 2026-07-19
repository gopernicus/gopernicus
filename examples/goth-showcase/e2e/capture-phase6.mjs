// GOTH-6.6 Layer-4 run-and-look capture. Drives the running showcase server and
// stores curated screenshots of the ten Phase 6 specimens (light/dark, one RTL,
// one narrow) plus a regression sample of earlier phases. Not part of the CI
// gate; run manually against a live server.
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = join(__dirname, "screenshots", "phase6");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOWCASE_URL ?? "http://127.0.0.1:8099";
const WIDE = { width: 1280, height: 900 };
const NARROW = { width: 390, height: 844 };

// The ten Phase 6 primitives, keyed to a representative specimen id.
const phase6 = [
  "primitive-attachment",      // P55
  "primitive-bubble",          // P56
  "primitive-carousel",        // P57
  "primitive-chart",           // P58
  "primitive-message",         // P59
  "primitive-message-scroller",// P60
  "primitive-resizable",       // P61
  "primitive-scroll-area",     // P62
  "primitive-sonner",          // P63
  "primitive-toast",           // P64
];

// Regression sample of earlier phases (verify Amendment 1 stylesheets still render).
const regression = [
  "primitive-alert",     // P01 (Phase 2)
  "primitive-card",      // P08
  "primitive-tabs",      // P34 (Phase 3)
  "primitive-dialog",    // P39 (Phase 4)
  "primitive-data-table",// P52 (Phase 5)
];

const browser = await chromium.launch();

async function shot(page, path, file, { dark = false, viewport = WIDE } = {}) {
  await page.setViewportSize(viewport);
  await page.goto(base + path, { waitUntil: "networkidle" });
  if (dark) {
    await page.evaluate(() => {
      document.documentElement.classList.add("dark");
      document.documentElement.setAttribute("data-theme", "dark");
    });
    await page.waitForTimeout(120);
  }
  await page.screenshot({ path: join(outDir, file), fullPage: true });
}

const page = await browser.newPage();

// Phase 6: light + dark for all ten.
for (const id of phase6) {
  const name = id.replace("primitive-", "");
  await shot(page, "/specimen/" + id, `p6-light-${name}.png`);
  await shot(page, "/specimen/" + id, `p6-dark-${name}.png`, { dark: true });
}

// Phase 6 RTL (dedicated real dir=rtl specimen) + one narrow.
await shot(page, "/specimen/primitive-resizable-rtl", "p6-rtl-resizable.png");
await shot(page, "/specimen/primitive-toast", "p6-narrow-toast.png", { viewport: NARROW });
await shot(page, "/specimen/primitive-message-scroller", "p6-narrow-message-scroller.png", { viewport: NARROW });

// Regression sample: earlier-phase specimens still render styled (light + dark).
for (const id of regression) {
  const name = id.replace("primitive-", "");
  await shot(page, "/specimen/" + id, `reg-light-${name}.png`);
  await shot(page, "/specimen/" + id, `reg-dark-${name}.png`, { dark: true });
}

await browser.close();
console.log("phase6 screenshots written to", outDir);
