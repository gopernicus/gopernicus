# gopernicus — framework monorepo (sdk + integrations + pockets + examples)
#
# Multi-module workspace (go.work), 47 modules. templ is pinned via the `tool`
# directive in pockets/cms/views/goth/go.mod (where the .templ sources live),
# so `go tool templ` is reproducible.

MODULES = sdk pockets integrations/authentication/auth0 integrations/authentication/firebase integrations/cryptids/bcrypt integrations/cryptids/go-oidc integrations/cryptids/golang-jwt integrations/cryptids/google-uuid integrations/datastores/pgxdb integrations/datastores/turso integrations/email/sendgrid integrations/filestorage/gcs integrations/filestorage/s3 integrations/kvstores/goredis integrations/oauth/github integrations/oauth/google integrations/scheduling/robfig-cron integrations/tracing/otel pockets/authentication pockets/authentication/stores/pgx pockets/authentication/stores/turso pockets/authentication/views/goth pockets/authorization pockets/authorization/stores/pgx pockets/authorization/stores/turso pockets/authorization/stores/goredis pockets/cms pockets/cms/stores/pgx pockets/cms/stores/turso pockets/cms/views/goth pockets/events pockets/events/stores/pgx pockets/events/stores/turso pockets/jobs pockets/jobs/stores/pgx pockets/jobs/stores/turso pockets/secrets pockets/secrets/stores/pgx ui/goth examples/auth-cms examples/cms examples/external-identity examples/goth-showcase examples/jobs-minimal examples/minimal examples/production-baseline workshop/gopernicus

# STORE_MODULES carry env-gated live conformance suites (storetest against a real
# database). `make check`/`make test` run them hermetically (loud skips); `make
# test-stores` runs them EXPECTING the datastore env vars set.
STORE_MODULES = pockets/cms/stores/pgx pockets/cms/stores/turso pockets/authentication/stores/pgx pockets/authentication/stores/turso pockets/jobs/stores/pgx pockets/jobs/stores/turso pockets/events/stores/pgx pockets/events/stores/turso pockets/authorization/stores/pgx pockets/authorization/stores/turso pockets/secrets/stores/pgx

# INTEGRATION_TAG_MODULES carry `-tags=integration` sources `make check` must keep
# COMPILING: the Turso external-store suites and authorization's SQL-to-Redis
# suite (local SQLite plus env-gated PostgreSQL). The Redis module is named
# explicitly because it is not in STORE_MODULES. Execution of these tagged suites
# is a separate verification leg.
INTEGRATION_TAG_MODULES = $(filter %/turso,$(STORE_MODULES)) pockets/authorization/stores/goredis
# PROVIDER_LIVE_TAG_MODULES carry a bare `-tags=live` leg against a real identity
# provider tenant (owner-minted tokens; env-gated, never run by CI).
# EMULATOR_TAG_MODULES carry a `-tags=emulator` leg against the Firebase Auth
# emulator (`make dev-firebase`). `make check` vets both compile-only so the
# tagged legs cannot rot between manual runs.
PROVIDER_LIVE_TAG_MODULES = integrations/authentication/auth0 integrations/authentication/firebase integrations/cryptids/go-oidc
EMULATOR_TAG_MODULES = integrations/authentication/firebase

.PHONY: generate generate-ui-assets build vet test test-stores vuln test-ui-browser docs-install docs docs-build run migrate check tidy guard warm-scaffold-cache dev-firebase dev-firebase-down \
	guard-sdk-stdlib guard-pocket-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-pocket-dependencies guard-pocket-transport-sdk-web guard-pocket-no-cross-pocket \
	guard-store-no-foreign-pocket guard-no-underlying guard-no-lax-scan \
	guard-workshop-boundary guard-sdk-layering guard-integration-no-inward \
	guard-auth-no-delivery-repo guard-auth-no-request-time-provider \
	guard-authorization-no-delivery-repo guard-authorization-rolesvc-no-engine guard-ui-no-inward guard-ui-require-whitelist \
	guard-no-legacy-features-path guard-list-no-nethttp guard-violation-message-not-error \
	guard-pocket-logic guard-authorization-tuples-leaf guard-authorization-decisions-no-mutations guard-inbound-authorization guard-tuple-write-integrity guard-host-contract guard-auth-no-vendor-names \
	guard-cms-admin-gate guard-host-posture guard-pocket-no-env guard-baseline-ledger guard-rail-no-dml guard-auth-maintenance-leaf \
	guard-credential-fields-secret

# Regenerate *_templ.go from .templ sources. Each bundled views/templ module pins
# its own templ tool; generation runs inside each so the tool version is
# reproducible per module.
generate:
	cd pockets/cms/views/goth && go tool templ generate
	cd pockets/authentication/views/goth && go tool templ generate
	cd ui/goth && go tool templ generate

# Regenerate the ui/goth CSS/JS/HTMX assets + fingerprinted manifest from source.
# NODE-GATED: this is the only target that needs the repository-local Node
# toolchain (pinned via ui/goth/tools/.nvmrc + package-lock.json). It is
# deliberately NOT a dependency of `generate`, `build`, or `check` — Go consumers
# and Node-free contributors never run it. `make check` instead proves the
# committed dist/ + manifest.json are in sync via a plain-git diff (see `check`),
# so stale assets still fail CI without invoking Node. `npm ci --ignore-scripts`
# installs exactly the committed lockfile with no install scripts (esbuild's
# platform binary ships as an optional dependency package, not a postinstall).
generate-ui-assets:
	cd ui/goth/tools && npm ci --ignore-scripts && npm run build

build: generate
	@for m in $(MODULES); do echo "build $$m"; (cd $$m && go build ./...) || exit 1; done

vet:
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done

# vuln runs govulncheck (symbol-level reachability: only advisories your code
# actually reaches fail the run) in every MODULE. NOT part of `check`: it needs
# the live vulnerability database from vuln.go.dev, so it is not hermetic. CI runs
# it as its own job ("vuln", check.yml) next to "check", and a release records
# the clean run in its manifest. The tool version is pinned so a result is
# reproducible for a given database snapshot; bump it deliberately.
GOVULNCHECK = golang.org/x/vuln/cmd/govulncheck@v1.9.0

vuln:
	@for m in $(MODULES); do echo "== vuln $$m =="; (cd $$m && go run $(GOVULNCHECK) ./...) || exit 1; done

test:
	@for m in $(MODULES); do echo "test $$m"; (cd $$m && go test ./...) || exit 1; done

