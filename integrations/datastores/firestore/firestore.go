// Package firestore is the datastore connector for Google Cloud Firestore
// (Native mode): it bridges the cloud.google.com/go/firestore client to a small
// wrapper (client lifecycle, document/query surface, transactions, index
// manifest). It is a reusable connector — it owns "how to talk to Firestore,"
// not any app's queries. App-specific repositories live in the app's outbound
// tier or in a pocket's stores/firestore module and consume this package's *DB.
//
// It is its own module (github.com/gopernicus/gopernicus/integrations/datastores/firestore),
// depending only on sdk (for the sentinels MapError targets) and the Google
// Cloud Firestore client — whose module also carries the Admin API package the
// index probe needs — plus that client's own required surface:
// google.golang.org/api for credential options and google.golang.org/grpc for
// the status codes every Firestore error is expressed in (the same posture
// integrations/filestorage/gcs takes with google.golang.org/api). The vendor
// import is aliased gcfs throughout so this package and the library it wraps
// never read alike.
//
// Firestore keeps the SQL connectors' shape and swaps the vocabulary: documents
// and queries for statements, an index manifest for migrations, and a
// transaction whose callback MAY RUN MORE THAN ONCE for BEGIN/COMMIT.
//
// # Mediation discipline
//
// DB.Collection and DB.Doc hand out the vendor's own *CollectionRef,
// *DocumentRef and Query values, and those types carry their own I/O methods
// (ref.Get, q.Documents, ref.Create). Holding one is NOT permission to use
// them. A store issues every read and write through the Reader and Writer
// returned by DB.ReaderFrom(ctx) / DB.WriterFrom(ctx); references and queries
// are values to BUILD, never to execute. The reason is not tidiness: a direct
// ref.Get inside a Transact callback runs on the client, outside the
// transaction, and silently splits an atomic unit — the exact false green the
// tx-aware surface exists to prevent, and one no import guard can see (G9 only
// proves no Underlying()/Client() accessor exists).
//
// Two obligations follow the seam out to the caller, because the vendor's
// iterator cannot be wrapped without losing its cursor:
//
//   - Stop every iterator Documents returns (defer it at the call site).
//   - At the iteration boundary, treat iterator.Done as the loop terminator and
//     pass every other Next error through MapError. Errors the transactional
//     reader defers to Next — the read-after-write refusal among them — arrive
//     nowhere else.
package firestore

import (
	"context"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/sdk"
)

// DefaultDatabase is the database a zero Config.DatabaseID resolves to — the
// vendor's name for a project's unnamed default database.
const DefaultDatabase = gcfs.DefaultDatabaseID

// DefaultMaxAttempts is how many times Transact runs its callback before a
// commit that keeps losing its contention race gives up — the vendor's own
// default, restated here so a Config author does not have to import gcfs.
const DefaultMaxAttempts = gcfs.DefaultTransactionMaxAttempts

// defaultConnectTimeout bounds Open's eager boot validation when Config leaves
// ConnectTimeout unset.
const defaultConnectTimeout = 10 * time.Second

// ErrNoProjectID is returned by Open when Config carries no project. Firestore
// has no discoverable "default project" at this layer: the composition root
// names it (or sets FIRESTORE_PROJECT_ID / GOOGLE_CLOUD_PROJECT and reads it),
// so an empty ProjectID is a wiring bug, not a runtime condition. It wraps
// sdk.ErrInvalidInput, the same sentinel the vendor's InvalidArgument maps to.
var ErrNoProjectID = fmt.Errorf("firestore: empty project id: %w", sdk.ErrInvalidInput)

// Config holds the Firestore client settings.
type Config struct {
	// ProjectID is the Google Cloud project owning the database. Required.
	ProjectID string

	// DatabaseID selects a named database. Empty resolves to DefaultDatabase.
	DatabaseID string

	// CredentialsJSON is a service-account key. Absent (nil/empty) uses
	// Application Default Credentials — the normal posture on Cloud Run, GKE,
	// and any workload with an attached service account. Never logged.
	CredentialsJSON []byte

	// ConnectTimeout bounds client construction and the eager boot validation
	// below. 0 defaults to 10s.
	ConnectTimeout time.Duration

	// Retry, when its Attempts is > 1, makes Open perform EAGER boot validation:
	// a real round-trip (StatusCheck: one document read) retried under a
	// full-jitter exponential backoff, targeting the orchestration race where the
	// database is not yet reachable. Opting into Retry therefore opts into eager
	// boot validation — client construction alone performs no RPC, so a
	// constructor that cannot fail would be vacuous. The zero value keeps Open
	// lazy: the store constructors' index probe is the boot validator.
	//
	// This governs ONLY the boot connectivity check. No read or write is ever
	// auto-retried by the connector — Firestore's own transaction contention
	// retry lives in the vendor client, and statement retry is store-owned.
	Retry RetryPolicy

	// MaxAttempts caps how many times Transact runs its callback when the
	// COMMIT loses a contention race (the vendor's Aborted retry loop). 0
	// defaults to DefaultMaxAttempts, the vendor's own default of 5. It is not
	// a request retry: a failed read or write inside the callback is never
	// retried by itself, and ReadSnapshot's read-only transaction is never
	// retried at all.
	//
	// Raise it for a hot document under heavy write contention (each retry
	// re-runs the whole callback, so the cost is real reads); lower it to 1 to
	// turn a contention loss into an immediate sdk.ErrConflict the caller
	// handles itself.
	MaxAttempts int
}

// Redacted returns the connection target — project and database only — safe to
// place in logs and error messages. Credentials never appear in it.
func (cfg Config) Redacted() string {
	return fmt.Sprintf("projects/%s/databases/%s", cfg.ProjectID, cfg.database())
}

// database resolves DatabaseID, mapping the zero value to the default database.
func (cfg Config) database() string {
	if cfg.DatabaseID == "" {
		return DefaultDatabase
	}
	return cfg.DatabaseID
}

// Open connects to a Firestore database. When FIRESTORE_EMULATOR_HOST is set the
// vendor client routes to the emulator and ignores credentials; DB.Emulated
// reports that so callers (the index probe, tests) can branch LOUDLY.
//
// Client construction issues no RPC. Set Config.Retry.Attempts > 1 to make Open
// verify the database with a real round-trip before returning.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.ProjectID == "" {
		return nil, ErrNoProjectID
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	var opts []option.ClientOption
	if len(cfg.CredentialsJSON) > 0 {
		opts = append(opts, option.WithCredentialsJSON(cfg.CredentialsJSON))
	}

	client, err := gcfs.NewClientWithDatabase(ctx, cfg.ProjectID, cfg.database(), opts...)
	if err != nil {
		return nil, fmt.Errorf("opening firestore database %s: %w", cfg.Redacted(), err)
	}

	db := &DB{
		client:      client,
		database:    cfg.database(),
		project:     cfg.ProjectID,
		maxAttempts: cfg.MaxAttempts,
	}

	if cfg.Retry.Attempts > 1 {
		if err := retry(ctx, cfg.Retry, func(ctx context.Context) error {
			return StatusCheck(ctx, db)
		}); err != nil {
			client.Close()
			return nil, fmt.Errorf("verifying firestore database %s: %w", cfg.Redacted(), err)
		}
	}
	return db, nil
}
