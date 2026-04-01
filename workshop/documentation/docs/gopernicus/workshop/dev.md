---
sidebar_position: 3
title: Dev
---

# Workshop — Dev

`workshop/dev/` contains local development tooling — anything needed to run the application on a developer's machine that isn't part of the application itself.

## Getting Started

Bring up local services:

```bash
make dev-up
# or: docker compose -f workshop/dev/local-data-compose.yml up -d
```

Tear them down:

```bash
make dev-down
```

Data is persisted in `workshop/dev/data/` (gitignored) so volumes survive restarts.

## Services

| Service    | Port   | Purpose                              |
| ---------- | ------ | ------------------------------------ |
| PostgreSQL | `5432` | Primary database                     |
| Redis      | `6379` | Cache and session storage            |

Connect to the local database with:

```bash
make dev-psql
```

## Extending for Your App

`local-data-compose.yml` is the right place to add any additional services your app needs locally. This list is not exhaustive, but common additions include:

- **Firebase emulator** — local Firestore and Auth without hitting production Firebase
- **Meilisearch** — local search engine for apps using full-text search
- **MinIO** — S3-compatible object storage for apps that handle file uploads

Add each as a new service under the `services` key and wire it into the `{YOURAPPNAME}-network` network so it can communicate with the other containers.
