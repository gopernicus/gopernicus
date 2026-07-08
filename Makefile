# gopernicus — framework monorepo (sdk + integrations + features + examples)
#
# Multi-module workspace (go.work), 30 modules. templ is pinned via the `tool`
# directive in features/cms/views/templ/go.mod (where the .templ sources live),
# so `go tool templ` is reproducible.

MODULES = sdk integrations/cryptids/bcrypt integrations/cryptids/golang-jwt integrations/datastores/pgxdb integrations/datastores/turso integrations/email/sendgrid integrations/filestorage/gcs integrations/filestorage/s3 integrations/kvstores/goredis integrations/oauth/github integrations/oauth/google integrations/scheduling/robfig-cron integrations/tracing/otel features/authentication features/authentication/stores/pgx features/authentication/stores/turso features/cms features/cms/stores/pgx features/cms/stores/turso features/cms/views/templ features/events features/events/stores/pgx features/events/stores/turso features/jobs features/jobs/stores/pgx features/jobs/stores/turso examples/auth-cms examples/cms examples/jobs-minimal examples/minimal

# STORE_MODULES carry env-gated live conformance suites (storetest against a real
# database). `make check`/`make test` run them hermetically (loud skips); `make
# test-stores` runs them EXPECTING the datastore env vars set.
STORE_MODULES = features/cms/stores/pgx features/cms/stores/turso features/authentication/stores/pgx features/authentication/stores/turso features/jobs/stores/pgx features/jobs/stores/turso features/events/stores/pgx features/events/stores/turso

.PHONY: generate build vet test test-stores run migrate check tidy guard \
	guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-feature-core-sdk-only guard-feature-transport-sdk-web guard-feature-no-cross-feature

# Regenerate *_templ.go from .templ sources (they live in features/cms/views/templ).
generate:
	cd features/cms/views/templ && go tool templ generate

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
# `make guard` runs all seven.
guard: guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path \
	guard-feature-core-sdk-only guard-feature-transport-sdk-web guard-feature-no-cross-feature

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
	@fail=0; for f in features/authentication features/cms features/events features/jobs; do \
		extras=$$(awk '/^require \(/{inblk=1; next} inblk && /^\)/{inblk=0; next} inblk && !/\/\/ indirect/{print $$1} /^require [^(]/{print $$2}' $$f/go.mod \
			| grep -v '^github.com/gopernicus/gopernicus/sdk$$' || true); \
		tools=$$(grep -E '^tool ' $$f/go.mod | awk '{print $$2}' || true); \
		bad=$$(printf '%s\n%s\n' "$$extras" "$$tools" | grep -v '^$$' || true); \
		if [ -n "$$bad" ]; then echo "ERROR (FS1): $$f/go.mod requires more than sdk:"; echo "$$bad"; fail=1; fi; \
	done; exit $$fail

# G6 (FS9, feature-standard 2026-07-07): feature transports respond via
# sdk/web — no hand-rolled JSON/error response writing anywhere in a feature's
# sealed interior (production code; tests exempt). A legitimate future hit
# (e.g. json.NewEncoder into a buffer or an SSE stream) gets a named per-line
# exception HERE citing FS9 — never a regex weakening.
guard-feature-transport-sdk-web:
	@echo "== guard: feature transports use sdk/web responders (FS9) =="
	@! grep -rn --include='*.go' --exclude='*_test.go' -E 'json\.NewEncoder\(|http\.Error\(' features/*/internal/ || { echo "ERROR (FS9): hand-rolled HTTP response writing in a feature core — use web.Respond* (features/README.md, FS9)"; exit 1; }

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

# CI-style gate: templ generation must be a no-op (no drift), then per-module
# vet/build/test across all MODULES, then the four layering guards. Drift
# is checked via `git diff` when this tree is a git repo; this repo IS a git
# repo (as of phase 2), so that branch runs. The before/after checksum branch
# remains as a fallback for gitless checkouts of *_templ.go.
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
	@for m in $(MODULES); do echo "== $$m =="; (cd $$m && go vet ./... && go build ./... && go test ./...) || exit 1; done
	@echo "== integration-tag vet (compile-only, no DB) =="
	@for m in $(filter %/turso,$(STORE_MODULES)); do echo "== vet -tags=integration $$m =="; (cd $$m && go vet -tags=integration ./...) || exit 1; done
	@$(MAKE) guard
	@echo "all checks passed"
