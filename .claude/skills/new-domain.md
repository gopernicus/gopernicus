---
name: new-domain
description: Interactive workflow to design and build a new domain — tables, migrations, manifest, repos, and wiring
---

# New Domain Workflow

You are guiding the user through creating a new domain in their Gopernicus project. This is a multi-step process. Move through each phase one at a time, asking questions before proceeding. Do not rush ahead — the user's answers at each step inform the next.

## Phase 1: Understand the Domain

Start by asking:

- What is this domain called? (e.g., "billing", "notifications", "inventory")
- What entities (tables) will it contain? Just the names for now — we will design each one.
- Does this domain relate to an existing domain? (e.g., do these entities have foreign keys to tenants, users, or other existing tables?)

Once you understand the scope, confirm back: "So we are building a [domain] domain with [entity1], [entity2], ... — does that sound right?"

## Phase 2: Design Tables

For each entity the user listed, work through the table design one at a time:

- Ask what columns this table needs. Suggest sensible defaults:
  - Primary key column (e.g., project_id VARCHAR NOT NULL)
  - Foreign keys to parent tables (e.g., tenant_id if tenant-scoped)
  - record_state VARCHAR NOT NULL DEFAULT 'active' (if soft-delete is needed)
  - created_at and updated_at timestamps
- Ask about constraints: unique columns, indexes, check constraints
- Ask about any enum-like columns and what values they should accept
- Write the CREATE TABLE SQL and show it to the user for review before proceeding to the next entity

Do NOT create the migration file yet — gather all table designs first.

## Phase 3: Create Migration

Once all tables are designed and approved:

1. Run: `gopernicus db create add_<domain>`
2. Write all the CREATE TABLE statements into the generated migration file
3. Show the user the complete migration and confirm before applying
4. Run: `gopernicus db migrate`
5. Run: `gopernicus db reflect`
6. Verify the new tables appear in workshop/migrations/primary/_public.json

If the migration fails, help the user diagnose and fix the SQL.

## Phase 4: Update the Manifest

Open gopernicus.yml and add the new domain with its tables:

```yaml
databases:
  primary:
    domains:
      <domain>: [table1, table2, ...]
```

Show the user the change and confirm. The domain name determines the directory structure: core/repositories/<domain>/<entity>/.

## Phase 5: Scaffold Repositories

For each entity in the domain:

1. Run: `gopernicus new repo <domain>/<entity>`
2. Review the generated queries.sql — ask the user:
   - Are the CRUD operations correct?
   - Do you need any custom queries? (e.g., GetByEmail, ListActive, search on specific columns)
   - Should any columns be excluded from create/update inputs?
   - Are the filter conditions right?
3. Review the generated bridge.yml — ask the user:
   - Are the routes correct? (method, path, nesting)
   - What middleware should each route have? (authenticate, authorize, rate_limit)
   - Do you need authorization? If so, transition to the add-auth skill or ask about relations/permissions here.
4. Help the user make any customizations to queries.sql and bridge.yml before generation

## Phase 6: Generate Code

1. Run: `gopernicus generate <domain>`
2. Run: `go build ./...` to check for compile errors
3. If there are errors, diagnose them — common causes:
   - Missing or mismatched column types in queries.sql annotations
   - bridge.yml referencing a query that does not exist
   - Import path issues

## Phase 7: Wire the Domain

If this is a new domain (not adding entities to an existing one), the domain composite needs to be registered in the server setup.

1. Read app/server/config/server.go to understand the current wiring
2. Show the user what needs to be added — constructing the composite bridge and mounting its routes
3. Help write the wiring code

## Phase 8: Verify

1. Run: `go build ./...` — confirm everything compiles
2. Run: `go test -tags integration ./core/repositories/<domain>/...` if integration tests are available
3. Summarize what was created: migration, entities, routes, and any next steps (e.g., "you may want to add authorization" or "consider adding custom queries for X")

## Reference

See workshop/documentation/gopernicus/guides/adding-new-entity.md for the entity guide.
