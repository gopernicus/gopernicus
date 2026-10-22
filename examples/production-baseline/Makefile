# examples/production-baseline — the auditable reference host.
#
# The server never migrates: `migrate` applies the host-owned ledger
# (workshop/migrations/primary) pre-boot, and `run` applies it first. Both
# binaries read APP_RUNTIME_MODE from the process environment only (never
# .env), so these targets export the development posture. `guard` checks the
# host contract (H0–H14) and the production posture (P0–P8).

.PHONY: build vet test run migrate guard

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

run: migrate
	APP_RUNTIME_MODE=development go run ./cmd/server

migrate:
	APP_RUNTIME_MODE=development go run ./workshop/migrations

guard:
	go run ../../workshop/gopernicus guard --posture .
