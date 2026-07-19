// GOTH-4.5 Layer-4 run-and-look capture for the Phase 4 overlay/navigation
// primitives (P37–P48). Drives the running showcase server and stores curated
// screenshots of the meaningful states — open dialogs/sheets/drawers with scrim,
// anchored popovers/menus with submenus open, light/dark, and one RTL menu —
// under e2e/screenshots/phase4/. Not part of the CI gate; run manually with the
// showcase server up.
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outDir = join(__dirname, "screenshots", "phase4");
mkdirSync(outDir, { recursive: true });

const base = process.env.SHOWCASE_URL ?? "http://127.0.0.1:8099";
const WIDE = { width: 1280, height: 900 };

const browser = await chromium.launch();
const page = await browser.newPage();

async function open(path, { dark = false, rtl = false } = {}) {
  await page.setViewportSize(WIDE);
  await page.goto(base + path, { waitUntil: "networkidle" });
  if (dark) {
    await page.evaluate(() => {
      document.documentElement.classList.add("dark");
      document.documentElement.setAttribute("data-theme", "dark");
    });
  }
  if (rtl) {
    await page.evaluate(() => document.documentElement.setAttribute("dir", "rtl"));
  }
  // Let Alpine boot for the Interactive specimens.
  await page.waitForTimeout(200);
}

async function snap(file) {
  await page.screenshot({ path: join(outDir, file), fullPage: true });
}

async function click(sel) {
  await page.locator(sel).first().click();
  await page.waitForTimeout(200);
}

// --- Modal/panel primitives: open with scrim, light + dark. ---
// Dialog (P39): open modal over scrim + nested dialog.
await open("/specimen/mechanics-dialog");
await click("#dialog-trigger");
await snap("dialog-open-light.png");
await click("#nested-trigger");
await snap("dialog-nested-open-light.png");
await open("/specimen/mechanics-dialog", { dark: true });
await click("#dialog-trigger");
await snap("dialog-open-dark.png");

// Alert Dialog (P37): destructive decision, scrim does not dismiss.
await open("/specimen/primitive-alert-dialog");
await click("#alert-trigger");
await snap("alert-dialog-open-light.png");

// Sheet (P47) + Drawer (P40): edge panels with scrim.
await open("/specimen/primitive-sheet");
await click("#sheet-trigger");
await snap("sheet-open-light.png");
await open("/specimen/primitive-drawer");
await click("#drawer-trigger");
await snap("drawer-open-light.png");
await open("/specimen/primitive-drawer", { dark: true });
await click("#drawer-trigger");
await snap("drawer-open-dark.png");

// Server-open StylesOnly baselines (no-JS readable panels).
await open("/specimen/primitive-dialog-open");
await snap("dialog-server-open-nojs-light.png");
await open("/specimen/primitive-sheet-open");
await snap("sheet-server-open-nojs-light.png");

// --- Anchored info/selection primitives. ---
// Popover (P45): anchored panel open.
await open("/specimen/primitive-popover");
await click("#popover-trigger");
await snap("popover-open-light.png");
// Hover Card (P42): open on hover.
await open("/specimen/primitive-hover-card");
await page.locator("#hover-card-trigger").hover();
await page.waitForTimeout(400);
await snap("hover-card-open-light.png");
// Tooltip (P48): open on focus.
await open("/specimen/primitive-tooltip");
await page.locator("#tooltip-trigger").focus();
await page.waitForTimeout(300);
await snap("tooltip-open-light.png");
// Select (P46): native, light + dark.
await open("/specimen/primitive-select");
await snap("select-light.png");
await open("/specimen/primitive-select", { dark: true });
await snap("select-dark.png");

// --- Menu primitives with submenu open, light/dark/RTL. ---
// Dropdown Menu (P41): open + submenu open.
await open("/specimen/mechanics-menu");
await click("#menu-trigger");
await snap("dropdown-open-light.png");
await page.locator("#submenu-trigger").hover();
await page.waitForTimeout(250);
await snap("dropdown-submenu-open-light.png");
await open("/specimen/mechanics-menu", { dark: true });
await click("#menu-trigger");
await snap("dropdown-open-dark.png");
// RTL menu.
await open("/specimen/mechanics-menu-rtl");
await click("#menu-trigger");
await snap("dropdown-open-rtl.png");
// Menubar (P43): open a menu.
await open("/specimen/primitive-menubar");
await click("#mb-file");
await snap("menubar-open-light.png");
// Context Menu (P38): right-click to open at pointer.
await open("/specimen/primitive-context-menu");
await page.locator("#context-region").click({ button: "right", position: { x: 60, y: 30 } });
await page.waitForTimeout(200);
await snap("context-menu-open-light.png");
// Dropdown server-open (no-JS baseline).
await open("/specimen/primitive-dropdown-menu-open");
await snap("dropdown-server-open-nojs-light.png");
// Navigation Menu (P44): native details disclosure (no-JS).
await open("/specimen/primitive-navigation-menu");
await snap("navigation-menu-light.png");

await browser.close();
console.log("phase4 screenshots written to", outDir);
