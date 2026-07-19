module github.com/gopernicus/gopernicus/examples/auth-cms

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/authentication v0.0.0
	github.com/gopernicus/gopernicus/features/authentication/views/goth v0.0.0
	github.com/gopernicus/gopernicus/features/authorization v0.0.0
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/features/cms/views/goth v0.0.0
	github.com/gopernicus/gopernicus/features/events v0.0.0
	github.com/gopernicus/gopernicus/features/jobs v0.0.0
	github.com/gopernicus/gopernicus/features/jobs/stores/pgx v0.0.0
	github.com/gopernicus/gopernicus/features/jobs/stores/turso v0.0.0
	github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/pgxdb v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/turso v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
	github.com/gopernicus/gopernicus/ui/goth v0.0.0
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
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20240325151524-a685a6edb6d8 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/gopernicus/gopernicus/features/authentication => ../../features/authentication

replace github.com/gopernicus/gopernicus/features/authentication/views/goth => ../../features/authentication/views/goth

replace github.com/gopernicus/gopernicus/features/authorization => ../../features/authorization

replace github.com/gopernicus/gopernicus/features/cms => ../../features/cms

replace github.com/gopernicus/gopernicus/features/cms/views/goth => ../../features/cms/views/goth

replace github.com/gopernicus/gopernicus/features/events => ../../features/events

replace github.com/gopernicus/gopernicus/features/jobs => ../../features/jobs

replace github.com/gopernicus/gopernicus/features/jobs/stores/pgx => ../../features/jobs/stores/pgx

replace github.com/gopernicus/gopernicus/features/jobs/stores/turso => ../../features/jobs/stores/turso

replace github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt => ../../integrations/cryptids/bcrypt

replace github.com/gopernicus/gopernicus/integrations/datastores/pgxdb => ../../integrations/datastores/pgxdb

replace github.com/gopernicus/gopernicus/integrations/datastores/turso => ../../integrations/datastores/turso

replace github.com/gopernicus/gopernicus/sdk => ../../sdk

replace github.com/gopernicus/gopernicus/ui/goth => ../../ui/goth