# test-stores runs the dialect store modules' live conformance suites, EXPECTING
# the datastore env vars set (vs `make check`/`make test`, which skip loudly and
# stay hermetic). It fails loudly if POSTGRES_TEST_DSN is unset — this milestone's
# proof is the live postgres run. The turso leg is `-tags=integration` and skips
# loudly without TURSO_DATABASE_URL/TURSO_AUTH_TOKEN.
#
# Spin a local postgres, then run:
#   docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
#   POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
#     make test-stores
# The Firebase Auth emulator (integrations/authentication/firebase -tags=emulator)
# is `make dev-firebase`; the equivalent one-off container is:
#   docker build -t gopernicus/firebase-auth-emulator:15.34.0 workshop/dev/firebase
#   docker run --rm -d -p 127.0.0.1:9099:9099 --name gopernicus-firebase-auth-emulator \
#     gopernicus/firebase-auth-emulator:15.34.0
#   FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 (project demo-gopernicus)
# pgx-leg runs one pgx store module's live suite twice: once unqualified (the
# default every existing host runs) and once with POSTGRES_TEST_SCHEMA set, so
# every statement is proven both bare and schema-qualified
# (plans/pgx-store-schema-option.md, S7). The pgxdb connector's own live tests
# carry their schema coverage internally and run once.
define pgx-leg
	@echo "== $(1) (live) =="
	@cd $(1) && go test ./...
	@echo "== $(1) (live, POSTGRES_TEST_SCHEMA=gopernicus_schema_test) =="
	@cd $(1) && POSTGRES_TEST_SCHEMA=gopernicus_schema_test go test ./...
endef

test-stores:
	@if [ -z "$$POSTGRES_TEST_DSN" ]; then \
		echo "ERROR: POSTGRES_TEST_DSN not set — postgres store conformance cannot run (make check stays hermetic; this target expects it)"; \
		echo "  docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17"; \
		echo "  POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' make test-stores"; \
		exit 1; \
	fi
	@echo "== integrations/datastores/pgxdb (live) =="
	@cd integrations/datastores/pgxdb && go test ./...
	$(call pgx-leg,pockets/cms/stores/pgx)
	$(call pgx-leg,pockets/authentication/stores/pgx)
	$(call pgx-leg,pockets/jobs/stores/pgx)
	@echo "== pockets/cms/stores/turso (live, -tags=integration) =="
	@cd pockets/cms/stores/turso && go test -tags=integration ./...
	@echo "== pockets/authentication/stores/turso (live, -tags=integration) =="
	@cd pockets/authentication/stores/turso && go test -tags=integration ./...
	@echo "== pockets/jobs/stores/turso (live, -tags=integration) =="
	@cd pockets/jobs/stores/turso && go test -tags=integration ./...
	$(call pgx-leg,pockets/events/stores/pgx)
	@echo "== pockets/events/stores/turso (live, -tags=integration) =="
	@cd pockets/events/stores/turso && go test -tags=integration ./...
	$(call pgx-leg,pockets/authorization/stores/pgx)
	@echo "== pockets/authorization/stores/turso (live, -tags=integration) =="
	@cd pockets/authorization/stores/turso && go test -tags=integration ./...
	$(call pgx-leg,pockets/secrets/stores/pgx)

# dev-firebase starts the Firebase Auth emulator (auth only, demo-gopernicus
# project, no credentials) on 127.0.0.1:9099 from workshop/dev/firebase, whose
# Dockerfile pins firebase-tools and the node base image. Docker-gated and never
# part of `check`; the -tags=emulator leg skips loudly without it.
dev-firebase:
	docker compose -f workshop/dev/firebase/docker-compose.yml up -d --build --wait
	@echo "Firebase Auth emulator: FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 FIREBASE_PROJECT_ID=demo-gopernicus"

dev-firebase-down:
	docker compose -f workshop/dev/firebase/docker-compose.yml down

# test-ui-browser runs the ui/goth three-engine Playwright + axe harness
# (Chromium, Firefox, WebKit) against the zero-datastore examples/goth-showcase
# host. NODE-GATED and browser-gated: it is deliberately NOT a dependency of
# `check`/`test`/`build`, so the hermetic Go loop and Node-free contributors never
# run it, while the UI milestone/release gate requires it (plan Layer 3). axe-core
# (MPL-2.0) lives ONLY in the e2e toolchain and never reaches ui/goth/assets/dist.
# Playwright starts the Go server itself (webServer in playwright.config.ts). On a
# cold machine the browser binaries download once via `npx playwright install`.
test-ui-browser:
	cd examples/goth-showcase/e2e && npm ci && npx playwright install chromium firefox webkit && npm test

# Documentation is Node-gated and deliberately separate from the hermetic Go
# gate. The lockfile pins the full Docusaurus toolchain.
docs-install:
	cd workshop/documentation && pnpm install --frozen-lockfile

docs:
	cd workshop/documentation && pnpm start

docs-build:
	cd workshop/documentation && pnpm typecheck && pnpm build

# The server binary never migrates; `run` applies host-owned migrations first
# (pre-boot), then serves — keeping migration a separate, explicit step. Both
# read the runtime mode from the process environment only (never .env), so the
# targets export the development posture.
run: generate migrate
	cd examples/cms && APP_RUNTIME_MODE=development go run ./cmd/server

# Migrations are HOST-OWNED and applied pre-boot (never by the framework at
# startup). The CMS pocket's SQL is scaffolded into the host's own dir
# (examples/cms/workshop/migrations/primary) and applied by the host's runner.
migrate:
	cd examples/cms && APP_RUNTIME_MODE=development go run ./workshop/migrations

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

# Layering guards — each enforces one architectural boundary from the
# constitution (00-overview.md) or the feature-standard charter (FS rules,
# 2026-07-07); every target must print nothing and exit 0 on a clean tree.
# `make guard` runs the dependency and behavior boundary checks below.
guard: guard-sdk-stdlib guard-pocket-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-pocket-dependencies guard-pocket-transport-sdk-web guard-pocket-no-cross-pocket \
	guard-store-no-foreign-pocket guard-no-underlying guard-no-lax-scan \
	guard-workshop-boundary guard-sdk-layering guard-integration-no-inward \
	guard-auth-no-delivery-repo guard-auth-no-request-time-provider \
	guard-authorization-no-delivery-repo guard-authorization-rolesvc-no-engine guard-ui-no-inward guard-ui-require-whitelist \
	guard-no-legacy-features-path guard-list-no-nethttp guard-violation-message-not-error \
	guard-pocket-logic guard-authorization-tuples-leaf guard-authorization-decisions-no-mutations guard-inbound-authorization guard-tuple-write-integrity guard-host-contract guard-auth-no-vendor-names \
	guard-cms-admin-gate guard-host-posture guard-pocket-no-env guard-baseline-ledger guard-rail-no-dml guard-auth-maintenance-leaf \
	guard-credential-fields-secret

# G1: sdk imports only the standard library (also enforced structurally by
# sdk/go.mod having no require block).
guard-sdk-stdlib:
	@echo "== guard: sdk imports only the standard library =="
	@! grep -rn --include='*.go' '"github.com/' sdk/ | grep -v '"github.com/gopernicus/gopernicus/sdk' || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }
	@! grep -rnE '"(cloud\.google\.com|golang\.org/x|gopkg\.in)/' --include='*.go' sdk/ || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }

# G2: core production code cannot wire adapters. The shared Go checker follows
# actual go.mod boundaries, so stores/memory and stores/storetest remain covered;
# core tests may use those own-pocket packages without pulling in driver modules.
guard-pocket-isolation:
	@echo "== guard: pocket cores and store support preserve dependency direction =="
	@cd workshop/gopernicus && go test ./internal/commands -run '^TestPocketBoundaries/isolation$$' -count=1

