package firestore

import (
	"context"
	"fmt"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusCollection / statusDocument name the reserved path StatusCheck reads. It
// is never written: the read is the round-trip, and a NotFound answer proves the
// database served the request. The names are ordinary identifiers on purpose —
// Firestore rejects any collection or document id matching __.*__ with
// InvalidArgument, so the tempting "__gopernicus__" spelling is illegal.
const (
	statusCollection = "gopernicus_status"
	statusDocument   = "status"
)

// statusCheckTimeout bounds a StatusCheck whose context carries no deadline.
const statusCheckTimeout = time.Second

// DB wraps a Firestore client bound to one project and database. Vendor errors
// from the read/write surface are mapped to sdk sentinels via MapError.
//
// There is deliberately no Underlying() or Client() accessor (guard G9): stores
// reach documents through Collection/Doc and issue I/O through the tx-aware
// Reader/Writer seams, so an ambient transaction can never be bypassed.
type DB struct {
	client      *gcfs.Client
	project     string
	database    string
	maxAttempts int

	// clientOpts are the credential options Open built, kept so ProbeIndexes
	// can construct its own Admin API client against the SAME identity. The
	// Admin API is a different service with a different Go client; without
	// this, a host that passed CredentialsJSON would find the probe silently
	// falling back to Application Default Credentials.
	clientOpts []option.ClientOption
}

// Close releases the client's resources.
func (d *DB) Close() error {
	return d.client.Close()
}

// Emulated reports whether this client talks to the Firestore emulator
// (FIRESTORE_EMULATOR_HOST observed by the vendor client at Open). The index
// probe and the test factories branch on it LOUDLY: the emulator keeps no index
// registry, so an index check against it would be a false green.
func (d *DB) Emulated() bool {
	return d.client.UsesEmulator
}

// Target returns the project/database this DB is bound to, the same string
// Config.Redacted produces. Safe for logs.
func (d *DB) Target() string {
	return fmt.Sprintf("projects/%s/databases/%s", d.project, d.database)
}

// Collection returns a reference to a top-level collection. Building a
// reference performs no I/O.
func (d *DB) Collection(name string) *gcfs.CollectionRef {
	return d.client.Collection(name)
}

// Doc returns a reference to one document in a top-level collection. Building a
// reference performs no I/O.
func (d *DB) Doc(collection, id string) *gcfs.DocumentRef {
	return d.client.Collection(collection).Doc(id)
}

// StatusCheck returns nil if it can successfully talk to the database. It reads
// one reserved document that is never written: NotFound is the healthy answer —
// it proves the round-trip completed and the caller is authorized to read —
// while transport, permission, and quota failures surface as themselves.
func StatusCheck(ctx context.Context, db *DB) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusCheckTimeout)
		defer cancel()
	}
	_, err := db.Doc(statusCollection, statusDocument).Get(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	return nil
}
