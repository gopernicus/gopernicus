# gopernicus — framework monorepo (sdk + integrations + features + examples)
#
# Multi-module workspace (go.work), 26 modules. templ is pinned via the `tool`
# directive in features/cms/go.mod (where the .templ sources live), so
# `go tool templ` is reproducible.

MODULES = sdk integrations/cryptids/bcrypt integrations/cryptids/golang-jwt integrations/datastores/pgxdb integrations/datastores/turso integrations/email/sendgrid integrations/filestorage/gcs integrations/filestorage/s3 integrations/kvstores/goredis integrations/oauth/github integrations/oauth/google integrations/scheduling/robfig-cron integrations/tracing/otel features/auth features/auth/stores/pgx features/auth/stores/turso features/cms features/cms/stores/pgx features/cms/stores/turso features/jobs features/jobs/stores/pgx features/jobs/stores/turso examples/auth-cms examples/cms examples/jobs-minimal examples/minimal

# STORE_MODULES carry env-gated live conformance suites (storetest against a real
# database). `make check`/`make test` run them hermetically (loud skips); `make
# test-stores` runs them EXPECTING the datastore env vars set.
STORE_MODULES = features/cms/stores/pgx features/cms/stores/turso features/auth/stores/pgx features/auth/stores/turso features/jobs/stores/pgx features/jobs/stores/turso

.PHONY: generate build vet test test-stores run migrate check tidy guard \
	guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path

# Regenerate *_templ.go from .templ sources (templ sources live in features/cms).
generate:
	cd features/cms && go tool templ generate

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
	@echo "== features/auth/stores/pgx (live) =="
	@cd features/auth/stores/pgx && go test ./...
	@echo "== features/jobs/stores/pgx (live) =="
	@cd features/jobs/stores/pgx && go test ./...
	@echo "== features/cms/stores/turso (live, -tags=integration) =="
	@cd features/cms/stores/turso && go test -tags=integration ./...
	@echo "== features/auth/stores/turso (live, -tags=integration) =="
	@cd features/auth/stores/turso && go test -tags=integration ./...
	@echo "== features/jobs/stores/turso (live, -tags=integration) =="
	@cd features/jobs/stores/turso && go test -tags=integration ./...

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
# constitution (00-overview.md); every target must print nothing and exit 0 on
# a clean tree. `make guard` runs all four.
guard: guard-sdk-stdlib guard-feature-isolation guard-sdk-no-outward guard-no-legacy-path

# G1: sdk imports only the standard library (also enforced structurally by
# sdk/go.mod having no require block).
guard-sdk-stdlib:
	@echo "== guard: sdk imports only the standard library =="
	@! grep -rn --include='*.go' '"github.com/' sdk/ | grep -v '"github.com/gopernicus/gopernicus/sdk' || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }
	@! grep -rnE '"(cloud\.google\.com|golang\.org/x|gopkg\.in)/' --include='*.go' sdk/ || { echo "ERROR: sdk imports an external module — sdk is the stdlib kernel and must stay dependency-free"; exit 1; }

# G2: every feature core (features/*, excluding their own store adapter
# modules) never imports integrations, examples, or any feature's stores (A4:
# generalized from features/cms to all features/*).
guard-feature-isolation:
	@echo "== guard: features/* cores never import integrations/examples/their own stores =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(integrations|examples|features/[a-z0-9]+/stores)' features --exclude-dir=stores || { echo "ERROR: a features/* core imports an adapter layer"; exit 1; }

# G3: sdk never imports outward (features/integrations/examples).
guard-sdk-no-outward:
	@echo "== guard: sdk never imports features/integrations/examples =="
	@! grep -rn --include='*.go' -E '"github.com/gopernicus/gopernicus/(features|integrations|examples)' sdk/ || { echo "ERROR: sdk imports an outward layer"; exit 1; }

# G4: no references to the old local module prefix remain.
guard-no-legacy-path:
	@echo "== guard: no legacy gopernicus/ import =="
	@! grep -rn --include='*.go' -E '"gopernicus/' . || { echo "ERROR: legacy gopernicus import found"; exit 1; }

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
	@$(MAKE) guard
	@echo "all checks passed"