# G3: sdk never imports outward (pockets/integrations/examples).
guard-sdk-no-outward:
	@echo "== guard: sdk never imports pockets/integrations/examples =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(pockets|integrations|examples)' sdk/ || { echo "ERROR: sdk imports an outward layer"; exit 1; }

# G4: no references to the old local module prefix remain.
guard-no-legacy-path:
	@echo "== guard: no legacy gopernicus/ import =="
	@! grep -rn --include='*.go' -E '"gopernicus/' . || { echo "ERROR: legacy gopernicus import found"; exit 1; }

# G5: shared pockets requires SDK only; concrete pocket cores may also require
# the exact shared pockets module, never a concrete peer. Direct requires only ("// indirect" lines are
# MVS bookkeeping, not a host-facing promise); a `tool` directive counts as a
# require; the dev-only relative `replace` of sdk is permitted pre-tag.
guard-pocket-dependencies:
	@echo "== guard: shared pockets requires SDK; concrete cores require SDK/shared pockets only (FS1) =="
	@fail=0; for f in pockets pockets/authentication pockets/authorization pockets/cms pockets/events pockets/jobs pockets/secrets; do \
		allowed='^github.com/gopernicus/gopernicus/sdk$$'; \
		if [ "$$f" != pockets ]; then allowed='^github.com/gopernicus/gopernicus/(sdk|pockets)$$'; fi; \
		extras=$$(awk '/^require \(/{inblk=1; next} inblk && /^\)/{inblk=0; next} inblk && !/\/\/ indirect/{print $$1} /^require [^(]/{print $$2}' $$f/go.mod \
			| grep -vE "$$allowed" || true); \
		tools=$$(grep -E '^tool ' $$f/go.mod | awk '{print $$2}' || true); \
		bad=$$(printf '%s\n%s\n' "$$extras" "$$tools" | grep -v '^$$' || true); \
		if [ -n "$$bad" ]; then echo "ERROR (FS1): $$f/go.mod has dependencies outside its SDK/shared-contract allowance:"; echo "$$bad"; fail=1; fi; \
	done; exit $$fail
	@modules=$$(cd pockets && go list -deps -test -f '{{if .Module}}{{.Module.Path}}{{end}}' ./...) || exit 1; \
		bad=$$(printf '%s\n' "$$modules" | sort -u | grep -vE '^$$|^github.com/gopernicus/gopernicus/(sdk|pockets)$$' || true); \
		if [ -n "$$bad" ]; then echo "ERROR (FS1): shared pockets imports outside SDK/stdlib (including tests):"; echo "$$bad"; exit 1; fi

# G6: production pocket responses use sdk/pkg/web, including files in the root
# and moved in-module store-support packages. Nested adapter modules are separate.
guard-pocket-transport-sdk-web:
	@echo "== guard: pocket transports use sdk/pkg/web responders (FS9) =="
	@cd workshop/gopernicus && go test ./internal/commands -run '^TestPocketBoundaries/transport$$' -count=1

# G7: neither a core nor its view modules import another pocket. The same
# module-aware check runs against scaffold output in the CLI tests.
guard-pocket-no-cross-pocket:
	@echo "== guard: pocket cores and views never import another pocket =="
	@cd workshop/gopernicus && go test ./internal/commands -run '^TestPocketBoundaries/cross-pocket$$' -count=1

# G25: the audited pockets' domain and logic packages have no HTTP dependency.
# CMS remains deferred; its existing isolation and transport rules still apply.
guard-pocket-logic:
	@echo "== guard: pocket domain and logic are transport independent =="
	@cd workshop/gopernicus && go test ./internal/commands -run '^TestPocketBoundaries/logic$$' -count=1

