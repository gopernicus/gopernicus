package generators

// Deploy profile templates. These generalize real production pipelines:
// do-app transcribes segovia's tag-triggered GitHub workflow (ghcr build →
// migrate as a release step → DigitalOcean app_action), cloud-run
// transcribes bluemark-redirector's make-driven gcloud flow. Workflow and
// makefile templates use [[ ]] delimiters because their payloads contain
// `${{ }}` (GitHub Actions) and `$( )` (make) syntax that collides with
// the default {{ }}.

// deployDoAppWorkflowTemplate produces .github/workflows/deploy-<app>-do.yml.
const deployDoAppWorkflowTemplate = `name: Build and Deploy [[.ProjectName]] to DigitalOcean
on:
  push:
    tags:
      - "prod.[[.ProjectName]].*"

jobs:
  build-migrate-deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      # Deploys only ship from main: a tag on any other branch fails loud.
      - name: Verify tag is on main
        run: |
          git branch -r --contains "${{ github.sha }}" | grep -qE 'origin/main$' || \
            { echo "::error::Tag is not on the main branch"; exit 1; }

      - name: Log in to GitHub Container Registry
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push API Docker image
        uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          file: ./workshop/docker/dockerfile.[[.ProjectName]]
          build-args: |
            BUILD_REF=${{ github.sha }}
            BUILD_DATE=${{ github.event.repository.pushed_at }}
          tags: |
            ghcr.io/${{ github.repository }}-api:latest
            ghcr.io/${{ github.repository }}-api:${{ github.sha }}

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      # Migrations run as a RELEASE STEP, never at boot: a bad migration
      # fails the deploy here, before any new instance starts.
      - name: Run database migrations
        env:
          [[.AppNameUpper]]_DB_DATABASE_URL: ${{ secrets.PROD_[[.AppNameUpper]]_PG_URL }}
        run: go tool gopernicus db migrate

      - name: Deploy the API
        uses: digitalocean/app_action/deploy@v2
        with:
          token: ${{ secrets.DIGITAL_OCEAN_ACCESS_TOKEN }}
          app_name: prod-[[.ProjectName]]
`

// deployDoAppSpecTemplate produces workshop/deploy/do-app/app-spec.yaml —
// the reference DigitalOcean app spec the workflow deploys into.
const deployDoAppSpecTemplate = `# Reference DigitalOcean App Platform spec for {{.ProjectName}}.
# Create the app once (doctl apps create --spec app-spec.yaml, or via the
# control panel) — the deploy workflow then refreshes it by app_name.
# Replace <github-org>/<repo> with your repository.
name: prod-{{.ProjectName}}
services:
  - name: api
    image:
      registry_type: GHCR
      registry: <github-org>
      repository: <repo>-api
      tag: latest
    http_port: 3000
    instance_count: 1
    instance_size_slug: basic-xxs
    health_check:
      http_path: /healthz
    # Environment: set {{.AppNameUpper}}_* vars here or in the control
    # panel; mark secrets (database URL, JWT secret) as type SECRET.
    # envs:
    #   - key: {{.AppNameUpper}}_DB_DATABASE_URL
    #     type: SECRET
    #     value: postgres://...
`

