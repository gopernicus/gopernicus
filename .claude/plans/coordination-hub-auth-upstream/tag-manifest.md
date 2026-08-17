# Tag manifest — coordination-hub auth upstream (+ crud search)

Status: **PREPARED, NOT CUT.** Every command in the "owner commands" sections is
**DO NOT RUN BY THE PLANNING TASK**. Nothing here has been pushed, tagged, or
adopted downstream.

Date prepared: 2026-08-16
Plans of record: `.claude/plans/coordination-hub-auth-upstream/` (phases 1–7) and
`.claude/plans/crud.md` (T1–T4), stacked onto one release by owner direction.

> **Note on the previous manifest.** This file replaces one deleted in the working
> tree by the owner along with the old single-file `plan.md`. The prior batch's
> release evidence (sdk v0.3.0, authentication v0.2.0, pgxdb v0.3.0) is unchanged
> in history at commit `9b73785` and can be recovered with
> `git show 9b73785:.claude/plans/coordination-hub-auth-upstream/tag-manifest.md`.
> `RELEASING.md` references this path, so it is restored rather than renamed.

---

## 1. Commit state

The working tree is **DIRTY and uncommitted**. `git status --short` at preparation
time: **108 entries** — 50 modified, 56 untracked, 2 deleted.

The two deletions (`plan.md`, `tag-manifest.md`) are the **owner's**, not this
task's: they were removed when the ten-file planning packet replaced the single
plan file. They are preserved as-is except for restoring `tag-manifest.md` (this
file), which `RELEASING.md` links to.

**No commit, tag, push, or downstream edit has been made.** The version proposals
below are computed from the diff between each module's latest tag and the current
working tree.

---

## 2. Module → tag → rationale

| module | current | proposed | bump | why |
|---|---|---|---|---|
| `sdk` | `v0.3.1` | **`v0.4.0`** | minor | New canonical runtime posture (`environment.Mode` + validation), capability-owned transport checks (`email.CheckSender` / `notify.CheckNotifier` + `ErrInsecureTransport` + `TransportPosture`), and the restored list-search vocabulary (`crud.SearchField`, `ListParams.Search`, `ListRequest.Search`, `MatchesSearch`). All additive → minor. Also carries the transactional-layout logo fix, which is a patch on its own but must not be split from this commit. |
| `integrations/datastores/pgxdb` | `v0.3.0` | **`v0.4.0`** | minor | `ListQuery.SearchFields`, `AddSearchClause`, `EscapeSearchTerm`, reserved `@list_search`. Requires `sdk v0.4.0`. |
| `integrations/datastores/turso` | `v0.2.0` | **`v0.3.0`** | minor | The dialect-parity twin of the above. Requires `sdk v0.4.0`. |
| `features/authentication` | `v0.2.2` | **`v0.3.0`** | minor | The largest surface: account lifecycle + operator directory, verification resend, password-reset links (**new required production config**), the `RuntimeMode` alias, the `q` search param, and — if the second train ships together — provision-on-consumption. Requires `sdk v0.4.0`. |
| `features/authentication/stores/pgx` | `v0.1.0` | **`v0.2.0`** | minor | Two new canonical migrations (**host schema**), the `UserAdmin`/`ActiveSessions`/`Passwordless` ports, API-key `SearchFields`, the `users.id` collation pin, and a cross-dialect cursor fix. Requires the new authentication, sdk, and pgxdb tags. |
| `features/authentication/stores/turso` | `v0.1.0` | **`v0.2.0`** | minor | The same, dialect-side. Requires the new authentication, sdk, and turso tags. |
| `integrations/email/sendgrid` | `v0.2.0` | **no tag** | — | The only change is a new `posture_test.go`. Test-only; nothing importable changed. |
| `examples/*` | — | **never tagged** | — | Demonstration hosts, not libraries. |

### Source vs operator compatibility — classified separately

**Source-compatible** for every existing caller. Nothing exported was removed or
renamed. `auth.RuntimeMode` became a type ALIAS of `environment.Mode`, which is
assignable in both directions. Two struct-field additions (`user.User.Status` /
`StatusChangedAt`, `challenge.Challenge.SubjectKey`) would break only an
exhaustive positional literal; no such construction exists in-repo.

Two error MESSAGES changed (`ErrRuntimeModeRequired` / `ErrRuntimeModeInvalid`
gained a canonical-error suffix). `errors.Is` is unchanged and remains the
supported matcher.

**Operator-INCOMPATIBLE** without action, in two specific ways:

1. **Store migrations `0014` and `0015` must be applied before deploying binaries
   built against the new store tags.** Both constructors now probe the added
   columns by name and refuse to construct otherwise — a loud boot failure, not a
   silent one.
2. **`Config.PasswordResetURL` is REQUIRED in production** once the forgot/reset
   rail is wired. A production host without it fails at construction. The example
   host's own production baseline test caught this, which is exactly the signal
   adopters will get.