# G8 (authorization-v1 Z5, Q3 ADD): store adapter modules never import a
# DIFFERENT pocket — a store implements exactly its own pocket's ports over
# one connector — covering the stores/ subtrees G7 deliberately excludes. The
# pattern carries one extra alternation (steward minor 6): store→examples/
# imports, which no other guard watches. Same shape as G7: drop self-imports
# (pockets/<x>/...), anything left is a foreign reach.
guard-store-no-foreign-pocket:
	@echo "== guard: store modules never import a foreign pocket or examples (rule 6, stores) =="
	@fail=0; for d in pockets/*/stores/; do \
		[ -d "$$d" ] || continue; \
		x=$$(basename $$(dirname $$d)); \
		hits=$$(grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(pockets/[a-z0-9]+|examples)' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/pockets/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (rule 6, stores): a $$x store module reaches into a foreign pocket or an example host:"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail

# G9 (datastore-hardening P6, audit ruling 6): nothing outside the datastore
# connectors calls Underlying() — the escape hatch to the raw pool/DB is the
# service-locator workaround the scaffolded transaction.Transactor seam exists to
# prevent. A legitimate future host/cmd hit gets a named per-line exception
# HERE citing audit ruling 6 — never a regex weakening (the G6 discipline).
guard-no-underlying:
	@echo "== guard: no Underlying() outside the datastore connectors (transaction.Transactor seam) =="
	@! grep -rn --include='*.go' '\.Underlying()' --exclude-dir=integrations . || { echo "ERROR (P6/ruling 6): Underlying() called outside integrations/datastores — use the ports, or consume the transaction.Transactor seam"; exit 1; }

# G10 (datastore-hardening P6, audit ruling 8): RowToStructByNameLax silently
# tolerates missing fields — the quiet-data-loss variant of pgx struct
# scanning. Strict RowToStructByName only, everywhere, no exceptions.
guard-no-lax-scan:
	@echo "== guard: no RowToStructByNameLax anywhere (strict scanning only) =="
	@! grep -rn --include='*.go' 'RowToStructByNameLax' . || { echo "ERROR (P6/ruling 8): RowToStructByNameLax found — strict RowToStructByName only"; exit 1; }

# G11 (workshop-v2-scaffolding W1, review-gate fold item 7): the scaffolding CLI
# is isolated in BOTH directions. (a) Nothing outside workshop/ imports it — the
# CLI EMITS hosts and is never their runtime dependency. (b) workshop/ imports no
# pocket cores (pockets/) and no examples/ — it templates them, it never links
# them (a per-field/queries.sql pull is the v2b trigger, not a runtime import).
guard-workshop-boundary:
	@echo "== guard: workshop/ is isolated both directions (nothing imports it; it imports no pocket/example) =="
	@! grep -rn --include='*.go' --exclude-dir=workshop '"github.com/gopernicus/gopernicus/workshop' . || { echo "ERROR (W1): a non-workshop module imports the scaffolding CLI — workshop/ emits hosts, it is never a runtime dependency"; exit 1; }
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(pockets|examples)' workshop/ || { echo "ERROR (W1): workshop/ imports a pocket core or an example — the CLI templates them, it never links them"; exit 1; }

# G12 (sdk-layering, 2026-07-10; tests included 2026-08-27): the intra-sdk
# import law. Kernel = the root
# package (cycle-enforced against every subpackage that imports it; the grep
# below is the primary enforcement for the rest). pkg/* may import the
# ROOT only — FLAT, no pkg->pkg edges. capabilities/* may import
# root + pkg and its own subsystem subpackages (notify/email -> notify),
# never a different capability. Shared pocket composition lives in the separate
# pockets module. The law applies to production and test
# code alike: a test proves its package contract without coupling peer tiers.
guard-sdk-layering:
	@echo "== guard: sdk layering (kernel <- pkg <- capabilities) =="
	@! grep -n --include='*.go' '"github.com/gopernicus/gopernicus/sdk/' sdk/*.go 2>/dev/null || { echo "ERROR (G12a): the kernel (root package sdk) imports a subpackage"; exit 1; }
	@fail=0; for d in sdk/pkg/*/; do \
		x=$$(basename $$d); \
		hits=$$(grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/sdk/(pkg|capabilities)' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/sdk/pkg/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (G12b): pkg/$$x imports a sibling tier or upward — pkg imports the root only, including in tests:"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail
	@fail=0; for d in sdk/capabilities/*/; do \
		x=$$(basename $$d); \
		hits=$$(grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/sdk/capabilities' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/sdk/capabilities/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (G12c): capabilities/$$x imports another capability — including in tests, cross-capability composition leaves sdk"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail

# G21 (web-crud-list-request, 2026-08-27): sdk/pkg/list may import
# net/url (its ParseListQuery reads url.Values — transport vocabulary), never
# net/http. The reason is dependency weight, not taste: every store adapter
# (integrations/datastores/pgxdb, turso, each pocket's stores/*) imports list,
# so whatever list imports, every adapter carries. G12b greps gopernicus
# module paths only and cannot see a stdlib edge; this one-liner can.
# Production files only — a list test may drive an httptest server if it ever
# needs to; the adapters do not link tests.
guard-list-no-nethttp:
	@echo "== guard: sdk/pkg/list never imports net/http (G21) =="
	@! grep -rln --include='*.go' --exclude='*_test.go' '"net/http"' sdk/pkg/list/ || { echo "ERROR (G21): sdk/pkg/list imports net/http — list is transport vocabulary over url.Values and every store adapter carries its imports"; exit 1; }

# G23 (crud-write-vocabulary, 2026-08-31): sdk.Violation.Message is CALLER-FACING
# TEXT ONLY. web.ErrFromDomain now puts a ValidationError's sentences on the wire
# by concrete type, so wrapping a store/driver error string into a violation
# leaks internals to the caller — sdk.Refuse(field, code, err.Error()) and
# ve.Add(field, code, err.Error()) are the two shapes that do it. Comment lines
# are excluded so the rule can be stated in prose (sdk/faults.go,
# sdk/pkg/web/errors.go both name the anti-pattern).
# (G22 was reserved for the host-layout-contract PR-B `gopernicus guard` tool;
# it shipped as G30 `guard-host-contract`, host-contract-reconciliation.)
guard-violation-message-not-error:
	@echo "== guard: sdk.Violation messages never wrap a raw error string (G23) =="
	@! grep -rn --include='*.go' -E '(Refuse\(|\.Add\().*err\.Error\(\)' sdk/ pockets/ integrations/ examples/ | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' || { echo "ERROR (G23): a violation message wraps a raw error string — sdk.Violation.Message is caller-facing text only, never err.Error() from a store or driver"; exit 1; }

# (G24 `guard-firestore-mediation`, firestore-stores C8, retired 2026-10-23 with
# the store adapters it mediated; AUDIT-055. The number is not reused.)

# G13 (sdk-layering, 2026-07-10, folded steward finding): integrations never
# depend on other framework modules except SDK/self. Integrations implement
# SDK contracts using third-party providers; composition within a cohesive SDK
# subsystem needs no separate integration module. A legitimate future hit gets a named per-line exception HERE —
# never a regex weakening.
guard-integration-no-inward:
	@echo "== guard: integration framework imports and requirements stay within SDK/self =="
	@cd workshop/gopernicus && go test ./internal/commands -run '^TestIntegrationBoundaries$$' -count=1

# G14 (authv3-delivery-refactor AV3D-5.1): authentication owns NO bespoke durable
# delivery queue. The private deliveryjob domain, its `delivery_jobs` table, and
# its pgx/turso stores were removed — durable delivery is the generic jobs pocket
# reached through a host-wired Config.DeliveryDispatcher; auth owns no delivery
# table. This tripwire fails if either bespoke marker returns. The snake_case
# `delivery_jobs` (SQL table) is case-sensitive so it never matches the legitimate
# jobs-mode names (DeliveryJobKind, DeliveryJobRuntime, DeliveryJobsAcknowledged),
# and the deliveryjob package clause/import path catches a renamed resurrection.
guard-auth-no-delivery-repo:
	@echo "== guard: authentication owns no bespoke delivery table/repository (AV3D-5.1) =="
	@! grep -rn 'delivery_jobs' pockets/authentication || { echo "ERROR (AV3D-5.1): a 'delivery_jobs' table returned to authentication — durable delivery is the generic jobs pocket; auth owns no delivery table"; exit 1; }
	@! grep -rnE 'domain/deliveryjob|package deliveryjob' --include='*.go' pockets/authentication examples/auth-cms || { echo "ERROR (AV3D-5.1): the bespoke deliveryjob domain package returned — durable delivery is the generic jobs pocket, reached via Config.DeliveryDispatcher"; exit 1; }

# G15 (authv3-delivery-refactor AV3D-2.4/5.1): no auth producer calls a provider
# on the request path. Every outbound message is admitted through the delivery
# dispatcher seam; the actual send (Router.Deliver, email.Sender.Send,
# BodySender.Send) is the off-request processor's job. A producer that
# called any send verb directly would leak a secret on the request path, defeating
# enumeration safety. The delivery/ package (where those verbs legitimately live)
# is excluded; the AST-precise companion is TestNoProducerBypassesDispatcherSeam
# (producer_seam_test.go) — this coarse grep keeps the boundary in `make guard`.
guard-auth-no-request-time-provider:
	@echo "== guard: no authentication producer calls a provider on the request path (AV3D-2.4) =="
	@test -d pockets/authentication/logic
	@hits=$$(grep -rnE '\.(Deliver|Send|Notify)\(' --include='*.go' --exclude='*_test.go' pockets/authentication/logic | grep -v '/delivery/' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (AV3D-2.4): a producer package calls a provider-send verb directly — outbound must go through the delivery dispatcher seam, never a request-time send:"; echo "$$hits"; exit 1; fi

# G16 (authorizationv3 AZ3-5.3): authorization owns NO authorization-specific
# jobs/delivery table or repository. The v3 correctness kernel emits no effects
# (00-overview.md standing invariant: "Production/example wiring never relies on
# an authorization-specific jobs queue"); a later effects packet must consume the
# generic jobs pocket + a same-transaction events outbox, never an
# authorization-owned queue. This tripwire — the guard-auth-no-delivery-repo twin
# pointed at pockets/authorization migrations + repositories — fails if a
# delivery/jobs table or a bespoke deliveryjob domain package appears in the
# authorization pocket or its stores. The snake_case table tokens are
# case-sensitive so they never match a legitimate camelCase Go identifier.
guard-authorization-no-delivery-repo:
	@echo "== guard: authorization owns no bespoke jobs/delivery table/repository (AZ3-5.3) =="
	@! grep -rnE 'delivery_jobs|fenced_job_queue|job_queue|job_schedules' pockets/authorization || { echo "ERROR (AZ3-5.3): an authorization-specific jobs/delivery table returned to authorization — the v3 kernel emits no effects; durable delivery would be the generic jobs pocket reached via a same-transaction events outbox, never an authorization-owned queue"; exit 1; }
	@! grep -rnE 'domain/deliveryjob|package deliveryjob' --include='*.go' pockets/authorization || { echo "ERROR (AZ3-5.3): a bespoke deliveryjob domain package appeared in authorization — the v3 kernel ships no effects/delivery domain"; exit 1; }

# G19 (authorization-roles-model, 2026-08-26): the roles domain service
# (logic/roles) never imports the relationship engine, composite decider or cache.
# The decision surface composes downward onto roles; the leaf must not import
# the composition that consumes it.
guard-authorization-rolesvc-no-engine:
	@echo "== guard: authorization roles imports no relationship, decision or cache engine (G19) =="
	@test -d pockets/authorization/logic/roles
	@! grep -rnE 'logic/(relationships|decisions|tuplecache)["`/]' --include='*.go' pockets/authorization/logic/roles || { echo "ERROR (G19): roles imports the relationship engine, composite decider or cache — decisions composes onto roles, never the reverse"; exit 1; }

# G26: canonical tuple vocabulary sits below every authorization consumer, including
# the model and cache. Keep its tests on the same side of that boundary.
guard-authorization-tuples-leaf:
	@echo "== guard: authorization tuple vocabulary imports no authorization consumer (G26) =="
	@test -d pockets/authorization/logic/tuples
	@! (grep -rnE '"github.com/gopernicus/gopernicus/pockets/authorization(/|")' --include='*.go' pockets/authorization/logic/tuples | grep -vE '"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples(/[^" ]+)?"') || { echo "ERROR (G26): tuple vocabulary imports an authorization consumer — roles, relationships, models and caches depend on tuples, never the reverse"; exit 1; }

# G17 (ui-goth GOTH-0.2, 2026-07-17): the seventh module kind — a UI
# implementation (a reusable presentation system for one rendering/runtime
# family: ui/goth today, ui/react/ui/vue later) — imports NO pockets/,
# integrations/, examples/, or workshop/. It owns its view-library dependencies,
# semantic tokens, components, controllers, and distributable assets, and it owns
# no domain schema or routes; it may depend on its own view/runtime libraries
# (templ and its pinned inputs) and sdk, but never inward. A pocket reaches a UI
# implementation only through its own views/<pkg> adapter module (which legally
# imports ui/goth) — never the other way. Same import-direction discipline as G13
# (integrations import no inward): the arrow is what keeps "UI implementation"
# meaning something. Third-party view/runtime deps (github.com/a-h/templ, …) never
# match the inward gopernicus/(pockets|integrations|examples|workshop) pattern,
# so a legal presentation dependency is not misclassified. A legitimate future hit
# gets a named per-line exception HERE — never a regex weakening.
guard-ui-no-inward:
	@echo "== guard: ui/* implementations import no pockets/integrations/examples/workshop =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(pockets|integrations|examples|workshop)' ui/ || { echo "ERROR (G17): a ui/ implementation imports inward — a UI implementation depends on its own view/runtime libraries and sdk, never a pocket/integration/example/workshop"; exit 1; }

# G18 (ui-goth GOTH-1.1, 2026-07-17): the UI-implementation go.mod require
# whitelist — the G5 analogue for the seventh module kind. A ui/* implementation's
# go.mod requires ONLY its own view/runtime library (github.com/a-h/templ) plus
# sdk; nothing else (direct requires only — "// indirect" lines are MVS
# bookkeeping, and the templ `tool` directive is that same view library, not a new
# dependency). The frozen GOTH-0.3 surface imports no sdk, but the taxonomy
# (ARCHITECTURE.md UI-implementation row) permits it, so sdk stays on the
# whitelist. It also imports no pockets: a UI implementation is not a pocket
# composer (the pockets.Mount seam is a host/pocket concern). A legitimate
# future view library gets a named addition to the whitelist HERE — never a regex
# weakening.
guard-ui-require-whitelist:
	@echo "== guard: ui/* go.mod requires only templ + sdk, and never imports pockets (G5 analogue) =="
	@fail=0; for f in ui/*/go.mod; do \
		[ -f "$$f" ] || continue; \
		extras=$$(awk '/^require \(/{inblk=1; next} inblk && /^\)/{inblk=0; next} inblk && !/\/\/ indirect/{print $$1} /^require [^(]/{print $$2}' $$f \
			| grep -vE '^(github.com/a-h/templ|github.com/gopernicus/gopernicus/sdk)$$' || true); \
		if [ -n "$$extras" ]; then echo "ERROR (G18): $$f requires beyond templ/sdk:"; echo "$$extras"; fail=1; fi; \
	done; exit $$fail
	@! grep -rn --include='*.go' '"github.com/gopernicus/gopernicus/pockets' ui/ || { echo "ERROR (G18): a ui/ implementation imports pockets — a UI implementation is not a pocket composer"; exit 1; }

# >>> LEGACY-PATTERNS — self-exempt block (see SELF-EXEMPTION below). Every
# literal legacy string in this Makefile lives between these two markers.
#
# G20 (rename-pockets, 2026-08-27): the retired third-tier vocabulary never
# comes back. `features` was removed COMPLETELY — no shim, no alias, no
# surviving path or verb — so this guard fails on any legacy path, phrase, or
# tier-derived identifier in ANY TRACKED file, regardless of extension
# (.tmpl, .tsx, .yml, .sql and go.sum are known necessary COVERAGE, not
# exceptions), and on the existence of any legacy directory/file artifact.
# `git grep` is deliberate: it scans tracked files only, so ignored/untracked
# dependency trees (node_modules/, emitted scaffolds, build output) are never
# walked and never false-positive.
#
# Excluded, and ONLY these: ratified/executed history — plans/, .claude/plans/,
# .claude/past/ — all of RELEASING.md, whose release chronicle intentionally
# names immutable old tags at their old module paths; the dated decision logs
# NOTES.md and examples/cms/NOTES.md (every entry is an as-executed record);
# and applied migration files under pockets/*/stores/*/migrations/, because
# both migration runners (pgxdb/migrate.go, turso/migrate.go) SHA-256 the WHOLE
# file, comments included, and hard-fail a migrated host on mismatch — so a
# shipped migration is immutable bytes, comment vocabulary included.
#
# SELF-EXEMPTION: the patterns necessarily appear in this file. Rather than
# excluding the whole Makefile (a hole the size of the build system), the guard
# blanks exactly the lines between the LEGACY-PATTERNS markers — this block —
# with a sed range that PRESERVES line numbering, then greps the remainder. So
# every other Makefile line is still policed, and only the definitions are
# exempt. This is the extension-scoped `--include='*.go'` trick of
# guard-no-legacy-path (G4) made precise, because G20 is not extension-scoped.
LEGACY_TIER_RE = features/|sdk/feature|new feature|eventsfeature|feature\.(Mount|Group|PrefixRegistrar|RouteRegistrar)
LEGACY_TIER_ARTIFACTS = features sdk/feature \
	workshop/gopernicus/internal/commands/feature.go \
	workshop/gopernicus/internal/commands/feature_scaffold_test.go \
	workshop/gopernicus/internal/commands/templates/feature \
	workshop/documentation/docs/features \
	workshop/documentation/docs/guides/create-feature.md \
	workshop/documentation/docs/architecture/feature-contract.md
guard-no-legacy-features-path:
	@echo "== guard: the retired features/ tier vocabulary never returns (G20) =="
	@fail=0; \
	hits=$$(git grep -nE '$(LEGACY_TIER_RE)' -- \
		':(exclude)plans' ':(exclude).claude/plans' ':(exclude).claude/past' \
		':(exclude)RELEASING.md' ':(exclude)NOTES.md' ':(exclude)examples/cms/NOTES.md' \
		':(exclude)pockets/*/stores/*/migrations/*.sql' ':(exclude)Makefile' || true); \
	self=$$(sed '/^# >>> LEGACY-PATTERNS/,/^# <<< LEGACY-PATTERNS/s/.*//' Makefile \
		| grep -nE '$(LEGACY_TIER_RE)' | sed 's#^#Makefile:#' || true); \
	all=$$(printf '%s\n%s\n' "$$hits" "$$self" | grep -v '^$$' || true); \
	if [ -n "$$all" ]; then \
		echo "ERROR (G20): the retired features/ vocabulary is back — the third tier is pockets/ and its shared contract is pockets (no shim, no alias):"; \
		echo "$$all"; fail=1; \
	fi; \
	for a in $(LEGACY_TIER_ARTIFACTS); do \
		if [ -e "$$a" ]; then echo "ERROR (G20): a legacy artifact exists on disk: $$a"; fail=1; fi; \
	done; \
	exit $$fail
# <<< LEGACY-PATTERNS

# CI-style gate: templ generation must be a no-op (no drift), then per-module
# vet/build/test across all MODULES, then the thirty-six layering guards. Drift
# is checked via `git diff` when this tree is a git repo; this repo IS a git
# repo (as of phase 2), so that branch runs. The before/after checksum branch
# remains as a fallback for gitless checkouts of *_templ.go.
# The workshop scaffold-compile tests tidy emitted modules with GOPROXY=off —
# deliberate hermetic design ("a cold cache fails loud"). But an emitted
# module's ISOLATED MVS can select transitive versions LOWER than any repo
# module ever downloads (golang.org/x/sys, ncruces/go-strftime via the libsql
# graph), so a minimal GOMODCACHE — CI's — fails them even though every repo
# module builds. This target makes their warm-cache assumption true by
# construction: TestWarmScaffoldModuleCache (build tag warmcache, excluded from
# plain `go test`) re-runs the same template emissions with the proxy ON and
# tidies them, downloading exactly the module set the hermetic tidies resolve.
# Warm cache ⇒ near-instant no-op; `check` runs it before the module loop.
warm-scaffold-cache:
	@echo "== warm scaffold module cache =="
	@cd workshop/gopernicus && go test -tags warmcache -count=1 -run '^TestWarmScaffoldModuleCache$$' ./internal/commands

check:
	@if [ -d .git ]; then \
		$(MAKE) generate; \
		git diff --exit-code -- '*_templ.go' || { echo "ERROR: templ generation drift (git diff)"; exit 1; }; \
		git diff --exit-code -- ui/goth/assets/dist ui/goth/assets/manifest.json || { echo "ERROR: ui/goth asset drift (git diff) — regenerate with 'make generate-ui-assets' and commit dist/ + manifest.json"; exit 1; }; \
	else \
		before=$$(find . -name '*_templ.go' -not -path './.git/*' -exec shasum {} \; | sort); \
		$(MAKE) generate >/dev/null; \
		after=$$(find . -name '*_templ.go' -not -path './.git/*' -exec shasum {} \; | sort); \
		if [ "$$before" != "$$after" ]; then echo "ERROR: templ generation drift"; exit 1; fi; \
	fi
	@$(MAKE) warm-scaffold-cache
	@for m in $(MODULES); do echo "== $$m =="; (cd $$m && go vet ./... && go build ./... && go test ./...) || exit 1; done
	@echo "== integration-tag vet (compile-only, no DB) =="
	@for m in $(INTEGRATION_TAG_MODULES); do echo "== vet -tags=integration $$m =="; (cd $$m && go vet -tags=integration ./...) || exit 1; done
	@for m in $(PROVIDER_LIVE_TAG_MODULES); do echo "== vet -tags=live $$m =="; (cd $$m && go vet -tags=live ./...) || exit 1; done
	@for m in $(EMULATOR_TAG_MODULES); do echo "== vet -tags=emulator $$m =="; (cd $$m && go vet -tags=emulator ./...) || exit 1; done
	@$(MAKE) guard
	@echo "all checks passed"

# G27: compiled schema validation may be used by writes; decisions never import mutations.
guard-authorization-decisions-no-mutations:
	@echo "== guard: authorization decisions imports no mutation layer (G27) =="
	@! grep -rnE 'logic/mutations["`/]' --include='*.go' pockets/authorization/logic/decisions || { echo "ERROR (G27): decisions imports mutations — tuple writes consume compiled schemas, never the reverse"; exit 1; }

# G28: application access policy is invoked at inbound, not by domain/storage services.
guard-inbound-authorization:
	@echo "== guard: host authorization callbacks stay at inbound (G28) =="
	@! grep -rnE 'InviteCheck|inviteCheck|InvitationCan([^a-zA-Z]|$$)|InvitationResourceRule|checkResource|UserAdminCheck|userAdminCheck|CreateAuthorized|ListByResourceAuthorized|AuthorizeUserAdmin' --include='*.go' --exclude='*_test.go' pockets/authentication/logic || { echo "ERROR (G28): authentication logic retains an inbound host-policy callback or authorized wrapper"; exit 1; }
	@! grep -rnE '"github.com/gopernicus/gopernicus/pockets/authorization/logic/(decisions|model)"' --include='*.go' --exclude='*_test.go' examples/auth-cms/internal/logic/domains/documents examples/auth-cms/internal/outbound/domains/documents || { echo "ERROR (G28): document logic/storage invokes the authorization engine; inbound selects query restrictions"; exit 1; }

# G29: tuple writes enforce data integrity, never principal permission policy.
guard-tuple-write-integrity:
	@echo "== guard: tuple write services and stores contain no principal authorization (G29) =="
	@! grep -rnE 'MutationGuard|AuthorizeMutation|ApplyGuarded|StoreDecisionView|SystemMutator|PrincipalFromContext|\*decisions.Service' --include='*.go' --exclude='*_test.go' pockets/authorization/logic/mutations pockets/authorization/stores/memory pockets/authorization/stores/pgx pockets/authorization/stores/turso || { echo "ERROR (G29): tuple writers retain principal policy; inbound admits commands, stores enforce IntegrityPolicy"; exit 1; }

# external-identity-providers (D4, 2026-10-20): identity vendors are wired through
# sdk/capabilities/authentication and integrations/authentication/<vendor>; no
# vendor name enters the authentication pocket's production code. Store modules,
# tests and READMEs are excluded.
guard-auth-no-vendor-names:
	@echo "== guard: authentication names no identity vendor (external-identity-providers D4) =="
	@test -d pockets/authentication/logic
	@hits=$$(grep -rniE 'firebase|auth0|clerk|cognito|workos|keycloak|okta' --include='*.go' --exclude='*_test.go' --exclude-dir=stores pockets/authentication || true); \
		if [ -n "$$hits" ]; then echo "ERROR (external-identity-providers D4): a vendor name entered the authentication pocket — vendor code belongs in integrations/authentication/<vendor>:"; echo "$$hits"; exit 1; fi

# G30 (host-contract-reconciliation, 2026-10-20): the in-repo hosts pass the host
# contract's executable floor — every examples/README.md §8 row is guarded.
# examples/* never pin the tool (G11); the verb is run from this checkout.
guard-host-contract:
	@echo "== guard: example hosts pass gopernicus guard (H0–H14) (G30) =="
	@cd workshop/gopernicus && for h in cms minimal jobs-minimal auth-cms goth-showcase external-identity production-baseline; do \
		echo "-- examples/$$h"; go run . guard ../../examples/$$h || exit 1; done

# G31 (SOC 2 S1, cms admin deny-by-absence): since pockets/cms v0.4.0 a
# cms.Config with neither AdminMiddleware nor AdminUngatedAcknowledged leaves the
# admin surface unmounted — a WARN, not a boot refusal. Every file under
# examples/ (tests included) that builds a cms.Config literal names its admin
# posture: a gate or the demo acknowledgement. Downstream hosts get the same
# signal from `gopernicus guard --posture` (P8).
guard-cms-admin-gate:
	@echo "== guard: example cms.Config literals name their admin posture (G31) =="
	@fail=0; for f in $$(grep -rl --include='*.go' --exclude-dir=node_modules 'cms\.Config{' examples || true); do \
		if ! grep -qE 'AdminMiddleware|AdminUngatedAcknowledged' "$$f"; then \
			echo "ERROR (G31): $$f builds a cms.Config naming neither AdminMiddleware nor AdminUngatedAcknowledged — its admin surface is unmounted (deny-by-absence); pass an authorizing gate or acknowledge the demo posture"; fail=1; \
		fi; \
	done; exit $$fail

# G32 (SOC 2 S1, production posture): every G30 host also passes the opt-in
# production-posture rules P0–P9 of examples/README.md §3b — the mode from the
# process environment, RuntimeMode on every recognized surface, TrustProxies
# outermost, SecurityHeaders, ValidateProduction, literal acknowledgements,
# NewKeys, a posture-clean .env.example, and (S2) a byte-identical ledger copy
# of every mounted store's migrations. Like G30, the verb runs from this
# checkout (examples/* never pin the tool, G11).
guard-host-posture:
	@echo "== guard: example hosts pass gopernicus guard --posture (P0–P9) (G32) =="
	@cd workshop/gopernicus && for h in cms minimal jobs-minimal auth-cms goth-showcase external-identity production-baseline; do \
		echo "-- examples/$$h"; go run . guard --posture ../../examples/$$h || exit 1; done

# G33 (SOC 2 S1, O-5): a pocket never reads the process environment. The host
# resolves configuration and passes values in — ParseEnvTags over a pocket's
# Config, authentication.KeysInput + NewKeys for key material — so pocket
# production code (store modules excluded) names no os.Getenv, os.LookupEnv,
# environment.ParseEnvTags, environment.LoadEnv*, or environment.Secret(.
# Comment lines are skipped so a doc comment may name the rule it follows.
guard-pocket-no-env:
	@echo "== guard: pocket production code never reads the process environment (G33) =="
	@hits=$$(grep -rnE 'os\.Getenv|os\.LookupEnv|environment\.ParseEnvTags|environment\.LoadEnv|environment\.Secret\(' --include='*.go' --exclude='*_test.go' --exclude-dir=stores pockets | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (G33): pocket production code reads the process environment — the host resolves configuration and passes it in (ParseEnvTags over the pocket's Config; authentication.KeysInput + NewKeys):"; echo "$$hits"; exit 1; fi

# G34 (SOC 2 S2, ledger completeness): P9 over the reference host, in this
# repo's own gate. For every store module examples/production-baseline/go.mod
# requires that ships a migrations/ directory, every NNNN_<name>.sql there has a
# byte-identical copy in the baseline ledger under any number (hosts renumber;
# the version is the filename, so a renumbered copy keeps its bytes). A drifted
# ledger is reconciled by re-export — a file copy, never an edit.
guard-baseline-ledger:
	@echo "== guard: the baseline ledger holds every mounted store migration byte-identical (G34) =="
	@ledger=examples/production-baseline/workshop/migrations/primary; fail=0; \
	stores=$$(grep -oE 'github\.com/gopernicus/gopernicus/pockets/[a-z0-9_-]+/stores/[a-z0-9_-]+ ' examples/production-baseline/go.mod | sed 's#^github\.com/gopernicus/gopernicus/##'); \
	if [ -z "$$stores" ]; then echo "ERROR (G34): examples/production-baseline/go.mod requires no store module — the guard would check nothing"; exit 1; fi; \
	for s in $$stores; do \
		[ -d "$$s/migrations" ] || continue; \
		for f in "$$s"/migrations/[0-9]*_*.sql; do \
			[ -f "$$f" ] || continue; \
			suffix=$${f##*/}; suffix=$${suffix#*_}; found=0; \
			for c in "$$ledger"/[0-9]*_"$$suffix"; do \
				name=$${c##*/}; \
				if [ -f "$$c" ] && [ "$$(echo "$$name" | sed 's/^[0-9]*_//')" = "$$suffix" ] && cmp -s "$$f" "$$c"; then found=1; fi; \
			done; \
			if [ $$found = 0 ]; then echo "ERROR (G34): $$f has no byte-identical copy in $$ledger (re-export: copy the store file into the ledger; never edit a copy)"; fail=1; fi; \
		done; \
	done; exit $$fail

# G35 (SOC 2 S3, the rails are append-only in code too): no store rewrites or
# removes an audit-rail row except on a reviewed line. A non-test Go file under
# pockets/*/stores fails when one line names UPDATE, DELETE FROM, INSERT OR
# REPLACE INTO, TRUNCATE or DROP TRIGGER and, later on it, a rail — in BOTH
# spellings: the literal table names the turso stores write (security_events,
# iam_audit, secrets_audit) and the concatenated forms the pgx stores write
# (s.table(securityEventsTable), cfg.schema.Table("iam_audit"),
# r.table("secrets_audit")). The word boundaries keep security_events_retention
# and security_events_purge_before out; iam_operations is not a rail
# (AllowDelete). The pgx stores only CALL the 0024 definer function. The
# allow-list is PER LINE, by file and exact text — never a whole file: the turso
# rail purge's DELETE (its DROP TRIGGER line drops the captured names and names
# no rail, so it never matches) and the two turso canonical trigger texts
# (CREATE TRIGGER … BEFORE UPDATE ON <rail>), DDL the boot probe compares, not
# DML. Comment lines are skipped, and the pattern must still match the
# allow-listed purge line, so a grep that stops matching cannot pass silently.
# A SECOND pattern catches a rail trigger dropped BY NAME, which the first
# cannot see (a SQLite DROP TRIGGER names only the trigger): DROP TRIGGER
# [IF EXISTS] [<schema>.]<name>, case-insensitive, quoted or bare, where the
# name holds security_events, iam_audit or secrets_audit as an _-delimited
# token. It has no allow-list: the turso purge drops the triggers it captured
# from sqlite_schema (DROP TRIGGER `+quoteName(t.name)), a line that names no
# rail, so no store line matches it today and any that does is a violation. Its
# self-check runs the pattern over the canonical rail trigger names.
G35_PATTERN = (UPDATE|DELETE[[:space:]]+FROM|INSERT[[:space:]]+OR[[:space:]]+REPLACE[[:space:]]+INTO|TRUNCATE|DROP[[:space:]]+TRIGGER)[^;]*(\bsecurity_events\b|securityEventsTable|\biam_audit\b|\bsecrets_audit\b)
G35_ALLOWED = \
	-e 'pockets/authentication/stores/turso/maintenance.go:res, err := tx.Exec(ctx, `DELETE FROM security_events WHERE id IN (SELECT id FROM security_events WHERE created_at <= ? ORDER BY created_at, id LIMIT ?)`, cutoff, limit)' \
	-e 'pockets/authentication/stores/turso/turso.go:securityEventsImmutableUpdateSQL = `CREATE TRIGGER security_events_immutable_update BEFORE UPDATE ON security_events' \
	-e 'pockets/authorization/stores/turso/turso.go:iamAuditImmutableUpdate = `CREATE TRIGGER iam_audit_immutable_update BEFORE UPDATE ON iam_audit'
G35_DROP_PATTERN = DROP[[:space:]]+TRIGGER([[:space:]]+IF[[:space:]]+EXISTS)?[[:space:]]+([`"]?[A-Za-z0-9_]+[`"]?\.)?[`"]?([A-Za-z0-9_]*_)?(security_events|iam_audit|secrets_audit)(_[A-Za-z0-9_]*)?([^A-Za-z0-9_]|$$)
guard-rail-no-dml:
	@echo "== guard: stores rewrite or remove no audit-rail row outside the per-line allow-list (G35) =="
	@grep -qE '$(G35_PATTERN)' pockets/authentication/stores/turso/maintenance.go || { echo "ERROR (G35): the pattern no longer matches the allow-listed turso purge line — the guard would check nothing"; exit 1; }
	@{ grep -rnE --include='*.go' --exclude='*_test.go' '$(G35_PATTERN)' pockets/*/stores || true; } | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' | { \
		fail=0; while IFS= read -r hit; do \
			file=$${hit%%:*}; rest=$${hit#*:}; text=$$(printf '%s' "$${rest#*:}" | sed -E 's/^[[:space:]]+//'); \
			if printf '%s\n' "$$file:$$text" | grep -qxF $(G35_ALLOWED); then continue; fi; \
			echo "ERROR (G35): $$hit — the rails are append-only: write them by plain INSERT; purge security_events through the 0024 definer function (pgx) or the reviewed turso purge line"; fail=1; \
		done; exit $$fail; }
	@test "$$(printf '%s\n' 'DROP TRIGGER security_events_immutable_update' 'drop trigger if exists "iam_audit_immutable_update"' 'DROP TRIGGER IF EXISTS main.secrets_audit_no_truncate' 'DROP TRIGGER xsecurity_eventsx' | grep -ciE '$(G35_DROP_PATTERN)')" = 3 || { echo "ERROR (G35): the drop-by-name pattern no longer matches exactly the three canonical rail trigger samples — the guard would check nothing"; exit 1; }
	@{ grep -rniE --include='*.go' --exclude='*_test.go' '$(G35_DROP_PATTERN)' pockets/*/stores || true; } | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' | { \
		fail=0; while IFS= read -r hit; do \
			echo "ERROR (G35): $$hit — a rail trigger is dropped by name: the rails' immutability triggers are dropped only by the turso purge's capture-drop-restore transaction"; fail=1; \
		done; exit $$fail; }

# G36 (SOC 2 S3, the maintenance leaf): the authentication retention service
# sits beside the authentication service, never above it. It imports the leaf
# domain packages it purges through (…/logic/authentication/<leaf>) and never
# the service package itself, so the pocket root can build both without a
# cycle and the pass can never reach into the use cases. G26's shape; tests
# stay on the same side.
guard-auth-maintenance-leaf:
	@echo "== guard: authentication maintenance imports no authentication service (G36) =="
	@test -d pockets/authentication/logic/maintenance
	@! grep -rn '"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"' --include='*.go' pockets/authentication/logic/maintenance || { echo "ERROR (G36): logic/maintenance imports the authentication service package — maintenance depends on the leaf domain packages (securityevent, challenge, user, …), never the service"; exit 1; }

# G37 (SOC 2 S6, FSK-1): a credential-named field under integrations/ or sdk/
# is an sdk.Secret, never a plain string or []byte, so fmt, slog and
# encoding/json render it [redacted]. The pattern matches an indented field
# named Password, Secret, SecretAccessKey, APIKey, AuthToken, ClientSecret,
# CredentialsJSON, PrivateKey, DSN or Token, alone or after a capitalised
# prefix (DBPassword, SMTPAuthToken), typed string or []byte. Skipped:
# _test.go files; lines carrying a json:" tag (wire DTOs a provider response
# decodes into, not configuration); and sdk/capabilities/oauth/oauth.go by path
# (oauth.Tokens holds runtime token values with their own lifetime, not
# config). URL is not in the list (too generic; the type, not the guard,
# protects the goredis and turso URL fields), and pockets are out by path
# (inbound DTOs carry Password string json:"password"). Unexported fields are
# lower-case and never match: an adapter holding a secret in one implements
# fmt.Formatter and slog.LogValuer itself. The self-check runs the pattern over
# canonical samples, so a pattern that stops matching cannot pass silently.
G37_PATTERN = ^[[:space:]]+([A-Z][A-Za-z]*)?(Password|Secret|SecretAccessKey|APIKey|AuthToken|ClientSecret|CredentialsJSON|PrivateKey|DSN|Token)[[:space:]]+(string|\[\]byte)
guard-credential-fields-secret:
	@echo "== guard: credential-named fields in integrations and sdk are sdk.Secret (G37) =="
	@test "$$(printf '\t%s\n' 'Password string' 'DBPassword  string' 'CredentialsJSON []byte' 'Password sdk.Secret' 'URL string' 'apiKey string' | grep -E '$(G37_PATTERN)' | tr -d '\t' | tr '\n' ',')" = 'Password string,DBPassword  string,CredentialsJSON []byte,' || { echo "ERROR (G37): the pattern no longer matches exactly the three canonical credential-field samples — the guard would check nothing"; exit 1; }
	@hits=$$({ grep -rnE --include='*.go' --exclude='*_test.go' '$(G37_PATTERN)' integrations sdk || true; } | grep -v 'json:"' | grep -v '^sdk/capabilities/oauth/oauth.go:'); \
		if [ -n "$$hits" ]; then echo "ERROR (G37): a credential-named field is a plain string or []byte — make it sdk.Secret (it renders [redacted] through fmt, slog and encoding/json; Reveal() is the one read):"; echo "$$hits"; exit 1; fi
