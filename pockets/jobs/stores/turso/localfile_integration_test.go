//go:build integration

package turso

// The libsql client carries no SQLite engine, so a local "file:"
// TURSO_DATABASE_URL needs the pure-Go driver registered for the integration run.
import _ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
