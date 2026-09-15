// Package localfile registers the pure-Go SQLite driver (modernc.org/sqlite,
// no cgo) under the database/sql name "sqlite", which is what the pinned libsql
// driver looks for when a Config.URL is a local "file:" database: the libsql
// client carries no SQLite engine of its own. Import it for its side effect,
// beside the code that opens the database:
//
//	import _ "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
//
// Hosts that only ever open hosted URLs never import it and never link the
// engine. The turso package applies its local file profile (WAL, foreign keys,
// busy timeout on every pooled connection) whichever driver is registered.
package localfile

import (
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)
