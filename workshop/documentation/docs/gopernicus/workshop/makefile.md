---
sidebar_position: 6
title: Makefile
---

# Makefile

`gopernicus init` scaffolds a Makefile with targets for development, testing,
Docker, and infrastructure management. All targets use self-documenting comments
(`## description`) — run `make help` (or just `make`) to see them.

## Application

| Target | Description |
|---|---|
| `make dev` | Start the server with [air](https://github.com/air-verse/air) hot reload. Runs `make dev-up` first to ensure infrastructure is running. |
| `make run` | Start the server directly with `go run ./app/server`. No hot reload. |
| `make build` | Build the server binary to `bin/<project-name>`. |
| `make clean` | Remove `bin/` build artifacts. |
| `make fmt` | Format all Go source files (`go fmt ./...`). |
| `make tidy` | Run `go mod tidy && go mod verify`. |

## Docker Build

| Target | Description |
|---|---|
| `make build-docker` | Build the production Docker image from `workshop/docker/dockerfile.<project>`. Tags with both `<version>` and `latest`. |
| `make run-docker` | Run the API in a detached Docker container on port 3000, using `.env` for configuration. Stops and removes any existing container first. |
| `make stop-docker` | Stop and remove the Docker container. |
| `make logs-docker` | Tail the Docker container logs. |

The version is derived from `git describe --tags --always --dirty`, falling back
to `dev` if not in a git repository. Override with `make build-docker VERSION=v1.0.0`.

## Development Infrastructure

These targets manage the `workshop/dev/docker-compose.yml` stack (PostgreSQL,
and Redis if selected during init).

| Target | Description |
|---|---|
| `make dev-up` | Start PostgreSQL and Redis containers. Data persists across restarts via named Docker volumes. |
| `make dev-down` | Stop the infrastructure containers. Data is preserved. |
| `make dev-logs` | Tail logs from all dev services. |
| `make dev-ps` | Show status of dev services. |
| `make dev-reset` | Nuclear reset — stops containers, wipes all volumes (deletes database data), and restarts fresh. |
| `make dev-psql` | Open an interactive `psql` shell connected to the dev database. |

## Gopernicus

Thin wrappers over the pinned framework tool (`go tool gopernicus`), exposed as
the `GOPERNICUS` variable. Because the tool is pinned via the `tool` directive
in `go.mod`, these targets always run the generator that matches the framework
version the project links.

| Target | Description |
|---|---|
| `make generate` | Regenerate all domains, then `go build ./...` so codegen breakage surfaces immediately. |
| `make doctor` | Check project health and configuration. |
| `make db-migrate` | Apply pending migrations. |
| `make db-status` | Show migration status. |
| `make db-reflect` | Reflect the database schema into the migrations directory. |
| `make db-reset` | Wipe dev database volumes, restart (waiting for health checks), and re-apply migrations. |

Parameterized commands are deliberately not wrapped — make is clumsy with
arguments, so run them directly:

```bash
go tool gopernicus db create <name>           # create a migration
go tool gopernicus new repo <domain/entity>   # scaffold an entity
go tool gopernicus new case <name>            # scaffold a use case
```

## Tests

| Target | Description |
|---|---|
| `make test` | Run unit tests (`go test ./...`). |
| `make test-integration` | Run integration tests with `-tags=integration`. Requires a running database. |
| `make test-e2e` | Run end-to-end tests with `-tags=e2e` (`go test -tags=e2e ./...`). Requires a running server. |

### Typical test workflow

```bash
make dev-up                # start postgres + redis
make db-migrate            # apply migrations
make test-integration      # run integration tests

# For E2E tests:
make dev &                 # start server in background
make test-e2e              # run E2E tests against it
```

## Variables

The Makefile defines these variables at the top, all overridable:

| Variable | Default | Description |
|---|---|---|
| `BINARY` | `<project-name>` | Output binary name and Docker container name. |
| `COMPOSE` | `docker compose -f workshop/dev/docker-compose.yml` | Compose command for dev infrastructure. |
| `GOPERNICUS` | `go tool gopernicus` | The pinned framework tool. |
| `VERSION` | `git describe --tags` or `dev` | Version string for Docker image tags and build metadata. |
| `IMAGE` | `<project-name>` | Docker image name. |
| `IMAGE_TAG` | `<image>:<version>` | Full Docker image tag. |

## Customization

The Makefile is a bootstrap file — it's created once by `gopernicus init` and
never overwritten. Add your own targets freely. Common additions:

- `make lint` — run `golangci-lint`
- `make seed` — populate development data

## Related

- [Dev Infrastructure](dev.md) — docker-compose services and configuration
- [Docker](docker.md) — production Dockerfile details
- [Testing](testing.md) — test infrastructure and fixtures
