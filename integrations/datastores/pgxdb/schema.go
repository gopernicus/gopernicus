package pgxdb

import (
	"fmt"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	// maxSchemaNameBytes is Postgres' default identifier length limit
	// (NAMEDATALEN-1). A longer name is silently truncated server-side, which
	// would let two distinct schema names collide, so NewSchema rejects it.
	maxSchemaNameBytes = 63

	// reservedSchemaPrefix is the namespace prefix Postgres reserves for
	// system catalogs.
	reservedSchemaPrefix = "pg_"

	// informationSchema is the SQL-standard catalog namespace, reserved by the
	// server and never a valid target for a migration stream.
	informationSchema = "information_schema"
)

// Schema is a validated Postgres schema name. The zero value means "no
// schema": Table renders bare names, so a store constructed without one
// emits exactly the SQL it always has.
type Schema struct{ name string }

// NewSchema validates name against the identifier segment rule (letter, then
// letters/digits/underscores; single segment, no dots) and the Postgres schema
// restrictions below, and wraps sdk.ErrInvalidInput on failure. Quoting
// preserves case: "Auth" and "auth" are different schemas.
//
// Stricter than QuoteIdentifier: it rejects the empty name, names longer than
// Postgres' default 63-byte identifier limit, the reserved "pg_" prefix
// (case-insensitively, conservatively), and the built-in information_schema
// namespace. "public" remains valid as an explicit schema.
func NewSchema(name string) (Schema, error) {
	if name == "" {
		return Schema{}, fmt.Errorf("schema name is empty: %w", sdk.ErrInvalidInput)
	}
	if len(name) > maxSchemaNameBytes {
		return Schema{}, fmt.Errorf("schema name %q is %d bytes, over the %d-byte Postgres identifier limit: %w",
			name, len(name), maxSchemaNameBytes, sdk.ErrInvalidInput)
	}
	if !identifierSegment.MatchString(name) {
		return Schema{}, fmt.Errorf("invalid schema name %q: %w", name, sdk.ErrInvalidInput)
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, reservedSchemaPrefix) {
		return Schema{}, fmt.Errorf("schema name %q uses the reserved %q prefix: %w",
			name, reservedSchemaPrefix, sdk.ErrInvalidInput)
	}
	if lower == informationSchema {
		return Schema{}, fmt.Errorf("schema name %q is a reserved system namespace: %w", name, sdk.ErrInvalidInput)
	}
	return Schema{name: name}, nil
}

// Table qualifies table with the schema — "<schema>".table — or returns it
// bare for the zero Schema. table is trusted (a store's own literal), never
// host input.
func (s Schema) Table(table string) string {
	if s.IsZero() {
		return table
	}
	return `"` + s.name + `".` + table
}

// IsZero reports whether s is the zero Schema, which renders bare names.
func (s Schema) IsZero() bool { return s.name == "" }

// String returns the bare schema name, or "" for the zero Schema.
func (s Schema) String() string { return s.name }
