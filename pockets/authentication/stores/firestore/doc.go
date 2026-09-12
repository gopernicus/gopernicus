// Package firestore is the authentication pocket's Google Cloud Firestore store
// adapter — its own module so a host on another datastore never pulls the
// Firestore client into its module graph. It owns the collections, documents,
// and queries; the connector (integrations/datastores/firestore, imported here
// as firestoredb) owns how to talk to Firestore, and the HOST owns the
// database's lifecycle and its index deployment.
//
// Repositories implements every authentication repository slot. Startup probing
// uses the caller's context, capped by the connector's ProbeTimeout. The caller
// retains database ownership and controls enabled services through host policies.
//
// # Two family differences a host is choosing when it picks this store
//
// This store does NOT join an ambient transaction (milestone ruling R1). A
// Firestore transaction requires every read to precede every write and never
// observes its own pending writes. A method whose context carries a
// connector transaction fails loud with [ErrAmbientTransactionUnsupported]
// rather than quietly running on the client beside the host's transaction. The
// authentication pocket has no RunTransactional conformance family, so R1 shows
// up here only as that refusal.
//
// Firestore has no DDL, so there is no migration tree. Its analogue is the
// INDEX MANIFEST (ruling R5): the store embeds firestore.indexes.json, a host
// merges it into its own manifest with [ExportIndexes] and deploys it, and the
// constructor PROBES the live database at wiring time — the same "fail at
// wiring time, name the missing thing" promise the SQL stores' table probes
// make. Against the emulator, which keeps no index registry, the probe refuses:
// an emulator run passes [WithoutIndexProbe]. SCHEMA.md is the tracked
// statement of the document layout, of which SQL constraint each claim document
// reproduces, and of every port method's read and write set.
//
// Firestore has no unique constraints either, so each of the thirteen tables'
// UNIQUE indexes becomes a deterministic document id or a CLAIM document
// written in the same transaction as its row (ruling R3, SCHEMA.md §5).
//
// README.md is the host-facing document: the full surface, the index
// manifest's scaffold/deploy/probe cycle, the complete list of family
// differences this store takes from the SQL siblings, why a Firestore TTL policy
// is never a substitute for the ports' counted purges, the measured emulator
// costs, and how to run the hermetic, emulator and live legs. SCHEMA.md is the
// tracked derivation behind it.
package firestore
