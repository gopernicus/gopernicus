# gopernicus — framework monorepo (sdk + integrations + features + examples)
#
# Multi-module workspace (go.work), 36 modules. templ is pinned via the `tool`
# directive in features/cms/views/templ/go.mod (where the .templ sources live),
# so `go tool templ` is reproducible.

MODULES = sdk integrations/cryptids/bcrypt integrations/cryptids/golang-jwt integrations/cryptids/google-uuid integrations/datastores/pgxdb integrations/datastores/turso integrations/email/sendgrid integrations/filestorage/gcs integrations/filestorage/s3 integrations/kvstores/goredis integrations/notify/mailer integrations/oauth/github integrations/oauth/google integrations/scheduling/robfig-cron integrations/tracing/otel features/authentication features/authentication/stores/pgx features/authentication/stores/turso features/authentication/views/templ features/authorization features/authorization/stores/pgx features/authorization/stores/turso features/cms features/cms/stores/pgx features/cms/stores/turso features/cms/views/templ features/events features/events/stores/pgx features/events/stores/turso features/jobs features/jobs/stores/pgx features/jobs/stores/turso examples/auth-cms examples/cms examples/jobs-minimal examples/minimal workshop/gopernicus

# STORE_MODULES carry env-gated live conformance suites (storetest against a real
# database). `make check`/`make test` run them hermetically (loud skips); `make
# test-stores` runs them EXPECTING the datastore env vars set.
STORE_MODULES = features/cms/stores/pgx features/cms/stores/turso features/authentication/stores/pgx features/authentication/stores/turso features/jobs/stores/pgx features/jobs/stores/turso features/events/stores/pgx features/events/stores/turso features/authorization/stores/pgx features/authorization/stores/turso

.PHONY: generate build vet test test-stores run migrate check tidy guard warm-scaffold-cache \
	guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-feature-core-sdk-only guard-feature-transport-sdk-web guard-feature-no-cross-feature \
	guard-store-no-foreign-feature guard-no-underlying guard-no-lax-scan \
	guard-workshop-boundary guard-sdk-layering guard-integration-no-inward \
	guard-auth-no-delivery-repo guard-auth-no-request-time-provider \
	guard-authorization-no-delivery-repo

# Regenerate *_templ.go from .templ sources. Each bundled views/templ module pins
# its own templ tool; generation runs inside each so the tool version is
# reproducible per module.
generate:
	cd features/cms/views/templ && go tool templ generate
	cd features/authentication/views/templ && go tool templ generate

build: generate
	@for m in $(MODULES); do echo "build $$m"; (cd $$m && go build ./...) || exit 1; done

vet:
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done

test:
	@for m in $(MODULES); do echo "test $$m"; (cd $$m && go test ./...) || exit 1; done

# test-stores runs the dialect store modules' live conformance suites, EXPECTING
# the datastore env vars set (vs `make check`/`make test`, which skip loudly and
# stay hermetic). It fails loudly if POSTGRES_TEST_DSN is unset — this milestone's
# proof is the live postgres run. The turso leg is `-tags=integration` and skips
# loudly without TURSO_DATABASE_URL/TURSO_AUTH_TOKEN.
#
# Spin a local postgres and run:
#   docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17
#   POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' make test-stores
test-stores:
	@if [ -z "$$POSTGRES_TEST_DSN" ]; then \
		echo "ERROR: POSTGRES_TEST_DSN not set — postgres store conformance cannot run (make check stays hermetic; this target expects it)"; \
		echo "  docker run --rm -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:17"; \
		echo "  POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' make test-stores"; \
		exit 1; \
	fi
	@echo "== features/cms/stores/pgx (live) =="
	@cd features/cms/stores/pgx && go test ./...
	@echo "== features/authentication/stores/pgx (live) =="
	@cd features/authentication/stores/pgx && go test ./...
	@echo "== features/jobs/stores/pgx (live) =="
	@cd features/jobs/stores/pgx && go test ./...
	@echo "== features/cms/stores/turso (live, -tags=integration) =="
	@cd features/cms/stores/turso && go test -tags=integration ./...
	@echo "== features/authentication/stores/turso (live, -tags=integration) =="
	@cd features/authentication/stores/turso && go test -tags=integration ./...
	@echo "== features/jobs/stores/turso (live, -tags=integration) =="
	@cd features/jobs/stores/turso && go test -tags=integration ./...
	@echo "== features/events/stores/pgx (live) =="
	@cd features/events/stores/pgx && go test ./...
	@echo "== features/events/stores/turso (live, -tags=integration) =="
	@cd features/events/stores/turso && go test -tags=integration ./...
	@echo "== features/authorization/stores/pgx (live) =="
	@cd features/authorization/stores/pgx && go test ./...
	@echo "== features/authorization/stores/turso (live, -tags=integration) =="
	@cd features/authorization/stores/turso && go test -tags=integration ./...

