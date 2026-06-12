package generators

// compose-prod deploy profile templates: the "$10 VPS" story. One compose
// file runs app + postgres + redis + caddy (automatic TLS); deploy.sh
// builds, migrates (in a one-off golang container — the VPS needs no Go
// toolchain), and rolls the app; a systemd unit keeps the stack up across
// reboots; backup.sh + cron rotate pg dumps.

// deployComposeProdComposeTemplate produces
// workshop/deploy/compose-prod/compose.prod.yml.
const deployComposeProdComposeTemplate = `# Production compose stack for {{.ProjectName}}.
# Interpolated variables (POSTGRES_PASSWORD, SITE_ADDRESS) come from the
# environment — deploy.sh sources .env.prod at the repo root.
name: {{.ProjectName}}-prod

services:
  app:
    build:
      context: ../../..
      dockerfile: workshop/docker/dockerfile.{{.ProjectName}}
      args:
        BUILD_REF: ${BUILD_REF:-dev}
        BUILD_DATE: ${BUILD_DATE:-unknown}
    restart: unless-stopped
    env_file:
      - ../../../.env.prod
    # Published on loopback only — public traffic enters through caddy.
    ports:
      - "127.0.0.1:3000:3000"
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_started

  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: {{.ProjectName}}
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env.prod}
      PGDATA: /data/postgres
    volumes:
      - postgres-data:/data/postgres
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5
      start_period: 10s

  # Drop this service (and the app depends_on entry) if the app was
  # scaffolded without redis.
  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: redis-server --appendonly yes
    volumes:
      - redis-data:/data

  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    environment:
      SITE_ADDRESS: ${SITE_ADDRESS:?set SITE_ADDRESS in .env.prod (your domain, or :80 for plain HTTP)}
    volumes:
      - ./caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config
    depends_on:
      - app

  # One-off migration runner (profile-gated: never starts with 'up').
  # Runs the project's pinned generator tool, so the VPS needs no Go
  # toolchain — deploy.sh invokes it as a release step, never at boot.
  migrate:
    image: golang:1.26
    profiles: ["deploy"]
    working_dir: /src
    environment:
      {{.AppNameUpper}}_DB_DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/{{.ProjectName}}?sslmode=disable
      GOFLAGS: -mod=mod
    volumes:
      - ../../..:/src
      - go-mod-cache:/go/pkg/mod
    command: go tool gopernicus db migrate
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  postgres-data:
  redis-data:
  caddy-data:
  caddy-config:
  go-mod-cache:
`

// deployComposeProdCaddyfileTemplate produces
// workshop/deploy/compose-prod/caddy/Caddyfile.
const deployComposeProdCaddyfileTemplate = `# Caddy fronts the app with automatic TLS. SITE_ADDRESS is the served
# address from .env.prod: a domain (yourapp.example.com) gets a Let's
# Encrypt certificate automatically; ":80" serves plain HTTP for
# local smoke tests (port 80 is the published HTTP port).
{$SITE_ADDRESS}

reverse_proxy app:3000
`

// deployComposeProdDeployShTemplate produces
// workshop/deploy/compose-prod/deploy.sh.
const deployComposeProdDeployShTemplate = `#!/usr/bin/env bash
# Deploy {{.ProjectName}} on this host: build -> migrate -> roll the app.
# Run from anywhere; operates on the repo this script lives in.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
COMPOSE="docker compose -f $ROOT/workshop/deploy/compose-prod/compose.prod.yml"

cd "$ROOT"

if [[ ! -f .env.prod ]]; then
    echo "missing $ROOT/.env.prod — copy .env.example, set production values" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1091
source .env.prod
set +a

BUILD_REF="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
export BUILD_REF BUILD_DATE

echo "==> building app image (${BUILD_REF})"
$COMPOSE build app

echo "==> starting database"
$COMPOSE up -d postgres

echo "==> running migrations (release step — a failure stops the deploy)"
$COMPOSE run --rm migrate

echo "==> rolling the stack"
$COMPOSE up -d

echo "==> waiting for readiness"
for _ in $(seq 1 30); do
    if curl -fsS --max-time 2 http://127.0.0.1:3000/readyz >/dev/null 2>&1; then
        echo "==> deployed ${BUILD_REF}: $(curl -fsS --max-time 2 http://127.0.0.1:3000/healthz)"
        exit 0
    fi
    sleep 2
done
echo "app never became ready — check: $COMPOSE logs app" >&2
exit 1
`

