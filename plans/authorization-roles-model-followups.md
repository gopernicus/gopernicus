# authorization v0.4.0 — `Config.RelationshipModel` + `ErrNoDecisionKind` → 500

**Status:** RELEASED 2026-08-26 — `features/authorization/v0.4.0` @ f1d4ad6 then **v0.5.0 @ 23f3fa6 (F3: `Config.Model` removed; cold-verified — `Config{Model:}` fails to compile at the tag)**; v0.4.0 was cold-verified from a throwaway module (RelationshipModel constructs; both set ⇒ ErrConfigConflict; ErrNoDecisionKind → 500, no ErrInvalidInput wrap). Left on the deprecated `Model:` on purpose: `stores/pgx/upgrade_runbook_test.go:268` (store pins authorization v0.1.0; rename at the store's next repin, before the v1.0 config cut). RATIFIED 2026-08-26 in-session (owner: "do 3 and 4 and release those before I go back to three sixty") — the two owner calls left open at the v0.3.0 close of `authorization-roles-model.md`. Release BEFORE gps-360-go adoption so the handoff targets the final names.
**Target tag:** `features/authorization/v0.4.0` (minor: additive field + one-release deprecation + a sentinel's HTTP status change). Stores NOT retagged.

## F1 — `Config.RelationshipModel` (rename with a one-release pass-through)

- `Config.RelationshipModel Schema` — the relationship kind's model, the exact counterpart of `RoleModel`. `Config.Model` STAYS for one release as a deprecated pass-through (`// Deprecated: use RelationshipModel; removed at the v1.0 config cut.`).
- Resolution at `NewService`: exactly one of the two may be set (non-empty `ResourceTypes`); both set ⇒ `ErrConfigConflict` (new, wraps `sdk.ErrInvalidInput`, beside `ErrModelRequired`). The resolved model drives everything else (`ErrModelRequired` ⇔ `Repositories.Relationships`, D1 rule 4, `SchemaDigest`, …) — no other behaviour changes.
- Type alias `type RelationshipModel = Schema` for symmetry with `RoleModel` (`Schema` stays; `ResourceTypeDef` stays).
- In-repo dogfood: every `Config{Model:` literal in `features/authorization` (tests, storetest, roles_gate_test), `examples/auth-cms`, and any other example moves to `RelationshipModel:`; docs (`README.md`, wiring page, `ARCHITECTURE.md`, `RELEASING.md`, package docs) say `RelationshipModel`. One root test pins: `Model` alone still works (deprecated path), both ⇒ `ErrConfigConflict`.

## F2 — `ErrNoDecisionKind` is a 500

- `ErrNoDecisionKind` no longer wraps `sdk.ErrInvalidInput`: it is a plain sentinel (`errors.New`), so `ReasonFor` reports no decision reason and `RespondError`/`web.ErrFromDomain` land it at **500** — a server-side wiring fault, consistent with the gates (which panic at mount for the same wiring), and deliberately unlike `ErrMutationsNotConfigured` (a precondition an actor can observe, 400). Document the asymmetry at both sentinels.
- `codes_test.go:TestNoDecisionKindIsAWiringFaultNotADeny` pins `http.StatusInternalServerError`, never 403, not `errors.Is(sdk.ErrInvalidInput)`, clean identity. README/RELEASING/plan §Execution notes updated (400 → 500, with the reason).

## Compatibility (v0.3.0 → v0.4.0)
Additive `Config.RelationshipModel` + `RelationshipModel` alias + `ErrConfigConflict`; `Config.Model` deprecated (still honoured); `ErrNoDecisionKind` no longer satisfies `errors.Is(err, sdk.ErrInvalidInput)` and answers 500 (only roles-only-without-model hosts see it). No store change.

## Verify / release
Per-module build/test/vet (`features/authorization` incl. `-race`, both stores, `examples/auth-cms`), `make guard`, gofmt; tag `features/authorization/v0.4.0`; cold-resolve; RELEASING entry.

## F3 — `Config.Model` removed (v0.5.0, owner ruling 2026-08-26 after v0.4.0)

"drop config.model now… let's just make it correct now… I'll update the other 2 apps."
The pass-through and `ErrConfigConflict` are gone; `RelationshipModel` is the only
name; construction order is zero kinds → `ErrModelRequired`. In-repo: `authorization.go`,
the deprecated-path test deleted, README rows removed, `stores/pgx/upgrade_runbook_test.go`
literal moved (in-workspace only; store pin unchanged). Downstream: coordination-hub and
gps-360-go rename `Model:` → `RelationshipModel:` (owner). Tag `features/authorization/v0.5.0`.