# The server binary never migrates; `run` applies host-owned migrations first
# (pre-boot), then serves — keeping migration a separate, explicit step.
run: generate migrate
	cd examples/cms && go run ./cmd/server

# Migrations are HOST-OWNED and applied pre-boot (never by the framework at
# startup). The CMS feature's SQL is scaffolded into the host's own dir
# (examples/cms/workshop/migrations/primary) and applied by the host's runner.
migrate:
	cd examples/cms && go run ./workshop/migrations

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

# Layering guards — each enforces one architectural boundary from the
# constitution (00-overview.md) or the feature-standard charter (FS rules,
# 2026-07-07); every target must print nothing and exit 0 on a clean tree.
# `make guard` runs all sixteen.
guard: guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-feature-core-sdk-only guard-feature-transport-sdk-web guard-feature-no-cross-feature \
	guard-store-no-foreign-feature guard-no-underlying guard-no-lax-scan \
	guard-workshop-boundary guard-sdk-layering guard-integration-no-inward \
	guard-auth-no-delivery-repo guard-auth-no-request-time-provider \
	guard-authorization-no-delivery-repo

# G1: sdk imports only the standard library (also enforced structurally by
# sdk/go.mod having no require block).
guard-sdk-stdlib:
	@echo "== guard: sdk imports only the standard library =="
	@! grep -rn --include='*.go' '"github.com/' sdk/ | grep -v '"github.com/gopernicus/gopernicus/sdk' || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }
	@! grep -rnE '"(cloud\.google\.com|golang\.org/x|gopkg\.in)/' --include='*.go' sdk/ || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }

