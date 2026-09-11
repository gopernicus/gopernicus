# Listing documents with authorization

The host exposes one operation: `Documents.ListVisible(ctx, principal, query)`.
Its [domain port](../../../logic/domains/documents/documents.go) owns the query
and result. The [HTTP handler](../../../inbound/domains/documents/http.go) parses
tenant, search, name sort, limit and cursor. This outbound adapter combines those
business rules with the authorization pocket. The SDK gains no query planner.

[Server composition](../../../../cmd/server/documents.go) mounts
`GET /demo/tenants/{tenant}/documents` behind live authentication, using memory
storage and candidate filtering. For example:

```text
/demo/tenants/demo/documents?q=a&sort=-name&limit=2
```

This proof host seeds access for its synthetic `demo-owner`, as it does for its
project/admin examples. Registration does not grant document access. A host
assigns access through its chosen trusted or guarded application workflow;
there is no public bootstrap or impersonation route. The endpoint accepts only
normal business query parameters; strategy selection happens at construction.

## Choose the adapter

| Arrangement | Construction | Behavior |
| --- | --- | --- |
| Different stores, or same SQL database using separate queries | `NewListing(reader, authorizer, codec, CompleteSet, 0, bypass)` | Get a complete bounded `ResourceSet`, then apply it before tenant/search sorting and paging. |
| Different stores, or same SQL database using separate queries | `NewListing(reader, authorizer, codec, Candidates, batchSize, bypass)` | Read business candidates in order, then batch-check their document IDs. |
| Same PostgreSQL database with authorization in the business statement | `NewSQLListing(postgres, authorizationSchema, authorizer, codec, bypass)` | Apply a constrained permission `EXISTS` before business sorting and paging. |

All implement the same domain `Lister`. `bypass` is an optional host policy
function. A true result skips only the permission restriction; tenant, search,
input validation and cursor binding still apply. Policy/storage errors propagate.

`CompleteSet` handles unrestricted, none and errors separately. It never uses
one ID page as the whole filter, sorts an incomplete permission set, or quietly
enumerates every ID page after overflow. The default 1,000-ID ceiling rejects
the maintained 1,005-grant fixture even for a requested business page of 50.
Candidate and SQL strategies page that fixture correctly; an explicitly sized
complete-set budget also returns the same name order.

`Candidates` uses `authorization.FilterPage` and returns its forward-only
`ScanLimitReached` signal. A short or empty page can have `HasMore: true`; resume
with `NextCursor` until `HasMore` is false. The cursor follows the last consumed
candidate, including denied rows, and an unused overfetched suffix is reread.
No candidate totals/facets or previous-page claims are exposed.

## PostgreSQL setup and selected policy

[postgres.go](postgres.go) contains the actual SQL and constructors.
[postgres_test.go](postgres_test.go) contains executable composition of the
connector, migrations, authorizer, domain service and real HTTP server.
The host owns the database lifecycle and applies migrations before boot:

1. Export/apply the authorization store's canonical migration stream in its
   authorization schema. Construct its relationship repository with that same
   `authzpgx.WithSchema` value.
2. Apply the host's [document stream](../../../../workshop/migrations/documents/primary/0001_documents.sql)
   with `pgxdb.WithSchema(documentSchema)`. The example uses separate schemas
   and separate ledgers in one database; migration call order is explicit.
3. Pass the same `*pgxdb.DB` to the document and authorization adapters.
   `NewSQLListing` receives the authorization schema explicitly. It cannot
   discover whether an unrelated authorizer uses another database.
4. Supply a shared cursor key, choose the adapter, call `documents.New(lister)`,
   and place its handler behind the host's identity middleware.

The SQL adapter accepts exactly this selected relationship permission:

```go
ResourceSchema{Name: "document", Def: ResourceTypeDef{
    Relations: map[string]RelationDef{
        "viewer": {AllowedSubjects: []SubjectTypeRef{{Type: "user"}}},
    },
    Permissions: map[string]PermissionRule{
        "view": AnyOf(Direct("viewer")),
    },
}}
```