// deployDoAppReadmeTemplate produces workshop/deploy/do-app/README.md.
const deployDoAppReadmeTemplate = `# Deploying {{.ProjectName}} to DigitalOcean App Platform

Tag-triggered pipeline: pushing a ` + "`prod.{{.ProjectName}}.*`" + ` tag on main
builds the image, runs migrations as a release step, and refreshes the DO
app. The workflow lives at
` + "`.github/workflows/deploy-{{.ProjectName}}-do.yml`" + `.

## One-time setup

1. **Create the app** from the reference spec (edit placeholders first):

       doctl apps create --spec workshop/deploy/do-app/app-spec.yaml

   Or create it in the control panel; the workflow targets it by
   ` + "`app_name: prod-{{.ProjectName}}`" + `.

2. **Repository secrets** (GitHub → Settings → Secrets and variables):
   - ` + "`DIGITAL_OCEAN_ACCESS_TOKEN`" + ` — DO API token with app deploy scope.
   - ` + "`PROD_{{.AppNameUpper}}_PG_URL`" + ` — production database URL the
     migrate step uses.

3. **App environment**: set ` + "`{{.AppNameUpper}}_*`" + ` env vars on the DO app
   (database URL, JWT secret, allowed frontends, …) — the container reads
   configuration from the environment only.

## Deploying

    git tag prod.{{.ProjectName}}.1 && git push origin prod.{{.ProjectName}}.1

The workflow refuses tags that are not on main. Each deploy: ghcr image
(stamped with ` + "`BUILD_REF`" + ` — visible in startup logs and ` + "`/healthz`" + `) →
migrations (` + "`go tool gopernicus db migrate`" + `, **never at boot** — a bad
migration fails the deploy before any new instance starts) → app refresh.

## Probes

- ` + "`/healthz`" + ` — liveness, dependency-free (configured in the app spec).
- ` + "`/readyz`" + ` — readiness, pings the database; point the platform's
  readiness/load-balancer check here if you separate the two.

## Rollback

Deploys are immutable images tagged by commit SHA. Roll back by deploying
a previous SHA from the DO control panel (Deployments → Rollback), or by
tagging the previous good commit (` + "`prod.{{.ProjectName}}.N+1`" + ` on the old
SHA). Migrations are forward-only — write down-safe migrations or restore
from backup for schema rollback.
`

// deployCloudRunMakefileTemplate produces
// workshop/deploy/cloud-run/makefile.cloud-run. Include it from the root
// Makefile; it reuses the root's BINARY/VERSION vars (with fallbacks).
const deployCloudRunMakefileTemplate = `# Cloud Run deploy targets for [[.ProjectName]].
# Include from the root Makefile:
#   include workshop/deploy/cloud-run/makefile.cloud-run
# Override any var via env or CLI: make cloud-deploy GCP_REGION=us-east1

BINARY     ?= [[.ProjectName]]
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GOPERNICUS ?= go tool gopernicus

GCP_PROJECT_ID    ?= <your-gcp-project>
GCP_REGION        ?= us-central1
ARTIFACT_REPO     ?= [[.ProjectName]]
CLOUD_RUN_SERVICE ?= $(BINARY)
# Strip defends against trailing whitespace / accidental inline comments on
# the var lines above — Make would otherwise include them in the value.
GCP_PROJECT_ID    := $(strip $(GCP_PROJECT_ID))
GCP_REGION        := $(strip $(GCP_REGION))
ARTIFACT_REPO     := $(strip $(ARTIFACT_REPO))
CLOUD_RUN_SERVICE := $(strip $(CLOUD_RUN_SERVICE))
RUNTIME_SA             := $(CLOUD_RUN_SERVICE)@$(GCP_PROJECT_ID).iam.gserviceaccount.com
CLOUD_RUN_IMAGE        := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(ARTIFACT_REPO)/$(CLOUD_RUN_SERVICE):$(VERSION)
CLOUD_RUN_IMAGE_LATEST := $(GCP_REGION)-docker.pkg.dev/$(GCP_PROJECT_ID)/$(ARTIFACT_REPO)/$(CLOUD_RUN_SERVICE):latest

.PHONY: cloud-bootstrap
cloud-bootstrap: ## One-time GCP setup: enable APIs, create Artifact Registry repo + runtime SA
	gcloud services enable \
		run.googleapis.com \
		artifactregistry.googleapis.com \
		iamcredentials.googleapis.com \
		--project=$(GCP_PROJECT_ID)
	-gcloud artifacts repositories create $(ARTIFACT_REPO) \
		--repository-format=docker \
		--location=$(GCP_REGION) \
		--project=$(GCP_PROJECT_ID)
	-gcloud iam service-accounts create $(CLOUD_RUN_SERVICE) \
		--display-name="[[.ProjectName]] runtime" \
		--project=$(GCP_PROJECT_ID)
	gcloud auth configure-docker $(GCP_REGION)-docker.pkg.dev --quiet

.PHONY: cloud-build
cloud-build: ## Build linux/amd64 image and push to Artifact Registry
	docker buildx build \
		--platform=linux/amd64 \
		-f workshop/docker/dockerfile.[[.ProjectName]] \
		-t $(CLOUD_RUN_IMAGE) \
		-t $(CLOUD_RUN_IMAGE_LATEST) \
		--build-arg BUILD_REF=$(VERSION) \
		--build-arg BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ") \
		--push \
		.

.PHONY: cloud-migrate
cloud-migrate: ## Run migrations against the production database (release step, never at boot)
	@test -n "$([[.AppNameUpper]]_DB_DATABASE_URL)" || \
		{ echo "set [[.AppNameUpper]]_DB_DATABASE_URL to the production database URL"; exit 2; }
	$(GOPERNICUS) db migrate

# Add the rest of your runtime env with more --set-env-vars/--set-secrets
# flags on cloud-deploy (the database URL belongs in Secret Manager via
# --set-secrets).
.PHONY: cloud-deploy
cloud-deploy: ## Deploy the pushed image to Cloud Run
	gcloud run deploy $(CLOUD_RUN_SERVICE) \
		--image=$(CLOUD_RUN_IMAGE) \
		--region=$(GCP_REGION) \
		--project=$(GCP_PROJECT_ID) \
		--platform=managed \
		--service-account=$(RUNTIME_SA) \
		--allow-unauthenticated \
		--set-env-vars=[[.AppNameUpper]]_PORT=8080

.PHONY: cloud-ship
cloud-ship: cloud-build cloud-migrate cloud-deploy ## Build, push, migrate, and deploy in one shot

.PHONY: cloud-url
cloud-url: ## Print the deployed Cloud Run service URL
	@gcloud run services describe $(CLOUD_RUN_SERVICE) \
		--region=$(GCP_REGION) --project=$(GCP_PROJECT_ID) \
		--format='value(status.url)'

.PHONY: cloud-logs
cloud-logs: ## Tail Cloud Run service logs
	gcloud beta run services logs tail $(CLOUD_RUN_SERVICE) \
		--region=$(GCP_REGION) --project=$(GCP_PROJECT_ID)
`

