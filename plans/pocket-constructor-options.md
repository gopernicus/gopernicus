# Pocket constructor options follow-up

Status: COMPLETE — 2026-09-11. Implementation, owned caller/docs migrations,
independent review and final verification passed. Consumer migration:
[AUDIT-031](../AUDIT.md#audit-031-consistent-pocket-constructor-options).

The owner noticed that the previous constructor pass retained pocket root Config
arguments and approved a consistent options construction surface, with required
dependencies explicit and meaningful policy records supplied through named options.
This supersedes the retained-pocket-Config decisions in constructor-options.md;
that plan remains the history of the earlier completed pass. CMS stays deferred.

## Contract and scope

- Review all public non-CMS pocket service, assembly, runtime and HTTP constructors.
  Move optional construction settings from positional Config/Deps bags into trailing
  typed functional options. Keep coherent policy/config records as option values;
  do not replace one large bag with a catch-all WithConfig option.
- Required collaborators and required mode selections remain explicit typed inputs.
  Related required ports may use a named dependencies/repositories record when
  separate arguments would obscure the assembly. Conditional features keep their
  existing paired-dependency validation. No new defaults or security-policy changes.
- Prefer feature-level groups for authentication (password policy, browser policy,
  delivery configuration, etc.), and existing coherent runtime/evaluation policies.
  Avoid a setter for every field within such a group. Each option replaces its
  group unless documented otherwise; no hidden nonzero-field merge.
- Options target private construction state, resolve before side effects, reject
  nil using the established error/panic convention, and snapshot mutable retained
  inputs. Preserve return signatures, error identities, validation, ownership,
  middleware order, subscriptions, worker lifecycle and host policy authority.
- Keep simple constructors with only required inputs, ordinary domain values,
  already-appropriate store option APIs and coherent resource connection inputs.
  Startup context/error-policy follow-ups from the previous plan are separate.
- Use New / NewX for exported construction functions and “constructor” as the noun
  in documentation and constructor filenames. Rename construct.go to constructor.go.
  Do not move every short New function out of its service file or rename existing
  clear NewService/NewRuntime APIs solely for uniform spelling.
- Migrate all owned callers, current docs/examples, and scaffold references when
  affected. Historical audit entries remain historical. Record AUDIT-031 breaking
  migrations and update the durable audit index and architecture convention.

## Tasks

1. Record constructor decisions and concrete input/group shapes for authentication,
   authorization, jobs and events. Check them against required ports and policies.
2. Implement each pocket and its local callers/tests; make independent named-agent
   review cover the resulting options and security/lifecycle preservation.
3. Migrate cross-pocket host wiring, examples, guides and migration documentation.
4. Format and verify affected modules (build/test/vet), focused behavior/race checks,
   actual-repository guards, docs and final 42-module make check. Record results.

## Preconditions and work ownership

- Branch: firestore-authentication. Extensive prior uncommitted audit work is
  preserved. Fresh baseline: /tmp/gopernicus-pocket-options-baseline and matching
  .json manifest; status: /tmp/gopernicus-pocket-options-status.txt.
- All Go/make commands use GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.
  Formatter: /Users/jrazmi/go/bin/goimports. There is no root go.mod; go.work
  contains 42 modules. Never hand-edit generated files or modify module dependencies.
- Named project implementer agents own authentication and authorization separately;
  root owns jobs/events, cross-module consumers and shared documentation. The named
  lead-backend-engineer reviewer is read-only. Legacy configured model names are
  unavailable, so existing agents retain the active runtime model. Current
  ARCHITECTURE.md and the owner's explicit decisions supersede stale role examples.
- Run listener-based tests with the established sandbox escalation if necessary;
  use isolated source snapshot for make check if the preexisting dirty generated
  baseline prevents a meaningful generated-drift check. No live external service,
  consumer repo, publishing, commit or deployment work is part of this task.

## Decisions, changed files and verification

### Implemented API choices

| Family | Required inputs and optional configuration |
|---|---|
| authentication.New | repositories, JWT signer, runtime mode, delivery mode; named password, sessions, identity, abuse-protection, delivery, messages, OAuth, passwordless, links, browser, invitations and administration groups; IDs/logger options |
| authentication logic.New | repositories, signer, runtime mode, limiter; feature-policy and collaborator options |
| invitations.New | repository and granter; access/delivery policies and optional normalizer, redirects, audit, TTL, clock, IDs/logger |
| authentication HTTP.New / NewAuthenticator | service and runtime mode; browser/authenticator policy and scoped options |
| delivery.NewRouter / NewJobsProcessor | mailer / encrypter and router explicit; templates, channel senders, processor policy and optional collaborators |
| delivery.NewInProcessQueue / NewInProcessRuntime | queue admission/retention options / explicit queue and processor plus retry/worker/shutdown/logger options |
| authorization.New | repositories; WithRelationshipModel, WithRoleModel, WithLimits, WithGuard, WithLogger, WithRoleRoutes(RoleRoutes) |
| decisions.NewService | Readers record; WithRoleModel and WithLimits |
| mutations.NewService | repository and Services record; compiled-model/guard/limits/logger options |
| authorization HTTP.New | Services record and optional WithRoleRoutes(RoleRoutes) |
| jobs.New | repositories; WithMaxAttempts, WithClock, WithCronParser, WithScheduleBatchSize; remove obsolete jobs.Config and queue.Config |
| queue.NewRuntime / NewFencedRuntime | service and handler map; RuntimePolicy / FencedRuntimePolicy groups, separate optional scheduler, logger, middleware and fenced clock/dead-letter hooks |
| events.New | bus; optional WithOutbox replaces the optional-only Repositories wrapper, plus visibility/projector/logger, stream limits, HTTP policy, resource gate and middleware |
| streams.New / events HTTP.New | bus / stream service; connection limits / lifetime policy and scoped collaborator options |

The public store constructors already follow the convention. Policy-value factories
(credential.NewDefaultPolicy), simple borrowed-emitter/logger helpers, explicit runtime-component
delivery assembly and ordinary entity/protocol builders remain explicit. The delivery assembly includes
an optional read-only status port alongside mutually exclusive runtime components;
it has no policy bag and retains existing component-pair validation. CMS stays
unchanged. No generic option or compatibility overload is introduced.

Authentication's existing browser strategy primitives Accept/Transports now
snapshot their captured argument slices: a shallow copy of a feature record
cannot make a mutable closure immutable. The independent review found this during
the grouped-option reuse check; its fix and behavior regression are complete.

### Owned host and test migrations

- The authentication example owns an environment/wiring record embedding the
  pocket's public feature configs; construction supplies its required inputs and
  named options. Policy values still load the existing AUTH_* keys.
- The jobs example loads RuntimePolicy plus its own tiny admission/scheduling
  settings. Its handlers and optional scheduler are explicit at construction.
- The authjobs host bridge constructs a runtime with matched Handle/Discard kind,
  receiving typed fenced options. The composition root retains its 20-second
  process timeout. Existing adversarial test matrices keep private fixture records
  for staged mutation; production APIs expose no legacy Config/Deps catch-all.
- The events example supplies its bus directly and opts into its outbox, visibility
  and HTTP gates. Environment loading uses streams.Limits and eventshttp.Policy.

### Final review and verification

Exact consolidated file inventory: /tmp/gopernicus-pocket-options-files.json,
computed against the fresh baseline. No module/dependency/SQL/generated artifact
edits were found in the interim inventory.

Named-agent reports:
- /tmp/gopernicus-pocket-options-authentication-implementation.md
- /tmp/gopernicus-pocket-options-authorization-implementation.md
- /tmp/gopernicus-pocket-options-jobs-events-final-review.md
- /tmp/gopernicus-pocket-options-authentication-authorization-review.md

Authorization's four modules passed build/test/vet, core race and tagged store
compile/vet; its host document-listing checks passed with local HTTP fixtures.
Jobs/events core tests pass, including stream HTTP behavior. New focused tests
cover nil-option rejection before subscription, repeated HTTP without additional
subscriptions, capture/reuse/clear of middleware and dead-letter hooks, and final
whole-policy replacement before fenced timeout validation. Authentication's five
modules passed build/test/vet and its final core race suite, including the nested
credential-policy regression. Independent final reviews found no remaining blockers.

All Go/make commands used GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache.

- Exact final source passed the 42-module `make check`: build/test/vet, tagged
  integration/live compilation, templ/assets generation consistency, scaffold
  generation/build/test coverage and guards. The established isolated snapshot
  avoids treating preexisting uncommitted generated files as new drift. Snapshot:
  /tmp/gopernicus-pocket-options-final-check; manifest and log use the same prefix
  with -manifest.json and .log. Driver: the same prefix with .py.
- `make guard` also passed in the actual Git workspace; this covers Git-dependent
  guard inputs omitted from the isolated source snapshot. Log:
  /tmp/gopernicus-pocket-options-final-guard.log.
- Jobs, events, examples/jobs-minimal and examples/auth-cms each passed
  `go build ./...`, `go test -count=1 ./...` and `go vet ./...`.
  Log: /tmp/gopernicus-pocket-options-root-checks.log. These exercise actual local
  HTTP flows, credential/authorization gates, worker startup/shutdown, retries,
  fenced delivery and stream visibility through the migrated host wiring.
- All four pocket cores passed race suites; jobs/events log:
  /tmp/gopernicus-pocket-options-root-race.log; authentication log:
  /tmp/gopernicus-pocket-options-authentication-race.log; authorization logs and
  its document-listing build/race/vet evidence are recorded in its named report.
- Documentation `pnpm typecheck` and `pnpm build` passed. Logs:
  /tmp/gopernicus-pocket-options-docs-typecheck.log and -docs-build.log.
- Changed Go files passed goimports and `git diff --check`. Formatter output:
  /tmp/gopernicus-pocket-options-format.log. Final file/hash verification is
  recorded in /tmp/gopernicus-pocket-options-final-source-verification.json.

The first full snapshot also passed. A final read found three malformed public
configuration-name strings in comments/documentation and one sentinel diagnostic;
these were corrected without changing error identity or authority, then the exact
corrected source passed the final full snapshot above. Initial local-listener
sandbox denials were resolved by approved loopback fixture runs. No failures remain.

Current-phase inventory is /tmp/gopernicus-pocket-options-files.json: 32 added,
222 modified and 2 removed paths (the two constructor filenames were renamed).
No CMS, module/dependency, SQL/schema or generated-artifact changes occurred.
Live SQL/cloud providers and browser-engine suites were not rerun for this
construction-API phase. No external consumer repo, release, commit or deployment
was performed. The separate startup-context/constructor-error questions remain
recorded in constructor-options.md; they are not unfinished work in this phase.

## Copyable next-window prompt

```text
Continue work on Gopernicus in /Users/jrazmi/code/gopernicus-ecosystem/gopernicus.
Read AGENTS.md/global preferences, ARCHITECTURE.md, plans/framework-audit.md,
plans/pocket-constructor-options.md and the latest AUDIT.md entries first.

Our priorities are correctness, understandable code, explicit host policy and
customization, and fewer unnecessary abstractions. SDK stays stdlib-only; pockets
are opt-in; adapters stay replaceable. CMS remains deferred.

The SDK/non-CMS pocket/integration audit and pocket reorganization are complete.
The latest constructor follow-up is COMPLETE: all four non-CMS pocket roots and
configurable service/HTTP/runtime constructors use explicit required inputs plus
typed WithFoo options. Meaningful feature/policy records remain grouped option
values. Groups replace completely, including zero fields; captured inputs are
snapshotted. Dedicated files use constructor.go; clear New/NewService/NewRuntime
names remain. AUDIT-031 supersedes AUDIT-030's retained-pocket-Config choices.
Do not repeat the completed audit or reintroduce removed Config/Deps overloads.

All 42 modules passed the final build/test/vet/generation/scaffold/guard check;
the actual workspace guards, four core race suites, migrated host HTTP/jobs tests
and docs checks passed. Live SQL/cloud/browser-engine checks were not rerun in
this constructor phase. Evidence and exact files are in the latest plan.

Branch: firestore-authentication, with extensive preexisting uncommitted work.
Preserve it; do not reset, commit, publish or modify external consumers implicitly.
There is no root go.mod (42-module go.work). Every Go/make command must use
GOCACHE=/tmp/gopernicus-audit-s1.aMLwCQ/cache; formatter is
/Users/jrazmi/go/bin/goimports. Follow the documented snapshot method if existing
generated-file changes prevent a meaningful direct make check. Never hand-edit
generated files.

First summarize the remaining choices from constructor-options.md's separate
startup-context/error-policy follow-ups versus consumer adoption using AUDIT.md.
Establish a bounded next phase before implementing it, and keep breaking consumer
instructions in AUDIT.md and progress in the established plans directory.
```
