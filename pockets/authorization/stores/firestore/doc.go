// Package firestore implements authorization fact storage and optional change
// history on Google Cloud Firestore. Repositories returns relationship, role,
// atomic mutation and audit reading ports. WithAudit enables transactional
// history recording for all write paths; it is disabled by default.
//
// Guards and mutations share native transactional reads. There are no durable
// mutation receipts or revision anchors. Only definite transaction aborts retry;
// ambiguous commit failures are returned without replay.
//
// This adapter refuses ambient connector transactions because Firestore requires
// reads before writes and cannot observe its pending writes. The connector still
// supports transactions for a host's own documents.
//
// Hosts own database lifecycle and index deployment. ExportIndexes supplies the
// baseline manifest; ExportAuditIndexes supplies optional history indexes.
// Constructors probe required indexes unless WithoutIndexProbe is selected.
// The emulator cannot verify index coverage. Existing installations follow
// UPGRADE.md using explicit UpgradeTupleStorage and RemoveLegacyMutationStorage;
// constructors never migrate or delete data.
package firestore
