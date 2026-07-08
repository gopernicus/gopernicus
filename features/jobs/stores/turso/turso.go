// Package turso is the jobs feature's Turso/libSQL store adapter — its own module
// so a host that brings a different datastore never pulls libsql into its module
// graph (the load-bearing opt-out property). It owns the SQL; the HOST owns its database lifecycle.
//
// Migrations follow the scaffold model (matching the auth and cms turso
// store modules): the canonical *.sql live here, but the recommended
// path is to ExportMigrations into the host's own migrations dir and let the
// host's runner apply them pre-boot through one app-owned ledger. The framework
// never applies migrations behind the host's back.
//
// The two stores implement the feature's ports over the connector's DB/MapError:
// Queue's Claim is one UPDATE ... WHERE job_id=(SELECT ... LIMIT 1) ... RETURNING
// statement (SQLite's single-writer model makes double-claim impossible; the
// lease-expiry reclaim arm is folded in), and Schedules' ClaimDue is a pure value
// compare-and-set. Contention surfaces as waiting (busy-timeout + bounded
// retry-on-busy inside the adapter), never a failed claim.
package turso

import (
	"github.com/gopernicus/gopernicus/features/jobs"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// Repositories returns the jobs repository set backed by db, WITHOUT touching
// migrations. This is the store half of the scaffold model: the host owns and
// applies the schema (see ExportMigrations) and the store just provides repos.
// opts configure the queue store (WithLease); db is the connector wrapper (error
// mapping + Tx), not a raw *sql.DB.
func Repositories(db *tursodb.DB, opts ...QueueOption) jobs.Repositories {
	return jobs.Repositories{
		Queue:     NewQueueStore(db, opts...),
		Schedules: NewScheduleStore(db),
	}
}

// ExportMigrations copies this store's canonical migration files into dst,
// creating dst if needed. It is the scaffold step: after export the files are the
// HOST's, applied by the host's own runner and extended with the host's own
// migrations in the same directory, under one app-owned schema_migrations ledger.
// The framework never reads or applies the host's copies.
func ExportMigrations(dst string) error {
	return tursodb.ExportMigrations(MigrationsFS, MigrationsDir, dst)
}