// deployComposeProdBackupShTemplate produces
// workshop/deploy/compose-prod/backup.sh.
const deployComposeProdBackupShTemplate = `#!/usr/bin/env bash
# Nightly pg_dump with 14-day rotation. Wire it to cron (see README):
#   0 3 * * * /path/to/repo/workshop/deploy/compose-prod/backup.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
COMPOSE="docker compose -f $ROOT/workshop/deploy/compose-prod/compose.prod.yml"
BACKUP_DIR="${BACKUP_DIR:-$ROOT/backups}"
KEEP_DAYS="${KEEP_DAYS:-14}"

# Compose interpolates POSTGRES_PASSWORD/SITE_ADDRESS even for exec.
set -a
# shellcheck disable=SC1091
source "$ROOT/.env.prod"
set +a

mkdir -p "$BACKUP_DIR"
STAMP="$(date -u +%Y%m%d-%H%M%S)"

$COMPOSE exec -T postgres pg_dump -U postgres {{.ProjectName}} | gzip > "$BACKUP_DIR/{{.ProjectName}}-$STAMP.sql.gz"
find "$BACKUP_DIR" -name '{{.ProjectName}}-*.sql.gz' -mtime "+$KEEP_DAYS" -delete

echo "backup written: $BACKUP_DIR/{{.ProjectName}}-$STAMP.sql.gz"
`

// deployComposeProdSystemdTemplate produces
// workshop/deploy/compose-prod/systemd/<app>-compose.service.
const deployComposeProdSystemdTemplate = `# Keeps the {{.ProjectName}} compose stack up across reboots.
# Install (see README): copy to /etc/systemd/system/, fix WorkingDirectory,
# then: systemctl enable --now {{.ProjectName}}-compose
[Unit]
Description={{.ProjectName}} production compose stack
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=true
# Set these to wherever the repo is checked out on the host. The env file
# provides the compose interpolation vars (POSTGRES_PASSWORD, SITE_ADDRESS).
WorkingDirectory=/opt/{{.ProjectName}}
EnvironmentFile=/opt/{{.ProjectName}}/.env.prod
ExecStart=/usr/bin/docker compose -f workshop/deploy/compose-prod/compose.prod.yml up -d
ExecStop=/usr/bin/docker compose -f workshop/deploy/compose-prod/compose.prod.yml down

[Install]
WantedBy=multi-user.target
`

// deployComposeProdReadmeTemplate produces
// workshop/deploy/compose-prod/README.md.
const deployComposeProdReadmeTemplate = `# Deploying {{.ProjectName}} with compose on a single host

The "$10 VPS" profile: one compose file runs the app, postgres, redis,
and caddy (automatic TLS). No registry, no orchestrator — the host
builds from the checked-out repo. Requirements on the host: docker (with
compose), git, curl. No Go toolchain — migrations run in a one-off
golang container.

## One-time setup

1. Check out the repo on the host (the systemd unit assumes
   ` + "`/opt/{{.ProjectName}}`" + `; adjust if elsewhere).
2. Create ` + "`.env.prod`" + ` at the repo root (gitignored — never commit it).
   Start from ` + "`.env.example`" + ` and set at least:

       POSTGRES_PASSWORD=<strong-password>
       SITE_ADDRESS=yourapp.example.com   # or :80 for plain HTTP
       {{.AppNameUpper}}_DB_DATABASE_URL=postgres://postgres:<strong-password>@postgres:5432/{{.ProjectName}}?sslmode=disable
       {{.AppNameUpper}}_REDIS_ADDR=redis:6379

   Hostnames are compose service names (` + "`postgres`" + `, ` + "`redis`" + `) — the app
   reaches them on the compose network.
3. Point your domain's DNS at the host; caddy provisions TLS on first
   request. (No domain yet? ` + "`SITE_ADDRESS=:80`" + ` serves plain HTTP.)
4. Install the systemd unit so the stack survives reboots:

       sudo cp workshop/deploy/compose-prod/systemd/{{.ProjectName}}-compose.service /etc/systemd/system/
       # edit WorkingDirectory if the repo is not at /opt/{{.ProjectName}}
       sudo systemctl enable --now {{.ProjectName}}-compose

5. Schedule backups (nightly pg_dump, 14-day rotation):

       crontab -e
       0 3 * * * /opt/{{.ProjectName}}/workshop/deploy/compose-prod/backup.sh

## Deploying

    git pull --ff-only
    ./workshop/deploy/compose-prod/deploy.sh

The script builds the image (stamped with the commit as ` + "`BUILD_REF`" + ` —
visible in startup logs and ` + "`/healthz`" + `), runs migrations as a **release
step** (a bad migration stops the deploy; the running app keeps serving),
rolls the stack, and waits for ` + "`/readyz`" + `.

## Probes & monitoring

- ` + "`/healthz`" + ` — liveness, dependency-free, reports the running build.
- ` + "`/readyz`" + ` — readiness, pings postgres.

Both are reachable on the host at ` + "`127.0.0.1:3000`" + ` (loopback only;
public traffic enters through caddy).

## Rollback

    git checkout <previous-good-sha>
    ./workshop/deploy/compose-prod/deploy.sh

Migrations are forward-only — restore from ` + "`backups/`" + ` for schema
rollback:

    gunzip -c backups/{{.ProjectName}}-<stamp>.sql.gz | \
      docker compose -f workshop/deploy/compose-prod/compose.prod.yml \
      exec -T postgres psql -U postgres {{.ProjectName}}
`
