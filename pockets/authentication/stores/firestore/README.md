# Authentication Firestore adapter

This adapter is incomplete. `Repositories` returns a full bundle, including stubs;
a non-nil repository slot does not demonstrate that its operations work. Its
document and index design is recorded in [SCHEMA.md](SCHEMA.md).

Construct with `Repositories(ctx, db, opts...)`, passing the host startup context.
The index probe honors cancellation and the earlier of the host deadline or
`firestoredb.ProbeTimeout` (30 seconds). A canceled context also rejects
construction with `WithoutIndexProbe()`. The context is used only during
construction; the host retains ownership of the database.

| Capability | Implementation status |
| --- | --- |
| Users, identifiers, password reads/writes | Implemented, including atomic initial credentials and revision-checked password changes |
| Sessions and active-user session admission | Implemented; admission checks the expected authentication revision |
| OAuth accounts and OAuth states | Implemented, including conditional link/adoption |
| Authentication grants | Implemented; revision/session admission and policy-aware one-use consumption |
| User directory list and summary | Implemented; lifecycle `SetStatus` remains a stub |
| Credential inventory and policy mutations | `Snapshot` and `Apply` remain stubs |
| Challenges, contact changes, password resets, passwordless redemption | Stubs |
| Invitations, service accounts, API keys, security events | Stubs |

Unsupported methods return the adapter's explicit not-implemented error. Features
that use those methods are not ready for Firestore hosts. Completing them is a
separate milestone; this authentication audit strengthens the implemented methods
and keeps stub signatures compatible with the new contracts.

Every public method rejects a connector-owned ambient transaction with
`ErrAmbientTransactionUnsupported`. Atomic operations own their transaction and
read before writing. The authentication SQL adapters also do not promise ambient
joining; multiple service calls and an external grant are not one transaction.

The default test run covers local structure and ownership. Emulator tests require
`FIRESTORE_EMULATOR_HOST` and `-tags=integration`; the full conformance suite still
fails on the listed stubs. Run supported families explicitly when verifying an
implemented slice. Emulator success does not prove live composite-index coverage
or production contention behavior. Hosts own index deployment; `WithoutIndexProbe`
is for emulator verification, not a production substitute.

Consumer migration: [AUDIT-022](../../../../AUDIT.md#audit-022-authentication-proof-lifecycle-and-host-api).
