// ui/goth asset build (GOTH-1.2).
//
// Produces four self-hosted, fingerprinted, production-minified assets and a
// committed manifest, then embedded by ../assets/assets.go:
//
//   theme.css          esbuild-bundled + minified reset + neutral contract +
//                      component CSS (no Tailwind, no default palette)
//   theme-default.css  esbuild-minified default light/dark palette — the kit's
//                      embedded default theme, injected by wiring when a host
//                      supplies no ThemeStylesheetPath (amendment-1 D3)
//   runtime.js         esbuild-bundled CSP-safe Alpine (+ GOTH controllers)
//   htmx.js            vendored, self-hosted htmx 2.0.10 (upstream minified dist)
//
// The build is deterministic: content-addressed fingerprints and integrity
// digests derive purely from bytes, so two runs on the same pinned toolchain are
// byte-identical. It NEVER embeds build/test tooling (axe-core is MPL-2.0,
// build/test only). Regeneration is Node-gated; `make check` diffs the committed
// dist via plain git and never invokes Node.
//
// Run: npm ci --ignore-scripts && npm run build  (from ui/goth/tools).

import { createHash } from "node:crypto";
import {
  readFileSync,
  readdirSync,
  writeFileSync,
  mkdirSync,
  unlinkSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as esbuild from "esbuild";

const toolsDir = dirname(fileURLToPath(import.meta.url));
const gothDir = join(toolsDir, "..");
const assetsDir = join(gothDir, "assets");
const distDir = join(assetsDir, "dist");
const srcCss = join(assetsDir, "src", "css", "index.css");
const srcDefaultCss = join(gothDir, "theme", "default.css");
const srcRuntime = join(assetsDir, "src", "js", "runtime.js");
const srcHtmxConfig = join(assetsDir, "src", "js", "htmx-config.js");
const htmxDist = join(toolsDir, "node_modules", "htmx.org", "dist", "htmx.min.js");

// FINGERPRINT_LEN is the hex prefix of the sha256 content hash used in the
// fingerprinted filename. Content-addressed, so identical bytes → identical name.
const FINGERPRINT_LEN = 8;

function sha256Hex(buf) {
  return createHash("sha256").update(buf).digest("hex");
}

function integritySha384(buf) {
  return "sha384-" + createHash("sha384").update(buf).digest("base64");
}

// CSS_TARGET is the browser floor esbuild lowers/prefixes the CSS against. It is
// chosen to preserve the vendor prefixes the previous Tailwind CLI (autoprefixer)
// emitted for the properties that actually matter to rendering — notably
// `-webkit-appearance` so WebKit keeps rendering the kit's custom form controls
// (checkbox/switch/radio/select/progress) instead of native chrome. It does not
// polyfill modern CSS the kit relies on (oklch, :has) — those pass through
// unchanged, exactly as under the old pipeline.
const CSS_TARGET = ["chrome111", "edge111", "firefox111", "safari15"];

// buildCssEntry bundles a CSS entry (resolving its @import graph) and minifies it
// via the pinned esbuild — the same bundler that builds runtime.js, so no
// Tailwind CLI and no separate CSS toolchain. Deterministic: identical bytes in,
// identical bytes out.
async function buildCssEntry(entry) {
  const result = await esbuild.build({
    entryPoints: [entry],
    bundle: true,
    minify: true,
    write: false,
    charset: "utf8",
    legalComments: "none",
    loader: { ".css": "css" },
    target: CSS_TARGET,
  });
  return Buffer.from(result.outputFiles[0].contents);
}

// buildCss compiles the kit stylesheet (reset + neutral contract + components);
// the default palette is a SEPARATE injected asset (buildDefaultCss), never baked
// into theme.css (amendment-1 D3).
async function buildCss() {
  return buildCssEntry(srcCss);
}

// buildDefaultCss compiles the kit's embedded default light/dark palette into its
// own fingerprinted asset. Wiring injects it as the theme-stylesheet link when a
// host supplies no ThemeStylesheetPath.
async function buildDefaultCss() {
  return buildCssEntry(srcDefaultCss);
}

// buildRuntime bundles the CSP-safe Alpine runtime entry (+ registered GOTH
// controllers and shared mechanics, GOTH-1.4) into a minified IIFE.
async function buildRuntime() {
  return bundleJS(srcRuntime);
}

// bundleJS bundles a CSP-safe IIFE from an entry point using the pinned esbuild.
async function bundleJS(entry) {
  const result = await esbuild.build({
    entryPoints: [entry],
    bundle: true,
    minify: true,
    format: "iife",
    target: ["es2019"],
    legalComments: "none",
    write: false,
    charset: "utf8",
    // Sources live under assets/src, outside tools/node_modules, so point
    // esbuild's module resolution at the pinned toolchain.
    nodePaths: [join(toolsDir, "node_modules")],
  });
  return Buffer.from(result.outputFiles[0].contents);
}

// vendorHtmx returns the single self-hosted htmx asset: the upstream-minified,
// byte-for-byte htmx 2.0.10, followed by the minified Gopernicus HTMX response
// configuration (explicit non-2xx fragment handling — GOTH-1.4, README §9). One
// asset keeps the Full profile honest; the runtime derives no security decision
// from an HTMX header.
async function vendorHtmx() {
  const upstream = readFileSync(htmxDist);
  const config = await bundleJS(srcHtmxConfig);
  return Buffer.concat([upstream, Buffer.from("\n"), config]);
}

function emit(logicalName, base, ext, bytes) {
  const fp = sha256Hex(bytes).slice(0, FINGERPRINT_LEN);
  const file = `${base}.${fp}.${ext}`;
  writeFileSync(join(distDir, file), bytes);
  return {
    logicalName,
    path: `dist/${file}`,
    integrity: integritySha384(bytes),
    bytes: bytes.length,
    // provenance (build-time only; not written to the Go manifest)
    sha256: sha256Hex(bytes),
  };
}

async function main() {
  // Clean dist so a removed/renamed asset never lingers and drift is honest.
  mkdirSync(distDir, { recursive: true });
  for (const f of readdirSync(distDir)) {
    if (f === ".gitkeep") continue;
    unlinkSync(join(distDir, f));
  }

  const css = await buildCss();
  const defaultCss = await buildDefaultCss();
  const runtime = await buildRuntime();
  const htmx = await vendorHtmx();

  const assets = [
    emit("theme.css", "theme", "css", css),
    emit("theme-default.css", "theme-default", "css", defaultCss),
    emit("runtime.js", "runtime", "js", runtime),
    emit("htmx.js", "htmx", "js", htmx),
  ];

  // Deterministic manifest: sorted by logical name; keys match the Go Asset
  // struct (case-insensitive JSON) minus the build-only sha256 field.
  assets.sort((a, b) => (a.logicalName < b.logicalName ? -1 : 1));
  const manifest = {
    assets: assets.map(({ logicalName, path, integrity, bytes }) => ({
      logicalName,
      path,
      integrity,
      bytes,
    })),
  };
  writeFileSync(
    join(assetsDir, "manifest.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );

  // Build-time provenance for THIRD_PARTY_NOTICES.md (stdout only).
  const htmxUpstreamSha = sha256Hex(readFileSync(htmxDist));
  console.log("ui/goth asset build complete:");
  for (const a of assets) {
    console.log(
      `  ${a.logicalName.padEnd(11)} ${a.path.padEnd(28)} ${a.bytes
        .toString()
        .padStart(7)}B  ${a.integrity}`,
    );
    console.log(`  ${" ".repeat(11)} sha256=${a.sha256}`);
  }
  console.log(`  vendored htmx.min.js (upstream 2.0.10) sha256=${htmxUpstreamSha}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