---

## 3. Release trains

The plan's release shape is followed: the high-risk provisioning work is a
separate train from the console unblock.

**Train 1 — core (phases 1–5, 7 + crud search).** Everything except
provision-on-consumption. This unblocks the coordination-hub admin console,
verification resend, reset links, the runtime-posture layering fix, sdk branding,
and searchable lists.

**Train 2 — provisioning (phase 6).** `PasswordlessProvisionOnRedeem`, the
`domain/passwordless` port, both atomic store implementations, and migration
`0015`.

**They are currently ONE commit.** Splitting them means separating the phase-6
changes (`domain/passwordless/`, `*/passwordless.go`, `0015_*.sql`, the challenge
`SubjectKey` field, the `PasswordlessProvisionOnRedeem` config, and the
provisioning tests) into a second commit before tagging. **Owner call:** ship both
on one set of tags, or split. If shipped together, the version numbers above are
unchanged — provisioning is additive and default-off, so it does not raise any
bump. If split, train 1 takes the versions above and train 2 takes
`features/authentication/v0.3.1` (or `v0.4.0` if the owner prefers a minor for a
new security capability) plus store `v0.2.1`.

---

## 4. Owner commands — **DO NOT RUN BY THE PLANNING TASK**

Dependency order matters: a store tag that pins an authentication tag which does
not exist yet will not resolve.

```sh
# 0. Commit the work first. Nothing below is meaningful on a dirty tree.
git add -A && git commit   # message is the owner's

# 1. sdk — nothing depends on an unreleased tag.
git tag sdk/v0.4.0 && git push origin sdk/v0.4.0

# 2. Both connectors. Update each go.mod to require sdk v0.4.0 FIRST, then commit.
#    (go.work masks a stale sibling require locally — the tag will not.)
git tag integrations/datastores/pgxdb/v0.4.0 && git push origin integrations/datastores/pgxdb/v0.4.0
git tag integrations/datastores/turso/v0.3.0 && git push origin integrations/datastores/turso/v0.3.0

# 3. authentication core. Its go.mod must require sdk v0.4.0.
git tag features/authentication/v0.3.0 && git push origin features/authentication/v0.3.0

# 4. Both authentication store modules. Their go.mod files must require the tags
#    from steps 1–3.
git tag features/authentication/stores/pgx/v0.2.0   && git push origin features/authentication/stores/pgx/v0.2.0
git tag features/authentication/stores/turso/v0.2.0 && git push origin features/authentication/stores/turso/v0.2.0
```

**go.mod edits required before tagging** (currently every module still pins the
v0.1.0-era siblings that `go.work` masks locally):

| module | change |
|---|---|
| `features/authentication` | `sdk v0.3.0` → `v0.4.0` |
| `integrations/datastores/pgxdb` | `sdk v0.1.0` → `v0.4.0` |
| `integrations/datastores/turso` | `sdk v0.1.0` → `v0.4.0` |
| `features/authentication/stores/pgx` | `sdk v0.1.0` → `v0.4.0`; `features/authentication v0.1.0` → `v0.3.0`; `pgxdb v0.1.0` → `v0.4.0` |
| `features/authentication/stores/turso` | `sdk v0.1.0` → `v0.4.0`; `features/authentication v0.1.0` → `v0.3.0`; `turso v0.1.0` → `v0.3.0` |

---

## 5. Pre-tag gate evidence (run 2026-08-16)

### Hermetic — ALL PASS

```
(cd sdk                                    && go build ./... && go test -race -count=1 ./... && go vet ./...)
(cd features/authentication                && go build ./... && go test -race -count=1 ./... && go vet ./...)
(cd features/authentication/stores/pgx     && go test -race -count=1 ./...)
(cd features/authentication/stores/turso   && go test -race -count=1 ./... && go vet -tags=integration ./...)
(cd integrations/datastores/pgxdb          && go test -race -count=1 ./...)
(cd integrations/datastores/turso          && go test -race -count=1 ./...)
(cd integrations/email/sendgrid            && go test -race -count=1 ./...)
(cd examples/auth-cms                      && go test -race -count=1 ./...)
make check   # nineteen guards + every module's vet
```

Every other workspace module (`features/{authorization,cms,events,jobs}`,
`ui/goth`, `examples/{cms,minimal,jobs-minimal}`, `workshop/gopernicus`) builds
clean.

### Live gates — BOTH DIALECTS CLOSED

*PostgreSQL 17*, in throwaway containers created and removed for these runs. The
user's existing `coordination-hub-postgres` and `venona-*` containers were
deliberately **not** used: the conformance harness truncates tables.

