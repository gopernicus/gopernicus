package firestore

import (
	"context"
	"embed"
	"fmt"
	"slices"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// IndexesFS holds the embedded index manifest — this store's analogue of the SQL
// siblings' MigrationsFS. A host MERGES it into its own firestore.indexes.json
// with [ExportIndexes] and deploys it; the constructor probes the live database
// for it at wiring time.
//
// Its baseline entries are DERIVED from the complete query matrix (SCHEMA.md §7) by
// the rules in SCHEMA.md §9.1, and indexes_test.go asserts the correspondence
// both ways: a query with no index and an index no query needs both fail the
// build. The emulator enforces no composite index, so an emulator-green run
// still proves nothing about the SET being right — indexes_live_test.go executes
// every matrix row against a real database, and until it runs the manifest is
// derived and reviewed rather than proven.
//
//go:embed firestore.indexes.json
var IndexesFS embed.FS

// IndexesFile is the manifest's name within [IndexesFS] and the conventional
// name of a host's own manifest.
const IndexesFile = "firestore.indexes.json"

// ErrAmbientTransactionUnsupported reports a store method called with a context
// carrying a connector-owned Firestore transaction (milestone ruling R1). A
// Firestore transaction requires every read to precede every write and never
// observes its own pending writes, so this store cannot join a host's ambient
// transaction the way the pgx and turso adapters do. Running on the client
// beside the host's transaction would SILENTLY split an atomic unit, so the call
// fails instead. It wraps [sdk.ErrInvalidInput]: the wiring is wrong, and no
// retry fixes it.
var ErrAmbientTransactionUnsupported = fmt.Errorf("authorization firestore store: this store does not join an ambient firestore transaction — call it outside Transact (firestore-stores ruling R1): %w", sdk.ErrInvalidInput)

// errAmbientMutation is the guarded-mutation form of the same refusal. It wraps
// BOTH sentinels: the store-typed one every port method answers with, and
// [mutation.ErrGuardedInsideTransaction], which the pocket already defines for
// exactly this refusal and which the shared conformance suite asserts.
var errAmbientMutation = fmt.Errorf("%w (%w)", ErrAmbientTransactionUnsupported, mutation.ErrGuardedInsideTransaction)

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	guardian   mutation.GuardianPolicy
	probeIndex bool
	audit      bool
}

// WithAudit records actual tuple and role changes in the write transaction.
// Each write requires an explicit audit source; recording is off by default.
func WithAudit() Option { return func(c *config) { c.audit = true } }

// WithGuardianPolicy selects the invariant enforced inside mutation transactions.
// The default is empty. The option snapshots its rules for each constructed store.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	p.Rules = slices.Clone(p.Rules)
	return func(c *config) { c.guardian = mutation.GuardianPolicy{Rules: slices.Clone(p.Rules)} }
}

// WithoutIndexProbe skips the boot-time index probe (ruling R5). Two callers
// need it, and no third should:
//
//   - a conformance or development run against the EMULATOR, which keeps no
//     index registry and enforces no composite index — the probe refuses there
//     rather than answering a false green;
//   - a host whose runtime service account cannot be granted
//     datastore.indexes.list, which then owns index deployment itself.
//
// Everything else should let the probe run: a missing index is otherwise
// discovered as a FAILED_PRECONDITION on a production request.
func WithoutIndexProbe() Option {
	return func(c *config) { c.probeIndex = false }
}

// Repositories returns the authorization repository set backed by db — ALL THREE
// ports wired (relationship.Storer, role.Storer, and the atomic
// mutation.MutationRepository over the shared iam_* collections) — after probing
// the embedded index manifest against the live database. The probe is this
// store's analogue of the SQL siblings' table probe: a missing or still-building
// composite index fails at WIRING TIME, naming the index and the console page,
// instead of failing the first production query. Pass [WithoutIndexProbe] on the
// emulator (which keeps no index registry, so the probe refuses) or where the
// credential cannot list indexes.
//
// It does NOT deploy anything: the host owns its manifest and its deployment
// (see [ExportIndexes]), exactly as the host owns migrations for the SQL stores.
// The mutation repository protects only explicitly configured guardian rules.
// ctx controls startup probing only; each probe is also bounded by
// firestoredb.ProbeTimeout. db remains owned by the caller.
func Repositories(ctx context.Context, db *firestoredb.DB, opts ...Option) (authorization.Repositories, error) {
	cfg, err := newConfig(ctx, db, opts)
	if err != nil {
		return authorization.Repositories{}, err
	}
	return authorization.Repositories{
		Relationships: newRelationshipStore(db, cfg.audit),
		Roles:         newRoleStore(db, cfg.audit),
		Mutations:     newMutationStore(db, cfg.guardian, cfg.audit),
		Audit:         &auditStore{db: db},
	}, nil
}

// RelationshipRepository returns only the relationship port, probing the same
// manifest. It is the direct constructor for a baseline-only host that
// intentionally does not wire the advanced mutation repository.
// ctx controls startup probing only; each probe is also bounded by
// firestoredb.ProbeTimeout. db remains owned by the caller.
func RelationshipRepository(ctx context.Context, db *firestoredb.DB, opts ...Option) (relationships.Storer, error) {
	cfg, err := newConfig(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	return newRelationshipStore(db, cfg.audit), nil
}

// ExportIndexes MERGES this store's manifest into the host's own manifest at
// dst, creating it when absent. A manifest is ONE shared document (a host
// commonly already has one), which is why this merges where the SQL siblings'
// ExportMigrations copies files. The write is atomic and byte-stable, so
// re-exporting an unchanged fragment is an empty diff and a host's unrelated
// indexes survive.
func ExportIndexes(dst string) error {
	m, err := firestoredb.ParseIndexManifest(IndexesFS, IndexesFile)
	if err != nil {
		return err
	}
	return firestoredb.ExportIndexes(m, dst)
}

// newConfig resolves the options and runs the boot probe.
func newConfig(ctx context.Context, db *firestoredb.DB, opts []Option) (config, error) {
	cfg := config{probeIndex: true}
	for _, o := range opts {
		if o == nil {
			return config{}, fmt.Errorf("firestore pocket store: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	if db == nil {
		return config{}, fmt.Errorf("authorization firestore store: nil database: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return config{}, err
	}
	if cfg.probeIndex {
		probeCtx, cancel := context.WithTimeout(ctx, firestoredb.ProbeTimeout)
		defer cancel()
		if err := firestoredb.ProbeIndexesFS(probeCtx, db, IndexesFS, IndexesFile); err != nil {
			return config{}, err
		}
		if cfg.audit {
			auditCtx, cancelAudit := context.WithTimeout(ctx, firestoredb.ProbeTimeout)
			defer cancelAudit()
			if err := firestoredb.ProbeIndexesFS(auditCtx, db, AuditIndexesFS, AuditIndexesFile); err != nil {
				return config{}, err
			}
		}
	}
	return cfg, nil
}

// refuseAmbient is ruling R1's seam: EVERY public port method calls it first, so
// a host that hands this store a Firestore transaction is told, rather than
// silently served from the client beside its transaction.
func refuseAmbient(ctx context.Context) error {
	if _, ok := firestoredb.TxFromContext(ctx); ok {
		return ErrAmbientTransactionUnsupported
	}
	return nil
}

// refuseAmbientMutation is refuseAmbient for the two guarded-mutation methods,
// which additionally answer the pocket's own ErrGuardedInsideTransaction.
func refuseAmbientMutation(ctx context.Context) error {
	if _, ok := firestoredb.TxFromContext(ctx); ok {
		return errAmbientMutation
	}
	return nil
}
