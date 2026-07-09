module github.com/gopernicus/gopernicus/features/authentication/stores/pgx

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/authentication v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/pgxdb v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
	github.com/jackc/pgx/v5 v5.8.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/gopernicus/gopernicus/features/authentication => ../..

replace github.com/gopernicus/gopernicus/integrations/datastores/pgxdb => ../../../../integrations/datastores/pgxdb

replace github.com/gopernicus/gopernicus/sdk => ../../../../sdk
