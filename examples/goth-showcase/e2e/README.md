# goth showcase — three-engine browser + accessibility harness (GOTH-1.5)

Pinned Playwright + axe harness that drives the zero-datastore `goth-showcase`
host in Chromium, Firefox, and WebKit. It carries the deferred real-browser
proofs from GOTH-1.3/1.4: strict-CSP smoke with zero console/CSP violations,
HTMX full/fragment/error/history diagnostics, no-`unsafe-eval` and
no-remote-origin proven in a live browser, and an axe accessibility pass on every
specimen — the Gate A ratified three-engine release gate.

## What runs where

- **Go suite** (`internal/showcase/*_test.go`) — hermetic `httptest` checks
  (routes, CSP mapping, host-theme link ordering + cascade and the `text/css`
  stylesheet response, HTMX status codes, registry completeness). Runs in
  `make check` / `make test`. No Node.
- **Browser suite** (this directory) — Node-gated. Runs only behind
  `make test-ui-browser`, never in the hermetic `make check` loop.

## Toolchain isolation (axe-core is MPL-2.0)

`axe-core` and `@axe-core/playwright` are **MPL-2.0** and live ONLY in this dev
toolchain's `node_modules`. They are never imported by `ui/goth`, never embedded
into `ui/goth/assets/dist`, and never reach a Go consumer's binary. This
directory is the boundary. Versions are exact-pinned in `package.json`; the
committed `package-lock.json` is installed with `npm ci`.

## Running locally

```sh
cd examples/goth-showcase/e2e
npm ci
npx playwright install chromium firefox webkit   # first run only; see cache note
npm test                                          # or: make test-ui-browser (from repo root)
```

Playwright starts the Go server itself (`webServer: go run ./cmd/server`, cwd
`..`) on `127.0.0.1:8099` (override with `SHOWCASE_PORT`) and waits for it before
the specs run.

## CI job, trigger, browser-binary cache, and flake policy (Gate B GOTH-1.5 record)

- **CI job / trigger.** Job name `ui-goth-browser`. It is **not** part of the
  default `make check` gate. Trigger: pull requests that touch `ui/goth/**` or
  `examples/goth-showcase/**`, plus a nightly `schedule` run and manual
  `workflow_dispatch`. The job runs `make test-ui-browser` on a runner with the
  pinned Node (`24.0.1`) and Go toolchains.
- **Browser-binary cache.** Playwright browsers are cached by the runner keyed on
  the exact `@playwright/test` version pinned in `package-lock.json`
  (`~/Library/Caches/ms-playwright` on macOS, `~/.cache/ms-playwright` on Linux).
  Cache key: `playwright-${{ runner.os }}-<playwright-version-from-lockfile>`. On
  a cache miss the job runs `npx playwright install --with-deps chromium firefox
  webkit`; on a hit it skips the download. The pinned Playwright `1.60.0` maps to
  browser builds chromium 1223 / firefox 1522 / webkit 2287.
- **Bounded retry / flake policy.** `retries: 2` in CI (3 attempts total per
  spec), `0` locally; `workers: 1` in CI so the single Go `webServer` is not
  overwhelmed and results are deterministic across the three engines. Retries are
  recorded in the HTML/`github` reporters, never hidden. A spec that fails all 3
  attempts fails the gate. `forbidOnly` is set in CI so a stray `test.only` fails
  the job rather than silently narrowing coverage. Network downloads (browser
  binaries) are the only non-deterministic step and are removed from the hot path
  by the cache above.

## Files

- `playwright.config.ts` — three engine projects, the Go `webServer`, retry/flake
  policy.
- `tests/csp.spec.ts` — strict-CSP smoke, no-`unsafe-eval`, no-remote-origin.
- `tests/runtime.spec.ts` — per-profile asset loading; Alpine CSP boot +
  `gothCollapse` toggle.
- `tests/htmx.spec.ts` — success / 422 validation / 500 error / history push +
  full-document re-fetch.
- `tests/axe.spec.ts` — axe pass on every specimen + reduced-motion.
- `tests/helpers.ts` — crawls the showcase index so new specimens are covered
  automatically.