| gate | result |
|---|---|
| full `storetest` conformance | PASS (55.1s) |
| lifecycle groups (`UserDirectory` / `UserLifecycle` / `ActiveSessionMint`) | PASS, 14 cases |
| deactivate-vs-mint race, `-count=5` (×12 rounds each) | PASS |
| `PasswordlessRedeem`, 12 adversarial cases | PASS |
| concurrent-redeem race, `-count=5` (×8 rounds each) | PASS |
| `ListSearch` oracle, 13 terms + count + cursor paging | PASS |
| `TestContractualCollation_Catalog` | PASS — `users.id` reports collation `C` |
| schema probes | PASS |

*Turso / libSQL*, against the authorized playground database
(`libsql://gopernicus-cms-playground-gps-impact.aws-us-east-2.turso.io`, verified
to match the recorded safe-to-wipe URL **before** any destructive run).

| gate | result |
|---|---|
| migrations `0014` + `0015` apply | PASS (checksums `c80035ba`, `b7789bfc`) |
| lifecycle groups | PASS, 14 cases (76.5s) |
| `PasswordlessRedeem`, 12 cases | PASS (64.4s) |
| `ListSearch` oracle | PASS (21.7s) |

**One environment failure is recorded verbatim rather than retried into a green:**
the first Turso conformance attempt failed with
`checksum mismatch: migration default:0001_users.sql was modified after being applied`
— a stale ledger row left on that shared playground database by an older auth
cut. The integration schema-probe's own drop-and-remigrate reset cleared it. No
canonical migration file was edited.

### Gates NOT run

- **`POSTGRES_NON_C_TEST_DSN`** (the non-C-collation ordering proof) was not set,
  so `TestCollationControlsOrdering_NonC` **skipped**. The catalog check that
  `users.id` reports collation `C` DID run and passed, so the new pin is verified;
  what is unverified is the belt-and-braces ordering demonstration on a locale
  database. **Open owner gate.**
- **Live concurrency repetition.** The plan suggests `-count=10`. Runs were
  `-count=5` (pgx) and `-count=1` (Turso), where each round costs ~8s of network
  round-trips. Because each case loops internally (12 rounds for the mint race, 8
  for the redeem race), the executed totals are 60 and 40 concurrent races on pgx
  and 12/8 on Turso. Raising it is a scheduling decision.
- **A real-browser flow.** Every user-facing flow is proven over real HTTP through
  exported host seams with a real cookie jar (admin lifecycle, resend, reset link
  redemption, OAuth link/unlink, provisioning), but no headless browser was
  driven. **Open owner gate** if a browser-level check is required before release.

### Documentation gate

- Exported comments compile and were reviewed against behavior.
- `features/authentication/README.md` gained five new sections (account lifecycle,
  verification resend, password reset, OAuth linking, provision-on-consumption),
  and its route table, config matrix, repositories table, and migration section
  are internally consistent with them.
- Store migration counts and file lists match: **15 files per dialect, filename
  sets byte-identical** (verified by `diff`).
- Every new production-required field (`PasswordResetURL`) appears in the example
  host and in the upgrade notes.
- Compatibility aliases (`RuntimeMode`, the runtime-mode errors, the reset
  template's `.Secret`) each carry a stated deprecation/removal posture.
- Downstream snippets use exported APIs and tagged module paths only.

---

## 6. Post-tag cold resolution — **owner runs after pushing**

`go.work` and local replaces are NOT release evidence. Verify each tag outside the
workspace:

```sh
cd "$(mktemp -d)"
go mod init scratch
GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/sdk@v0.4.0
GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/features/authentication@v0.3.0
GOWORK=off GOFLAGS=-mod=mod go get github.com/gopernicus/gopernicus/features/authentication/stores/pgx@v0.2.0
GOWORK=off go build ./...

# Authentication must select the intended sdk, and each store the intended authentication.
GOWORK=off go list -m github.com/gopernicus/gopernicus/sdk                       # expect v0.4.0
GOWORK=off go list -m github.com/gopernicus/gopernicus/features/authentication   # expect v0.3.0
```

---

## 7. Known-dirty / carried notes

- `.claude/plans/crud.md` is untracked; it is the plan of record for the search
  work and should be committed with it.
- `RELEASING.md` still carries the 2026-08-14 "PREPARED, NOT YET CUT" entry for
  the previous batch. That batch's tags (`sdk/v0.3.0`, `features/authentication/v0.2.0`,
  `integrations/datastores/pgxdb/v0.3.0`) **do exist**, so that entry is stale
  wording rather than pending work — worth a cleanup pass, not a blocker.
- The pgx stores other than the user directory (`service_accounts`, `api_keys`,
  `security_events`, `invitations`) still emit cursors carrying the session's time
  zone offset rather than UTC. Only the new directory list was normalized, because
  only it contractually promises byte-identical cursors across dialects. Flagged
  for the owner; out of scope here.
- `GET /auth/oauth/{provider}/link/start` uses the stateless `RequireUser` tier
  and therefore honors an already-issued access JWT for up to `AccessTokenTTL`
  after deactivation. Documented, deliberately not changed — it is a live-behavior
  change for existing hosts and belongs to its own dispatch.
