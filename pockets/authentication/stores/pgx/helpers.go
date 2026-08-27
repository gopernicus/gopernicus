package pgx

import (
	"embed"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// The canonical table names, one const per relation the canonical migrations
// create. Every statement in this package renders one of these through
// qualified.table, so a schema-scoped store never emits a bare name.
const (
	usersTable           = "users"
	passwordsTable       = "user_passwords"
	sessionsTable        = "sessions"
	oauthAccountsTable   = "oauth_accounts"
	oauthStatesTable     = "oauth_states"
	serviceAccountsTable = "service_accounts"
	apiKeysTable         = "api_keys"
	securityEventsTable  = "security_events"
	invitationsTable     = "invitations"
	identifiersTable     = "user_identifiers"
	challengesTable      = "challenges"
	contactChangesTable  = "contact_changes"
	authGrantsTable      = "authentication_grants"
)

// MigrationsFS holds the embedded schema (app-owned). cmd wires it into the
// connector's RunMigrations so the host applies it pre-boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// MigrationsDir is the directory within MigrationsFS holding the .sql files.
const MigrationsDir = "migrations"

// qualified is the shared table-qualification chokepoint every store in this
// package embeds: one implementation of the WithSchema seam for all eighteen
// constructors, so a new store cannot forget it.
type qualified struct {
	schema pgxdb.Schema
}

// table renders name under the store's schema — bare for the zero Schema, so the
// default emits exactly the SQL this store has always emitted.
func (s qualified) table(name string) string { return s.schema.Table(name) }

// applyOptions folds opts into the construction config. No option leaves the
// zero config, and therefore the zero Schema.
func applyOptions(opts []Option) config {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
