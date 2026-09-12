package firestore

import (
	"context"
	"embed"
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
// The fragment is DERIVED from the complete supported query matrix (ruling R5),
// not from the queries the tests happen to issue: queryMatrix in indexes_test.go
// is the specification, and the derivation is checked both ways — a query with
// no index and an index no query needs both fail the build. SCHEMA.md §8 states
// the entry set, the derivation rules, and the 32-of-200 composite budget.
//
// A green EMULATOR run is still no evidence about index coverage — the emulator
// enforces no composite index and keeps no index registry. Only the live leg
// (indexes_live_test.go) proves the set, and as of N5 it has not run.
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
// transaction. The authentication SQL adapters also own their focused atomic
// operations rather than guaranteeing ambient joining. Running on the client
// beside the host's transaction would SILENTLY split an atomic unit, so the call
// fails instead. It wraps [sdk.ErrInvalidInput]: the wiring is wrong, and no
// retry fixes it.
var ErrAmbientTransactionUnsupported = fmt.Errorf("authentication firestore store: this store does not join an ambient firestore transaction — call it outside Transact (firestore-stores ruling R1): %w", sdk.ErrInvalidInput)

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
// live database — every composite index AND every single-field override it
// declares (SCHEMA.md §8.4). The probe is this store's analogue of the SQL
// siblings' table probe: a missing or still-building composite index fails at
// WIRING TIME, naming the index and the console page, instead of failing the
// first production query. The caller's context and the connector's ProbeTimeout
// bound startup probing. Pass
// [WithoutIndexProbe] on the emulator (which keeps no index registry, so the
// probe refuses with ErrProbeUnavailableOnEmulator rather than a silent skip) or
// where the credential cannot list indexes.
//
// UserAdmin, ActiveSessions, and Passwordless are returned UNCONDITIONALLY,
// mirroring turso (N-D4): a store adapter that can serve a capability always
// offers it, and the host's policies decide whether anything mounts. It does NOT
// deploy anything: the host owns its manifest and its deployment (see
// [ExportIndexes]), exactly as the host owns migrations for the SQL stores.
// ctx controls startup probing only; each probe is also bounded by
// firestoredb.ProbeTimeout. db remains owned by the caller.
func Repositories(ctx context.Context, db *firestoredb.DB, opts ...Option) (auth.Repositories, error) {
	if _, err := newConfig(ctx, db, opts); err != nil {
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
func newConfig(ctx context.Context, db *firestoredb.DB, opts []Option) (config, error) {
	cfg := config{probeIndex: true}
	for _, o := range opts {
		if o == nil {
			return config{}, fmt.Errorf("firestore pocket store: nil option: %w", sdk.ErrInvalidInput)
		}
		o(&cfg)
	}
	if db == nil {
		return config{}, fmt.Errorf("authentication firestore store: nil database: %w", sdk.ErrInvalidInput)
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
