package firestore

import (
	"context"
	"embed"
	"fmt"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// IndexesFS holds the embedded index manifest — this store's analogue of the SQL
// siblings' MigrationsFS. A host MERGES it into its own firestore.indexes.json
// with [ExportIndexes] and deploys it; the constructor probes the live database
// for it at wiring time.
//
// The manifest is PROVISIONAL until task A5 of the firestore-stores milestone
// derives the definitive set from the complete query matrix and proves it
// against a live database. The emulator enforces no composite index, so an
// emulator-green run proves nothing about it (SCHEMA.md §7).
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

// errNotImplemented is the A1 skeleton's placeholder. Tasks A2–A4 replace every
// method body that returns it; nothing but those bodies may reference it, and it
// is removed when the last one lands.
var errNotImplemented = fmt.Errorf("authorization firestore store: not implemented: %w", sdk.ErrUnavailable)

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	guardian   mutation.GuardianPolicy
	probeIndex bool
}

// WithGuardianPolicy overrides the default guardian invariant (owner protected
// on every resource type, minimum one direct anchor) the atomic mutation
// repository enforces inside its Firestore transaction. Supply an empty policy
// to declare no invariant, or a narrower rule set to protect specific resource
// types. It mirrors the memstore's and both SQL siblings' WithGuardianPolicy so
// the guardian contract is wired identically across families.
func WithGuardianPolicy(p mutation.GuardianPolicy) Option {
	return func(c *config) { c.guardian = p }
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
// The mutation repository defaults to the ratified guardian policy unless
// [WithGuardianPolicy] overrides it.
func Repositories(db *firestoredb.DB, opts ...Option) (authorization.Repositories, error) {
	cfg, err := newConfig(db, opts)
	if err != nil {
		return authorization.Repositories{}, err
	}
	return authorization.Repositories{
		Relationships: newRelationshipStore(db),
		Roles:         newRoleStore(db),
		Mutations:     newMutationStore(db, cfg.guardian),
	}, nil
}

// RelationshipRepository returns only the relationship port, probing the same
// manifest. It is the direct constructor for a baseline-only host that
// intentionally does not wire the advanced mutation repository.
func RelationshipRepository(db *firestoredb.DB, opts ...Option) (relationship.Storer, error) {
	if _, err := newConfig(db, opts); err != nil {
		return nil, err
	}
	return newRelationshipStore(db), nil
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
func newConfig(db *firestoredb.DB, opts []Option) (config, error) {
	cfg := config{guardian: mutation.DefaultGuardianPolicy(), probeIndex: true}
	for _, o := range opts {
		o(&cfg)
	}
	if db == nil {
		return config{}, fmt.Errorf("authorization firestore store: nil database: %w", sdk.ErrInvalidInput)
	}
	if cfg.probeIndex {
		if err := firestoredb.ProbeIndexesFS(context.Background(), db, IndexesFS, IndexesFile); err != nil {
			return config{}, err
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