# G2: every feature core (features/*, excluding their own store/views adapter
# modules) never imports integrations, examples, or any feature's stores or
# views (A4: generalized from features/cms to all features/*; views added
# 2026-07-07, feature-standard FS3).
guard-feature-isolation:
	@echo "== guard: features/* cores never import integrations/examples/their own stores/views =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(integrations|examples|features/[a-z0-9]+/(stores|views))' features --exclude-dir=stores --exclude-dir=views || { echo "ERROR: a features/* core imports an adapter layer"; exit 1; }

# G3: sdk never imports outward (features/integrations/examples).
guard-sdk-no-outward:
	@echo "== guard: sdk never imports features/integrations/examples =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(features|integrations|examples)' sdk/ || { echo "ERROR: sdk imports an outward layer"; exit 1; }

# G4: no references to the old local module prefix remain.
guard-no-legacy-path:
	@echo "== guard: no legacy gopernicus/ import =="
	@! grep -rn --include='*.go' -E '"gopernicus/' . || { echo "ERROR: legacy gopernicus import found"; exit 1; }

# G5 (FS1, feature-standard 2026-07-07): every feature core go.mod requires
# exactly sdk — nothing else. Direct requires only ("// indirect" lines are
# MVS bookkeeping, not a host-facing promise); a `tool` directive counts as a
# require; the dev-only relative `replace` of sdk is permitted pre-tag.
guard-feature-core-sdk-only:
	@echo "== guard: feature core go.mod requires sdk only (FS1) =="
	@fail=0; for f in features/authentication features/authorization features/cms features/events features/jobs; do \
		extras=$$(awk '/^require \(/{inblk=1; next} inblk && /^\)/{inblk=0; next} inblk && !/\/\/ indirect/{print $$1} /^require [^(]/{print $$2}' $$f/go.mod \
			| grep -v '^github.com/gopernicus/gopernicus/sdk$$' || true); \
		tools=$$(grep -E '^tool ' $$f/go.mod | awk '{print $$2}' || true); \
		bad=$$(printf '%s\n%s\n' "$$extras" "$$tools" | grep -v '^$$' || true); \
		if [ -n "$$bad" ]; then echo "ERROR (FS1): $$f/go.mod requires more than sdk:"; echo "$$bad"; fail=1; fi; \
	done; exit $$fail

# G6 (FS9, feature-standard 2026-07-07): feature transports respond via
# sdk/foundation/web — no hand-rolled JSON/error response writing anywhere in a feature's
# sealed interior (production code; tests exempt). A legitimate future hit
# (e.g. json.NewEncoder into a buffer or an SSE stream) gets a named per-line
# exception HERE citing FS9 — never a regex weakening.
#
# Exclusion-style over ALL of features/ (the G2 idiom): one expression covering
# root, domain/, memstore/, storetest/, and internal/ — closing the root-file
# blind spot a feature's root package (e.g. authentication.go, authorization's
# exported RequirePermission builder) sat outside. stores/ and views/ are
# separate adapter modules, excluded to match G2.
guard-feature-transport-sdk-web:
	@echo "== guard: feature transports use sdk/foundation/web responders (FS9) =="
	@! grep -rn --include='*.go' --exclude='*_test.go' --exclude-dir=stores --exclude-dir=views -E 'json\.NewEncoder\(|http\.Error\(' features/ || { echo "ERROR (FS9): hand-rolled HTTP response writing in a feature core — use web.Respond* (features/README.md, FS9)"; exit 1; }

# G7 (constitution rule 6, events-v1 task-13; the plan called it "G5" but that
# slot was already taken by FS1): no feature imports a DIFFERENT feature — a
# feature declares a port and the host wires the peer (ARCHITECTURE.md rule 6).
# For each features/<x> we grep its whole subtree for feature imports and drop
# the self-imports (features/<x>/...); what remains is a features/<x> file
# reaching into some features/<y>, y != x. The stores/ subtree is excluded
# (separate adapter modules, per the task spec, matching G2's stores exclusion);
# views/ is NOT excluded — an intra-feature views->own-core import is a self-
# import (y == x) and is dropped by the filter, so it never false-positives,
# while a views adapter reaching a foreign feature is still caught.
guard-feature-no-cross-feature:
	@echo "== guard: no feature core imports a different feature (rule 6) =="
	@fail=0; for d in features/*/; do \
		x=$$(basename $$d); \
		hits=$$(grep -rn --include='*.go' --exclude-dir=stores -E '"github.com/gopernicus/gopernicus/features/[a-z0-9]+' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/features/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (rule 6): $$x reaches into a different feature core — declare a port and let the host wire the peer:"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail

# G8 (authorization-v1 Z5, Q3 ADD): store adapter modules never import a
# DIFFERENT feature — a store implements exactly its own feature's ports over
# one connector — covering the stores/ subtrees G7 deliberately excludes. The
# pattern carries one extra alternation (steward minor 6): store→examples/
# imports, which no other guard watches. Same shape as G7: drop self-imports
# (features/<x>/...), anything left is a foreign reach.
guard-store-no-foreign-feature:
	@echo "== guard: store modules never import a foreign feature or examples (rule 6, stores) =="
	@fail=0; for d in features/*/stores/; do \
		[ -d "$$d" ] || continue; \
		x=$$(basename $$(dirname $$d)); \
		hits=$$(grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(features/[a-z0-9]+|examples)' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/features/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (rule 6, stores): a $$x store module reaches into a foreign feature or an example host:"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail

# G9 (datastore-hardening P6, audit ruling 6): nothing outside the datastore
# connectors calls Underlying() — the escape hatch to the raw pool/DB is the
# service-locator workaround the scaffolded crud.Transactor seam exists to
# prevent. A legitimate future host/cmd hit gets a named per-line exception
# HERE citing audit ruling 6 — never a regex weakening (the G6 discipline).
guard-no-underlying:
	@echo "== guard: no Underlying() outside the datastore connectors (crud.Transactor seam) =="
	@! grep -rn --include='*.go' '\.Underlying()' --exclude-dir=integrations . || { echo "ERROR (P6/ruling 6): Underlying() called outside integrations/datastores — use the ports, or consume the crud.Transactor seam"; exit 1; }

# G10 (datastore-hardening P6, audit ruling 8): RowToStructByNameLax silently
# tolerates missing fields — the quiet-data-loss variant of pgx struct
# scanning. Strict RowToStructByName only, everywhere, no exceptions.
guard-no-lax-scan:
	@echo "== guard: no RowToStructByNameLax anywhere (strict scanning only) =="
	@! grep -rn --include='*.go' 'RowToStructByNameLax' . || { echo "ERROR (P6/ruling 8): RowToStructByNameLax found — strict RowToStructByName only"; exit 1; }

# G11 (workshop-v2-scaffolding W1, review-gate fold item 7): the scaffolding CLI
# is isolated in BOTH directions. (a) Nothing outside workshop/ imports it — the
# CLI EMITS hosts and is never their runtime dependency. (b) workshop/ imports no
# feature cores (features/) and no examples/ — it templates them, it never links
# them (a per-field/queries.sql pull is the v2b trigger, not a runtime import).
guard-workshop-boundary:
	@echo "== guard: workshop/ is isolated both directions (nothing imports it; it imports no feature/example) =="
	@! grep -rn --include='*.go' --exclude-dir=workshop '"github.com/gopernicus/gopernicus/workshop' . || { echo "ERROR (W1): a non-workshop module imports the scaffolding CLI — workshop/ emits hosts, it is never a runtime dependency"; exit 1; }
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(features|examples)' workshop/ || { echo "ERROR (W1): workshop/ imports a feature core or an example — the CLI templates them, it never links them"; exit 1; }

# G12 (sdk-layering, 2026-07-10): the intra-sdk import law. Kernel = the root
# package (cycle-enforced against every subpackage that imports it; the grep
# below is the primary enforcement for the rest). foundation/* may import the
# ROOT only — FLAT, no foundation->foundation edges. capabilities/* may import
# root + foundation — NEVER another capability. sdk/feature is the ONE
# sanctioned composer (unconstrained). Production code only: *_test.go is
# exempt (the G6 precedent) — the deliberate env round-trip tests
# (foundation/logging/logging_env_test.go, foundation/web/server_env_test.go)
# are WHY the exemption exists.
guard-sdk-layering:
	@echo "== guard: sdk layering (kernel <- foundation <- capabilities <- feature) =="
	@! grep -n --include='*.go' '"github.com/gopernicus/gopernicus/sdk/' sdk/*.go 2>/dev/null || { echo "ERROR (G12a): the kernel (root package sdk) imports a subpackage"; exit 1; }
	@fail=0; for d in sdk/foundation/*/; do \
		x=$$(basename $$d); \
		hits=$$(grep -rn --include='*.go' --exclude='*_test.go' -E '"github.com/gopernicus/gopernicus/sdk/(foundation|capabilities|feature)' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/sdk/foundation/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (G12b): foundation/$$x imports a sibling tier or upward — foundation imports the root only:"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail
	@fail=0; for d in sdk/capabilities/*/; do \
		x=$$(basename $$d); \
		hits=$$(grep -rn --include='*.go' --exclude='*_test.go' -E '"github.com/gopernicus/gopernicus/sdk/(capabilities|feature)' $$d \
			| grep -vE '"github.com/gopernicus/gopernicus/sdk/capabilities/'"$$x"'([\"/])' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (G12c): capabilities/$$x imports another capability or sdk/feature — cross-capability composition leaves sdk (integrations)"; echo "$$hits"; fail=1; fi; \
	done; exit $$fail

# G13 (sdk-layering, 2026-07-10, folded steward finding): integrations never
# import inward — no features/, examples/, or workshop/. Load-bearing now that
# COMPOSING integrations (zero external deps, e.g. notify/mailer) are
# legitimate: the import direction is what keeps "integration" meaning
# something. A legitimate future hit gets a named per-line exception HERE —
# never a regex weakening.
guard-integration-no-inward:
	@echo "== guard: integrations import no features/examples/workshop =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(features|examples|workshop)' integrations/ || { echo "ERROR (G13): an integration imports inward"; exit 1; }

# G14 (authv3-delivery-refactor AV3D-5.1): authentication owns NO bespoke durable
# delivery queue. The private deliveryjob domain, its `delivery_jobs` table, and
# its pgx/turso stores were removed — durable delivery is the generic jobs feature
# reached through a host-wired Config.DeliveryDispatcher; auth owns no delivery
# table. This tripwire fails if either bespoke marker returns. The snake_case
# `delivery_jobs` (SQL table) is case-sensitive so it never matches the legitimate
# jobs-mode names (DeliveryJobKind, DeliveryJobRuntime, DeliveryJobsAcknowledged),
# and the deliveryjob package clause/import path catches a renamed resurrection.
guard-auth-no-delivery-repo:
	@echo "== guard: authentication owns no bespoke delivery table/repository (AV3D-5.1) =="
	@! grep -rn 'delivery_jobs' features/authentication || { echo "ERROR (AV3D-5.1): a 'delivery_jobs' table returned to authentication — durable delivery is the generic jobs feature; auth owns no delivery table"; exit 1; }
	@! grep -rnE 'domain/deliveryjob|package deliveryjob' --include='*.go' features/authentication examples/auth-cms || { echo "ERROR (AV3D-5.1): the bespoke deliveryjob domain package returned — durable delivery is the generic jobs feature, reached via Config.DeliveryDispatcher"; exit 1; }

# G15 (authv3-delivery-refactor AV3D-2.4/5.1): no auth producer calls a provider
# on the request path. Every outbound message is admitted through the delivery
# dispatcher seam; the actual send (Router.Deliver, email.Sender.Send,
# notify.Notifier.Notify) is the off-request processor's job. A producer that
# called any send verb directly would leak a secret on the request path, defeating
# enumeration safety. The delivery/ package (where those verbs legitimately live)
# is excluded; the AST-precise companion is TestNoProducerBypassesDispatcherSeam
# (producer_seam_test.go) — this coarse grep keeps the boundary in `make guard`.
guard-auth-no-request-time-provider:
	@echo "== guard: no authentication producer calls a provider on the request path (AV3D-2.4) =="
	@hits=$$(grep -rnE '\.(Deliver|Send|Notify)\(' --include='*.go' --exclude='*_test.go' features/authentication/internal/logic | grep -v '/delivery/' || true); \
		if [ -n "$$hits" ]; then echo "ERROR (AV3D-2.4): a producer package calls a provider-send verb directly — outbound must go through the delivery dispatcher seam, never a request-time send:"; echo "$$hits"; exit 1; fi

# G16 (authorizationv3 AZ3-5.3): authorization owns NO authorization-specific
# jobs/delivery table or repository. The v3 correctness kernel emits no effects
# (00-overview.md standing invariant: "Production/example wiring never relies on
# an authorization-specific jobs queue"); a later effects packet must consume the
# generic jobs feature + a same-transaction events outbox, never an
# authorization-owned queue. This tripwire — the guard-auth-no-delivery-repo twin
# pointed at features/authorization migrations + repositories — fails if a
# delivery/jobs table or a bespoke deliveryjob domain package appears in the
# authorization feature or its stores. The snake_case table tokens are
# case-sensitive so they never match a legitimate camelCase Go identifier.
guard-authorization-no-delivery-repo:
	@echo "== guard: authorization owns no bespoke jobs/delivery table/repository (AZ3-5.3) =="
	@! grep -rnE 'delivery_jobs|fenced_job_queue|job_queue|job_schedules' features/authorization || { echo "ERROR (AZ3-5.3): an authorization-specific jobs/delivery table returned to authorization — the v3 kernel emits no effects; durable delivery would be the generic jobs feature reached via a same-transaction events outbox, never an authorization-owned queue"; exit 1; }
	@! grep -rnE 'domain/deliveryjob|package deliveryjob' --include='*.go' features/authorization || { echo "ERROR (AZ3-5.3): a bespoke deliveryjob domain package appeared in authorization — the v3 kernel ships no effects/delivery domain"; exit 1; }

# CI-style gate: templ generation must be a no-op (no drift), then per-module
# vet/build/test across all MODULES, then the four layering guards. Drift
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
	else \
		before=$$(find . -name '*_templ.go' -not -path './.git/*' -exec shasum {} \; | sort); \
		$(MAKE) generate >/dev/null; \
		after=$$(find . -name '*_templ.go' -not -path './.git/*' -exec shasum {} \; | sort); \
		if [ "$$before" != "$$after" ]; then echo "ERROR: templ generation drift"; exit 1; fi; \
	fi
	@$(MAKE) warm-scaffold-cache
	@for m in $(MODULES); do echo "== $$m =="; (cd $$m && go vet ./... && go build ./... && go test ./...) || exit 1; done
	@echo "== integration-tag vet (compile-only, no DB) =="
	@for m in $(filter %/turso,$(STORE_MODULES)); do echo "== vet -tags=integration $$m =="; (cd $$m && go vet -tags=integration ./...) || exit 1; done
	@$(MAKE) guard
	@echo "all checks passed"
