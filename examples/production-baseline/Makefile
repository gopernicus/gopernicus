# examples/production-baseline — the auditable reference host.
#
# The server never migrates: `migrate` applies the host-owned ledger
# (workshop/migrations/primary) pre-boot, and `run` applies it first. Every
# binary reads APP_RUNTIME_MODE from the process environment only (never
# .env), so these targets export the development posture. `guard` checks the
# host contract (H0–H14) and the production posture (P0–P9).

.PHONY: build vet test run migrate bootstrap guard

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

# bootstrap grants the global admin role to SUBJECT_ID, the local user id
# provisioned at that person's first provider login (README, "The first admin").
# Rerunning it is safe.
bootstrap:
	APP_RUNTIME_MODE=development go run ./cmd/bootstrap -subject-type user -subject-id "$(SUBJECT_ID)"

guard:
	go run ../../workshop/gopernicus guard --posture .