These are `authorization` types/functions. Other resource types and permissions
may coexist. A userset, Through rule, extra OR branch, changed allowed subject,
or role-owned `document/view` fails SQL adapter construction. The implementation
does not silently omit unsupported policy branches. Concrete machine subjects
receive no rows. The `EXISTS` uses canonical `iam_relationships` with exact
concrete-user/viewer predicates, so grant rows cannot multiply document rows.
There is no general ReBAC-to-SQL compiler here.

The selected predicate has permission/data parity with ordinary checks. It does
not share the generic evaluator's graph-work ceilings: SQL can prove this
restricted direct grant without traversing an unrelated userset graph. Tests
compare results within their configured budgets and test unsupported models
separately. Real `EXPLAIN (ANALYZE, BUFFERS)` output records database work; page
size is only a returned-row bound.

## Ordering, cursor privacy and changing data

Documents persist a non-null `name_key`, computed with Go `strings.ToLower` on
every host write. Memory and SQL compare `(name_key, id)` by bytes; PostgreSQL
uses explicit `COLLATE "C"`. Both keys reverse for descending order, with an
exclusive `>`/`<` continuation. Duplicate names use ID as the tie breaker.
Search is a normalized literal substring, including `%` and `_` as ordinary
characters, and all SQL values are parameters.

`Document.Validate` bounds IDs/tenants to 128 bytes and names to 512 bytes,
rejecting invalid UTF-8, controls and empty values. Both supplied writers enforce
it. Hosts writing SQL elsewhere must preserve that validation and `name_key`;
unbounded sort values could generate an unusably large cursor.

[cursor.go](cursor.go) uses AES-GCM to encrypt the actual persisted sort key and
ID. This hides denied candidate values and rejects tampering. Associated data
binds principal, tenant, normalized search, ordering and the selected permission.
Page size can change. The accepted 4,096-byte token bound accommodates every
validated document, including JSON escaping. Use a stable secret across host
instances; the ephemeral memory demo generates one per boot, so its cursors
expire on restart. Key rotation requires restart-from-first-page handling.

This cursor is not a data snapshot. New pages reapply current permission and
business predicates; tests revoke a grant and move a document to another tenant
between requests and verify neither row returns. Insertions or changes to sort
keys may change a later page. Candidate checks are fresh for each source pull;
there is no cross-pull or cross-request permission cache. A resource/grant can
still change between separate reads. `HasMore` can reveal that candidates exist.

Same-database separate queries use the connector's ambient context through the
same connector instance. An ordinary PostgreSQL Read Committed transaction still
has per-statement snapshots; the connector does not currently select repeatable
read isolation. `EXISTS` has one statement snapshot. Neither covers later HTTP
requests. A cross-store or cross-request snapshot requires an explicit host
consistency design.

## Measurements and verification

The candidate test compares the new remaining-capacity default against explicit
`BatchSize: 20`, matching the former `2 * Limit` pull size for a ten-row page.
These are source calls and returned candidates, not physical scans or production
latency. All variants return the same ten documents in the same order.

| Access density | Remaining capacity: calls / candidates | Batch 20: calls / candidates |
| --- | ---: | ---: |
| Every document | 1 / 10 | 1 / 20 |
| Every second document | 5 / 19 | 1 / 20 |
| Every hundredth document | 282 / 901 | 46 / 920 |

The default avoids dense overfetch. A sparse endpoint should explicitly size
its batch or choose a different strategy; no automatic selector is implied.
The memory reader sorts its fixture in memory and is not a database performance
model. Production cost depends on indexes, permission density, query selectivity
and latency.

From this example module:

```sh
go test -race ./internal/outbound/domains/documents -count=1
AUTHORIZATION_LISTING_TEST_DSN='postgres://...' \
    go test -race ./internal/outbound/domains/documents -count=1 -v
```

The first command uses real local HTTP and memory; live PostgreSQL cases skip
explicitly without the dedicated variable. The second requires an explicitly
chosen scratch database. It creates/removes unique `listing_auth_*` and
`listing_docs_*` schemas, applies real migrations and checks all three strategies,
including separate authorization storage, Unicode/name ordering, cursor binding,
none/unrestricted/errors, duplicate grants, overflow, revocation and moves.
The default host route also has a maintained composition HTTP regression.
