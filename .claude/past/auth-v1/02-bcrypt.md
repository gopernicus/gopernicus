# Phase 2 — `integrations/cryptids/bcrypt`

Status: DRAFT — pending ratification
Executor model: opus
Depends on: phase 1 (the `auth.PasswordHasher` port it satisfies).

## Goal

A new module `gopernicus/integrations/cryptids/bcrypt` wrapping exactly one
third-party library (`golang.org/x/crypto/bcrypt`), structurally satisfying
`features/auth`'s `PasswordHasher` port. Constitution rule 2 shape — mirror
`integrations/datastores/turso`'s module conventions.

## Design constraint to notice before coding

Constitution rule 3: the port lives with its consumer (`features/auth`). This
integration must NOT import `features/auth` — it exports a concrete
`Hasher` type whose method set matches the port; satisfaction is structural,
verified in THIS module's tests via a locally-declared interface literal
matching the port's method set (copy the signatures; add a comment naming the
port it mirrors). The original's adapter is at
`gopernicus-original/infrastructure/cryptids/bcrypt/` — read it for cost
handling and error semantics; port the behavior, not the file.

## Work items

1. `integrations/cryptids/bcrypt/go.mod` — module path as above; requires
   `golang.org/x/crypto` only (+ sdk if error mapping needs `sdk/errs`; prefer
   plain errors if not). `go.work` + Makefile MODULES updated.
2. `bcrypt.go`: `New(opts...)` (configurable cost, sane default —
   `bcrypt.DefaultCost`), `HashPassword(ctx, plain) (string, error)`,
   `VerifyPassword(ctx, plain, hash) error` (mismatch → a stable sentinel or
   the port's documented error semantics — match what phase 1's port doc
   comment promises; read it first). Reject over-long inputs (bcrypt's 72-byte
   truncation — return an error rather than silently truncating).
3. Tests: hash→verify roundtrip; wrong password fails; two hashes of the same
   password differ (salt); cost option respected; 72-byte boundary case;
   structural-satisfaction compile assertion against the locally-mirrored
   interface.
4. `go mod tidy`; confirm the module builds standalone (`cd` in, build/vet/test)
   AND via the workspace.

## Acceptance

```sh
cd integrations/cryptids/bcrypt && go build ./... && go vet ./... && go test ./...
grep -rn "features/" integrations/cryptids/bcrypt/   # empty — no feature import
make check                                            # green, module included
```

## Real-interaction check

Standing check (a) from `00-overview.md`.

## Execution log

### 2026-07-02 — phase 2 executed (loop leg 7; implementer on opus)

Shipped `integrations/cryptids/bcrypt` (10th module): `Hasher`, `New` +
`WithCost` (default bcrypt.DefaultCost, out-of-range clamps), HashPassword,
VerifyPassword (constant-time via CompareHashAndPassword),
`ErrPasswordTooLong` on >72 BYTES (never silently truncates), README.
go.work + MODULES updated. NO feature import — structural satisfaction
asserted via a locally-mirrored interface literal (comment phrased without
the literal "features/" string so the acceptance grep stays clean).

Divergences: none from the phase file. Error semantics: plain errors, no
sdk dependency — the port doc promises only self-describing hash /
non-nil-on-mismatch / constant-time, none of which needs errs kinds; the
module requires exactly golang.org/x/crypto. Note: the dispatch prompt
paraphrased the port with ctx params; the executor correctly matched the
REAL port signatures (`HashPassword(password) (string, error)`,
`VerifyPassword(hash, password) error`) — the port is the authority.

Acceptance (re-run FIRSTHAND): build/vet/test PASS; `grep -rn "features/"`
→ 0 hits; `make check` → "all checks passed" (10 modules, 4 guards).
Real-interaction (a): `GET http://localhost:8081/` → 200,
`GET /products/widget-3000` → 200; killed; port free. (The hasher has no
runtime surface of its own — it gets driven live by phase 4's flow (b).)

Unverified: nothing for this phase.
