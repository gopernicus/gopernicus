// Package nodrivertest exists only for its test: a binary that imports the
// turso connector without the localfile package, proving the error a host sees
// when it opens a "file:" URL with no SQLite driver registered. The connector's
// own tests register the driver, so this proof needs a package of its own.
package nodrivertest
