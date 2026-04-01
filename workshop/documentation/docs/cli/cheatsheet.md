---
sidebar_position: 0
title: CLI Cheat Sheet
---

# CLI Cheat Sheet

Quick reference for all gopernicus commands, when to run them, and how they
relate to each other.

## Command Map

```
gopernicus init <name>            Create a new project
       │
       ▼
gopernicus doctor                 Verify project health
       │
       ▼
make dev-up                       Start postgres + redis
       │
       ▼
gopernicus db migrate             Apply SQL migrations
       │
       ▼
gopernicus db reflect             Snapshot schema → _public.json
       │
       ▼
gopernicus boot repos             Scaffold all domain repos + bridges
       │   (or)
gopernicus new repo domain/entity Scaffold a single repo + bridge
       │
       ▼
gopernicus generate               Generate Go code from queries.sql + bridge.yml
       │
       ▼
make dev                          Run server with hot reload
```

## All Commands

### Project Setup

| Command | When |
|---|---|
| `gopernicus init <name>` | Once, to create a new project. |
| `gopernicus doctor` | After init, after upgrades, or when something seems wrong. |

### Database

| Command | When |
|---|---|
| `gopernicus db create <name>` | When you need a new migration file. |
| `gopernicus db migrate` | After writing or pulling new migration SQL. |
| `gopernicus db reflect` | After every `db migrate` — keeps the schema snapshot current. |
| `gopernicus db status` | To check which migrations are applied, pending, or tampered. |

### Scaffolding

| Command | When |
|---|---|
| `gopernicus new repo <domain/entity>` | To add a single entity with CRUD. |
| `gopernicus new case <name>` | To add business logic beyond CRUD. |
| `gopernicus new adapter <type> <name>` | To add a custom infrastructure adapter. |
| `gopernicus boot repos [domain]` | To scaffold all repos for a domain (or all domains) at once. |

### Code Generation

| Command | When |
|---|---|
| `gopernicus generate` | After any change to `queries.sql`, `bridge.yml`, or reflected schema. |
| `gopernicus generate <domain>` | To regenerate only one domain (faster). |
| `gopernicus generate --dry-run` | To preview what would change without writing files. |
| `gopernicus generate --force-bootstrap` | To overwrite bootstrap files (destructive — discards customizations). |

## Common Workflows

### Add a new table

```bash
gopernicus db create add_widgets_table
# edit the migration SQL
gopernicus db migrate
gopernicus db reflect
# add table to gopernicus.yml domains
gopernicus new repo catalog/widgets
gopernicus generate
```

### Modify a table

```bash
gopernicus db create alter_widgets_add_color
# edit the migration SQL
gopernicus db migrate
gopernicus db reflect
gopernicus generate          # picks up new columns automatically
```

### Add a use case

```bash
gopernicus new case checkout
# implement core/cases/checkout/case.go
# add routes in bridge/cases/checkoutbridge/http.go
# wire in app/server/config/server.go
```

### Add a custom adapter

```bash
gopernicus new adapter cache redis
# implement infrastructure/cache/redis/redis.go
go test ./infrastructure/cache/redis/...
```

### Regenerate after pulling changes

```bash
gopernicus db migrate        # apply any new migrations
gopernicus db reflect        # re-snapshot
gopernicus generate          # regenerate
```

## Flag Quick Reference

| Flag | Commands | Description |
|---|---|---|
| `--db <name>` | db *, boot repos, new repo | Target database (default: `primary`). |
| `--db-url <url>` | db migrate, db reflect, db status | Override database URL directly. |
| `--table <name>` | new repo | Override table name lookup. |
| `--dry-run` | generate | Preview without writing. |
| `--verbose`, `-v` | generate | Detailed output. |
| `--force-bootstrap`, `-f` | generate | Overwrite bootstrap files. |
| `--module`, `-m` | init | Go module path. |
| `--framework-version` | init | Pin framework version. |
| `--no-interactive` | init | Skip TUI prompts. |
| `--features <list>` | init | Comma-separated feature list. |
