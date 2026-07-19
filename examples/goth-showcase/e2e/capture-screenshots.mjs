// GOTH-2.4 Layer-4 run-and-look capture. Drives the running showcase server and
// stores curated screenshots (narrow/wide x light/dark x one RTL specimen) under
// e2e/screenshots/ as review artifacts. Not part of the CI gate; run manually.
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = join(__dirname, "screenshots");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOWCASE_URL ?? "http://127.0.0.1:8137";
const WIDE = { width: 1280, height: 900 };
const NARROW = { width: 390, height: 844 };

// Representative primitive specimens spanning the three waves.
const primitives = [
  "primitive-alert", "primitive-badge", "primitive-avatar", "primitive-card",
  "primitive-button", "primitive-button-group", "primitive-breadcrumb",
  "primitive-pagination", "primitive-separator", "primitive-field",
  "primitive-input", "primitive-input-group", "primitive-native-select",
  "primitive-table", "primitive-textarea", "primitive-progress",
  "primitive-typography", "primitive-item", "primitive-empty", "primitive-marker",
  "primitive-kbd", "primitive-label", "primitive-skeleton", "primitive-spinner",
  "primitive-aspect-ratio", "primitive-direction",
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
  }
  await page.screenshot({ path: join(outDir, file), fullPage: true });
}

const page = await browser.newPage();

// Index (wide light) + a wide/dark montage of all primitives on their own pages.
await shot(page, "/", "index-wide-light.png");

// Curated cross-product for a rich subset (wide light, wide dark, narrow light).
const rich = ["primitive-button", "primitive-card", "primitive-field",
  "primitive-table", "primitive-input-group", "primitive-typography",
  "primitive-alert", "primitive-pagination"];
for (const id of rich) {
  const name = id.replace("primitive-", "");
  await shot(page, "/specimen/" + id, `wide-light-${name}.png`);
  await shot(page, "/specimen/" + id, `wide-dark-${name}.png`, { dark: true });
  await shot(page, "/specimen/" + id, `narrow-light-${name}.png`, { viewport: NARROW });
}

// Every remaining primitive at wide light so the full wave is eyeballed once.
for (const id of primitives) {
  const name = id.replace("primitive-", "");
  await shot(page, "/specimen/" + id, `all-wide-light-${name}.png`);
}

// Theme axes: dedicated dark + RTL specimens served with real <html> attributes.
await shot(page, "/specimen/theme-dark", "theme-dark.png");
await shot(page, "/specimen/theme-rtl", "theme-rtl.png");
// The RTL primitive specimen (Direction P09) with real dir=rtl subtree.
await shot(page, "/specimen/primitive-direction", "rtl-direction.png");

await browser.close();
console.log("screenshots written to", outDir);
