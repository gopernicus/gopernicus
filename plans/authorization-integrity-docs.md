# Authorization documentation completion

Status: COMPLETE. Main remains at 22c712e2100ff58e0b8a3a53528a9b43ae69c942;
prior inbound/integrity implementation and these docs are uncommitted.

## Scope

Audit current reference docs, published documentation sources, schema/adoption
guidance and the auth-cms example for the current authorization/integrity API.
Keep principal admission inbound, data rules in logic and atomic enforcement in
stores; explain the deliberate admission/revocation boundary. Correct examples
against the source. Add missing denial customization and logging guidance and a
compact host API upgrade map. Preserve historical release/audit/benchmark and
executed-plan evidence. No Go implementation, migrations, dependencies or release.

Known gaps: nil mutation constructor/component documentation, old granter receipt
and semantic-conflict language, last-admin enforcement placed in access policy,
missing web-doc invitation preparation/inbound policy guidance, old example
callback name, and missing 404/logging documentation.

## Tasks

- [x] D1 Inspect active docs and code; named architecture review of boundary prose.
- [x] D2 Correct reference and site docs, adoption guidance and example pointers.
- [x] D3 Build/typecheck docs, verify examples/links and preserve a completion record.

## Verification and preservation

Use installed documentation package scripts (pnpm lock/package-manager contract;
installed CLI fallback if bootstrap unavailable), build and typecheck. Verify
Go snippets with the current API without editing production Go. Inspect generated
HTML for changed content and run git diff --check. No full Go suite rerun for
Markdown-only edits; prior full verification remains its historical checkpoint.
Preserve owner plans/cacher-design.md, plans/gps-360-go-audit-upgrade-handoff.md,
plans/segovia-v2-audit-upgrade-handoff.md and existing .github/scripts/__pycache__.
Evidence: /tmp/gopernicus-authorization-docs; completed plan/summary under plans/.

## Completion

Updated eight existing Markdown files: authorization/authentication reference
READMEs, auth-cms README, both pocket documentation pages, the hexagonal host
guide, authorization stores/UPGRADE.md and the current RELEASING.md entry.
The adoption map identifies removed APIs and current replacements. The docs now
distinguish admission from serialized integrity and from authentication proof;
explain admitted-write revocation semantics; correct invitation and admin examples;
and document per-policy denial handlers and service decision logging.

The named architecture steward reviewed the final prose and found no remaining
scoped issues. Documentation production build and TypeScript typecheck passed
using installed CLIs matching package scripts. A temporary Go harness compiled
and exercised the documented admin check, HTTP 404 denial hook, DEBUG logging,
nil writer behavior and last-owner integrity. Generated HTML content/anchors,
the release adoption link and git diff --check passed. The durable verification
record includes the harness source and results.

No production Go, SQL or dependency changes in this documentation pass. The
starting-file snapshot confirms prior work and owner files were preserved.
The full Go suite was not repeated for Markdown-only changes; prior implementation
verification remains in authorization-integrity-policy-verification.json.
No unresolved verification failures. No commit, publishing or release performed.
