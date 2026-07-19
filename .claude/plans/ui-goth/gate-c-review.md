# Gate C review record — 2026-07-17

Three-reviewer wave over the GOTH-0.4 implementation (authentication's HTML
resource-policy seam): lead-backend-engineer, architecture-steward,
platform-sre.

**Verdicts: accept (backend), accept-with-changes (architecture),
accept-with-changes (platform). No blockers.**

**Owner disposition (2026-07-17, interactive session):**

1. **Gate C accepted**, conditional on the doc/release-note remediation below.
2. **Seven-class widenable freeze ratified**: exactly
   script/style/img/font/connect/media/worker. `frame-src`/`child-src`/
   `object-src` stay excluded (they fall back to `default-src 'none'`);
   adding any class later is a recorded freeze-reopen, not a silent edit.
3. **Unsafe-source posture: document-only.** The seam stays value-neutral;
   source-value safety (`'unsafe-inline'`, `'unsafe-eval'`, `*`) is the
   host's responsibility, stated explicitly in docs. No RuntimeMode
   enforcement.

## Adjudications recorded

- The linter's "unused method `render`" finding (policy.go ~168) is a FALSE
  POSITIVE — `render` is the live non-nil rendering path
  (`buildCSP` → `writeHTMLSecurity`, four handler call sites). Keep as-is.
- The loud `ErrHTMLPolicyWithoutViews` construction posture is endorsed by
  all three reviewers (matches the `ErrInviteCheckWithoutGranter`
  convention).
- Nil-policy byte-identity was independently verified against git history
  (`282e39f:security.go`), not just the test constants.

## Binding remediation items (doc/release-note only — no code changes)

C1. `features/authentication/README.md` (HTML-surface section): add that a
non-nil policy **replaces** the default `script-src` tail entirely — a policy
omitting `script-src` (or lacking `Nonce: true`) leaves scripts governed by
`default-src 'none'`, so the bundled magic-link/reset fragment readers will
not run; include `HTMLResourceDirective{Kind: HTMLScriptSrc, Nonce: true}` to
retain them. Fail-closed by design.

C2. Same README section: state that widening is unbounded by design — the
seam validates structure (header injection, fixed/unknown kinds), not source
values; `'unsafe-inline'`/`'unsafe-eval'`/`*` acceptance is deliberate and
script hardening rests on the adapter/host (owner decision 3).

C3. `policy.go` `HTMLResourcePolicy` doc comment: note that source order
within a directive is preserved (dedup keeps first occurrence) and is
load-bearing for cross-process header stability — the GOTH-7.2 adapter must
emit sources deterministically.

C4. `RELEASING.md`: add the adopter-facing upgrade note for
`features/authentication` (new optional `Config.HTMLPolicy` +
`HTMLResourcePolicy`/`HTMLResourceDirective`/`HTMLResourceKind`/seven consts/
`NewHTMLResourcePolicy`/`ErrHTMLPolicyWithoutViews`; `nil` reproduces the
historical asset-free CSP byte-for-byte; policies only widen the seven frozen
classes and can never remove a fixed protection). Update the stale
"next tag: patch-only (internal delegation)" note for this module.

C5. `.claude/plans/ui-goth/plan.md` GOTH-7.2 task block: record the
unbudgeted contract test — a policy intended to keep bundled inline scripts
must carry `script-src` with `Nonce: true`; also note the adapter owns
deterministic source ordering (C3) and externalizing the fragment readers.

## Deferred/optional (recorded, not remediation)

- G2 `guard-feature-isolation` regex could add `|ui` for grep-level symmetry
  with G13/G17 (module-boundary check already covers it) — fold into
  GOTH-1.1's Makefile work.
- Internal `Mount` is at 8 positional params; next threaded value should
  trigger a params-struct refactor (NIT).
- Optional defense-in-depth: rejecting non-ASCII whitespace runes in
  `validateSource` if the validator ever gains a non-CSP consumer (NIT).
- No log line distinguishes "intended no-script policy" from "nonce minting
  failed"; browser-side-only visibility accepted for now (observability NIT).
- `&HTMLResourcePolicy{}` zero value is a safe second spelling of "empty"
  alongside `nil` (renders fixed-prefix-only); accepted as-is.
