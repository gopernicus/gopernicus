
SHELL_PATH := /bin/ash
SHELL := $(if $(wildcard $(SHELL_PATH)),/bin/ash,/bin/bash)

GOLANG := golang:1.25

NAMESPACE := gopernicus
IMAGE_NAME := localhost/gopernicus

# Server version info
SERVER_VERSION := 0.0.1
SERVER_NAME := gopernicus
SERVER_IMAGE := $(IMAGE_NAME)/$(SERVER_NAME):$(SERVER_VERSION)
SERVER_IMAGE_LATEST := $(IMAGE_NAME)/$(SERVER_NAME):latest

# Run API with hot reload (uses .air.toml config)
watch: dev-data-up
	air

# Run API without hot reload (manual restart)
dev-api: dev-data-up
	go run ./app/server

# ==============================================================================
# DATA
dev-data-up:
	docker compose -p $(NAMESPACE) -f workshop/dev/local-data-compose.yml up -d

dev-data-down:
	docker compose -p $(NAMESPACE) -f workshop/dev/local-data-compose.yml down

dev-psql:
	PGPASSWORD=admin psql -h localhost -p 5432 -U db_user -d $(NAMESPACE)_db

# Open Jaeger UI (traces) - Jaeger starts with dev-data-up
dev-jaeger:
	@echo "Opening Jaeger UI at http://localhost:16686"
	@open http://localhost:16686 2>/dev/null || xdg-open http://localhost:16686 2>/dev/null || echo "Visit: http://localhost:16686"

# ==============================================================================
# TESTS
#
# Build tags control which tests run:
#   - Unit tests: no build tag, no Docker needed (fast)
#   - Integration tests: -tags=integration, needs Docker (testcontainers)
#   - E2E tests: -tags=e2e, needs Docker (testcontainers)
#   - Penetration tests: -tags=penetration, needs Docker (slow)
#
# Examples:
#   make test              # Unit tests only (fast, no Docker)
#   make test-integration  # Integration tests (pgxstore, cache)
#   make test-e2e          # E2E tests (full HTTP API stack)
#   make test-penetration  # Penetration tests (security, slow)
#   make test-all          # Everything

# Unit tests only (fast, no Docker)
test:
	go test ./...

# Integration tests (pgxstore, cache — requires Docker for testcontainers)
test-integration:
	@echo "Running integration tests (requires Docker)..."
	go test -tags=integration -v -timeout 10m ./...

# E2E tests (full HTTP API — requires Docker for testcontainers)
test-e2e:
	@echo "Running E2E tests (requires Docker)..."
	go test -tags=e2e -v -timeout 10m ./testing/e2e/...

# Penetration tests (security — requires Docker, slow)
test-penetration:
	@echo "Running penetration tests (requires Docker)..."
	go test -tags=penetration -v -timeout 10m ./testing/penetration/...

# All tests (unit + integration + E2E + penetration)
test-all:
	@echo "Running full test suite..."
	go test -tags=integration,e2e,penetration -v -timeout 15m ./...

# ==============================================================================
# TESTS WITH COVERAGE

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-integration-coverage:
	go test -tags=integration -coverprofile=coverage-integration.out -coverpkg=./... -timeout 10m ./...
	go tool cover -html=coverage-integration.out -o coverage-integration.html
	@echo "Coverage report: coverage-integration.html"

test-e2e-coverage:
	go test -tags=e2e -coverprofile=coverage-e2e.out -coverpkg=./... -timeout 10m ./testing/e2e/...
	go tool cover -html=coverage-e2e.out -o coverage-e2e.html
	@echo "Coverage report: coverage-e2e.html"

test-all-coverage:
	go test -tags=integration,e2e,penetration -coverprofile=coverage-all.out -coverpkg=./... -timeout 15m ./...
	go tool cover -html=coverage-all.out -o coverage-all.html
	@echo "Coverage report: coverage-all.html"

# ==============================================================================
# FOCUSED TESTING

# Run a single test by package and name
# Usage: make test-one PKG=./core/repositories/auth/users/userspgx TEST=TestGeneratedUserStore_Create
test-one:
	go test -v $(PKG) -run $(TEST)

test-one-integration:
	go test -tags=integration -v -timeout 5m $(PKG) -run $(TEST)

# Run unit security tests (TestSecurity_* prefix, included in make test)
test-security:
	@echo "Running unit security tests..."
	go test -v -run TestSecurity ./...

# Run all security tests (unit + penetration)
test-security-all:
	@echo "Running all security tests..."
	go test -v -run TestSecurity ./...
	go test -tags=penetration -v -timeout 10m ./testing/penetration/...

# ==============================================================================
# TEST HELP

test-help:
	@echo "Test Commands:"
	@echo ""
	@echo "Main Commands:"
	@echo "  make test                    Unit tests only (fast, no Docker)"
	@echo "  make test-integration        Integration tests (needs Docker)"
	@echo "  make test-e2e                E2E tests (needs Docker)"
	@echo "  make test-penetration        Penetration tests (needs Docker)"
	@echo "  make test-all                Everything (unit + integration + E2E + penetration)"
	@echo ""
	@echo "With Coverage:"
	@echo "  make test-coverage"
	@echo "  make test-integration-coverage"
	@echo "  make test-e2e-coverage"
	@echo "  make test-all-coverage"
	@echo ""
	@echo "Focused Testing:"
	@echo "  make test-one PKG=./path/to/pkg TEST=TestName"
	@echo "  make test-one-integration PKG=./path/to/pkg TEST=TestName"
	@echo ""
	@echo "Security Testing:"
	@echo "  make test-security           Unit security tests (TestSecurity_*)"
	@echo "  make test-penetration        Penetration tests (needs Docker)"
	@echo "  make test-security-all       All security tests"