// deployCloudRunReadmeTemplate produces workshop/deploy/cloud-run/README.md.
const deployCloudRunReadmeTemplate = `# Deploying {{.ProjectName}} to Google Cloud Run

Make-driven pipeline. Wire it up by adding one line to the root Makefile:

    include workshop/deploy/cloud-run/makefile.cloud-run

## One-time setup

1. Edit ` + "`GCP_PROJECT_ID`" + ` (and region/repo if needed) in
   ` + "`makefile.cloud-run`" + `, or pass them per-invocation.
2. Bootstrap GCP (enables APIs, creates the Artifact Registry repo and
   runtime service account, configures docker auth):

       make cloud-bootstrap

3. Grant the runtime service account access to your database (e.g.
   ` + "`roles/cloudsql.client`" + ` plus a Cloud SQL connection, or network access
   to wherever Postgres lives) and put the database URL in Secret Manager.

## Deploying

    {{.AppNameUpper}}_DB_DATABASE_URL=<prod-url> make cloud-ship

` + "`cloud-ship`" + ` = ` + "`cloud-build`" + ` (linux/amd64 image stamped with
` + "`BUILD_REF`" + ` — visible in startup logs and ` + "`/healthz`" + `) →
` + "`cloud-migrate`" + ` (**release step, never at boot** — a bad migration stops
the deploy before any new revision starts) → ` + "`cloud-deploy`" + `.

The deploy sets ` + "`{{.AppNameUpper}}_PORT=8080`" + ` to match Cloud Run's default
container port. Add the rest of your runtime configuration with
` + "`--set-env-vars`" + `/` + "`--set-secrets`" + ` flags on ` + "`cloud-deploy`" + `.

## Probes

- ` + "`/healthz`" + ` — liveness, dependency-free.
- ` + "`/readyz`" + ` — readiness, pings the database. Configure it as the Cloud
  Run startup probe (service YAML ` + "`startupProbe.httpGet.path: /readyz`" + `)
  so revisions only receive traffic once the database is reachable.

## Rollback

Cloud Run keeps every revision:

    gcloud run services update-traffic $(CLOUD_RUN_SERVICE) --to-revisions=<previous-revision>=100

Migrations are forward-only — write down-safe migrations or restore from
backup for schema rollback.
`
