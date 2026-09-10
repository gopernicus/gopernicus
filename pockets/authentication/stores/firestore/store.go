package firestore

import (
	"context"
	"embed"
	"errors"
	"fmt"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/sdk"
)

// IndexesFS holds the embedded index manifest — this store's analogue of the SQL
// siblings' MigrationsFS. A host MERGES it into its own firestore.indexes.json
// with [ExportIndexes] and deploys it; the constructor probes the live database
// for it at wiring time.
//
// PROVISIONAL at task N1. The shipped fragment covers the query shapes SCHEMA.md
// §7 already pins, but the manifest's specification is the COMPLETE supported
// query matrix, which task N5 derives (and proves live). Until N5 lands, treat a
// green emulator run as no evidence at all about index coverage: the emulator
// enforces no composite index.
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
var ErrAmbientTransactionUnsupported = fmt.Errorf("authentication firestore store: this store does not join an ambient firestore transaction — call it outside Transact (firestore-stores ruling R1): %w", sdk.ErrInvalidInput)

// errNotImplemented is the N1 skeleton's answer from a port method whose body
// lands in N2–N4. It deliberately wraps NO sdk sentinel: an unclassified error
// surfaces as a 500 and fails every conformance case that expects a domain
// outcome, so a half-built store can never be mistaken for a passing one.
var errNotImplemented = errors.New("authentication firestore store: port method not implemented yet (firestore-stores task N1 skeleton — N2–N4 fill the bodies)")

// Option configures the store set at construction.
type Option func(*config)

type config struct {
	probeIndex bool
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

// Repositories returns the authentication repository set backed by db — ALL
// EIGHTEEN slots wired — after probing the embedded index manifest against the
// live database. The probe is this store's analogue of the SQL siblings' table
// probe: a missing or still-building composite index fails at WIRING TIME,
// naming the index and the console page, instead of failing the first production
// query. Pass [WithoutIndexProbe] on the emulator (which keeps no index
// registry, so the probe refuses) or where the credential cannot list indexes.
//
// UserAdmin, ActiveSessions, and Passwordless are returned UNCONDITIONALLY,
// mirroring turso (N-D4): a store adapter that can serve a capability always
// offers it, and the host's Config decides whether anything mounts. It does NOT
// deploy anything: the host owns its manifest and its deployment (see
// [ExportIndexes]), exactly as the host owns migrations for the SQL stores.
func Repositories(db *firestoredb.DB, opts ...Option) (auth.Repositories, error) {
	if _, err := newConfig(db, opts); err != nil {
		return auth.Repositories{}, err
	}
	return auth.Repositories{
		Users:                newUserStore(db),
		Identifiers:          newIdentifierStore(db),
		Passwords:            newPasswordStore(db),
		Sessions:             newSessionStore(db),
		OAuthAccounts:        newOAuthAccountStore(db),
		OAuthStates:          newOAuthStateStore(db),
		ServiceAccounts:      newServiceAccountStore(db),
		APIKeys:              newAPIKeyStore(db),
		SecurityEvents:       newSecurityEventStore(db),
		Invitations:          newInvitationStore(db),
		Challenges:           newChallengeStore(db),
		PasswordResets:       newPasswordResetStore(db),
		ContactChanges:       newContactChangeStore(db),
		AuthenticationGrants: newAuthGrantStore(db),
		CredentialMutations:  newCredentialMutationStore(db),
		UserAdmin:            newUserAdminStore(db),
		ActiveSessions:       newActiveSessionStore(db),
		Passwordless:         newPasswordlessStore(db),
	}, nil
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
	cfg := config{probeIndex: true}
	for _, o := range opts {
		o(&cfg)
	}
	if db == nil {
		return config{}, fmt.Errorf("authentication firestore store: nil database: %w", sdk.ErrInvalidInput)
	}
	if cfg.probeIndex {
		// The constructor takes no context — neither do the SQL siblings' table
		// probes — so the probe runs on Background and is bounded by the
		// connector's ProbeTimeout (30s): an Admin API that never answers fails
		// the boot instead of hanging it.
		if err := firestoredb.ProbeIndexesFS(context.Background(), db, IndexesFS, IndexesFile); err != nil {
			return config{}, err
		}
	}
	return cfg, nil
}

// refuseAmbient is ruling R1's seam: EVERY public port method calls it first, so
// a host that hands this store a Firestore transaction is told, rather than
// silently served from the client beside its transaction. Guard G24 pins the
// seam to this file so no call site can re-decide it.
func refuseAmbient(ctx context.Context) error {
	if _, ok := firestoredb.TxFromContext(ctx); ok {
		return ErrAmbientTransactionUnsupported
	}
	return nil
}
