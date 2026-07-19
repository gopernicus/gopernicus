// GOTH-3.4 Layer-4 run-and-look capture for the Phase 3 stateful primitives
// (P27–P36). Drives the running showcase server and stores curated screenshots
// of the meaningful states — open/closed, checked/unchecked, active-tab, light/
// dark, and RTL — under e2e/screenshots/phase3/. Not part of the CI gate; run
// manually with the showcase server up.
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = join(__dirname, "screenshots", "phase3");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOWCASE_URL ?? "http://127.0.0.1:8099";
const WIDE = { width: 1280, height: 900 };

const browser = await chromium.launch();
const page = await browser.newPage();

async function open(path, { dark = false } = {}) {
  await page.setViewportSize(WIDE);
  await page.goto(base + path, { waitUntil: "networkidle" });
  if (dark) {
    await page.evaluate(() => {
      document.documentElement.classList.add("dark");
      document.documentElement.setAttribute("data-theme", "dark");
    });
  }
  // Let Alpine boot for the Interactive specimens.
  await page.waitForTimeout(150);
}

async function snap(file) {
  await page.screenshot({ path: join(outDir, file), fullPage: true });
}

// Disclosure — Accordion: default (first item open), then open Section B (multiple).
await open("/specimen/primitive-accordion");
await snap("accordion-light-default.png");
await open("/specimen/primitive-accordion", { dark: true });
await snap("accordion-dark-default.png");

// Collapsible: closed + server-open baseline (both states visible on one page).
await open("/specimen/primitive-collapsible");
await snap("collapsible-light-closed.png");
await page.locator('[data-slot="collapsible-trigger"]').first().click();
await page.waitForTimeout(120);
await snap("collapsible-light-open.png");

// Tabs LTR: default active (account), then switch to Password.
await open("/specimen/primitive-tabs");
await snap("tabs-light-account.png");
await page.locator("#tab-password").click();
await page.waitForTimeout(120);
await snap("tabs-light-password.png");
await open("/specimen/primitive-tabs", { dark: true });
await snap("tabs-dark-account.png");

// Tabs RTL: default active + after ArrowLeft advances to the next (visually-left) tab.
await open("/specimen/primitive-tabs-rtl");
await snap("tabs-rtl-account.png");
await page.locator("#tab-account").focus();
await page.keyboard.press("ArrowLeft");
await page.waitForTimeout(120);
await snap("tabs-rtl-password.png");

// Selection — checked/unchecked shown together on each specimen page, light + dark.
for (const id of ["checkbox", "switch", "radio-group", "slider"]) {
  await open("/specimen/primitive-" + id);
  await snap(`${id}-light.png`);
  await open("/specimen/primitive-" + id, { dark: true });
  await snap(`${id}-dark.png`);
}

// Compact — OTP, Toggle (pressed/unpressed), Toggle Group (single/multiple).
for (const id of ["input-otp", "toggle", "toggle-group"]) {
  await open("/specimen/primitive-" + id);
  await snap(`${id}-light.png`);
  await open("/specimen/primitive-" + id, { dark: true });
  await snap(`${id}-dark.png`);
}

await browser.close();
console.log("phase3 screenshots written to", outDir);
