module github.com/gopernicus/gopernicus/examples/auth-cms

go 1.26.1

require (
	github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/pgxdb v0.5.0
	github.com/gopernicus/gopernicus/integrations/datastores/turso v0.1.0
	github.com/gopernicus/gopernicus/pockets/authentication v0.7.0
	github.com/gopernicus/gopernicus/pockets/authentication/views/goth v0.3.0
	github.com/gopernicus/gopernicus/pockets/authorization v0.6.0
	github.com/gopernicus/gopernicus/pockets/cms v0.2.0
	github.com/gopernicus/gopernicus/pockets/cms/views/goth v0.2.0
	github.com/gopernicus/gopernicus/pockets/events v0.2.0
	github.com/gopernicus/gopernicus/pockets/jobs v0.3.0
	github.com/gopernicus/gopernicus/pockets/jobs/stores/pgx v0.4.0
	github.com/gopernicus/gopernicus/pockets/jobs/stores/turso v0.3.0
	github.com/gopernicus/gopernicus/sdk v0.5.0
	github.com/gopernicus/gopernicus/ui/goth v0.1.0
)

require (
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.0 // indirect
	github.com/coder/websocket v1.8.12 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.8.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/tursodatabase/libsql-client-go v0.0.0-20260528064733-9d5d30a29a60 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/exp v0.0.0-20240325151524-a685a6edb6d8 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/gopernicus/gopernicus/pockets/authentication => ../../pockets/authentication

replace github.com/gopernicus/gopernicus/pockets/authentication/views/goth => ../../pockets/authentication/views/goth

replace github.com/gopernicus/gopernicus/pockets/authorization => ../../pockets/authorization

replace github.com/gopernicus/gopernicus/pockets/cms => ../../pockets/cms

replace github.com/gopernicus/gopernicus/pockets/cms/views/goth => ../../pockets/cms/views/goth

replace github.com/gopernicus/gopernicus/pockets/events => ../../pockets/events

replace github.com/gopernicus/gopernicus/pockets/jobs => ../../pockets/jobs

replace github.com/gopernicus/gopernicus/pockets/jobs/stores/pgx => ../../pockets/jobs/stores/pgx

replace github.com/gopernicus/gopernicus/pockets/jobs/stores/turso => ../../pockets/jobs/stores/turso

replace github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt => ../../integrations/cryptids/bcrypt

replace github.com/gopernicus/gopernicus/integrations/datastores/pgxdb => ../../integrations/datastores/pgxdb

replace github.com/gopernicus/gopernicus/integrations/datastores/turso => ../../integrations/datastores/turso

replace github.com/gopernicus/gopernicus/sdk => ../../sdk

replace github.com/gopernicus/gopernicus/ui/goth => ../../ui/goth
