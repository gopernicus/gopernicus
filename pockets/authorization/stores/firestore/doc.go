// Package firestore is the authorization pocket's Google Cloud Firestore store
// adapter — its own module so a host on another datastore never pulls the
// Firestore client into its module graph. It owns the collections, documents,
// and queries; the connector (integrations/datastores/firestore, imported here
// as firestoredb) owns how to talk to Firestore, and the HOST owns the
// database's lifecycle and its index deployment.
//
// The adapter fills all three ports — relationship.Storer (over
// iam_relationships), role.Storer (over iam_roles), and the atomic
// mutation.MutationRepository (over iam_scopes and iam_mutations) — and
// [Repositories] returns all three wired, exactly as the pgx and turso siblings
// do. Kind selection stays the host's wiring choice.
//
// # Two family differences a host is choosing when it picks this store
//
// This store does NOT join an ambient transaction (milestone ruling R1). A
// Firestore transaction requires every read to precede every write and never
// observes its own pending writes, so the ambient-join guarantee the two SQL
// families provide cannot be honored natively. A method whose context carries a
// connector transaction fails loud with [ErrAmbientTransactionUnsupported]
// rather than quietly running on the client beside the host's transaction; the
// pocket's storetest.RunTransactional family is skipped loudly, not faked. The
// connector still implements crud.Transactor for a host's OWN multi-document
// atomicity — that seam is independent of whether these repositories join it.
//
// Firestore has no DDL, so there is no migration tree. Its analogue is the
// INDEX MANIFEST (ruling R5): the store embeds firestore.indexes.json, a host
// merges it into its own manifest with [ExportIndexes] and deploys it, and the
// constructor PROBES the live database at wiring time — the same "fail at
// wiring time, name the missing thing" promise the SQL stores' table probes
// make. Against the emulator, which keeps no index registry, the probe refuses:
// an emulator run passes [WithoutIndexProbe]. SCHEMA.md is the tracked
// statement of the document layout and of which SQL constraint each claim
// document reproduces.
//
// README.md is what a host reads before choosing this family: the
// ambient-transaction difference above and what to do instead, the manifest's
// scaffold/deploy/probe cycle, and the ceilings and costs the SQL families do
// not have (166 tuples per write call, the same document ceiling on one
// mutation command, the effective listing's O(population) count, contention as
// waiting, and the uncached expansion walk).
package firestore
