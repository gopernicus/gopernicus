module github.com/gopernicus/gopernicus/features/cms/stores/pgx

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/pgxdb v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.8.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/gopernicus/gopernicus/features/cms => ../..

replace github.com/gopernicus/gopernicus/integrations/datastores/pgxdb => ../../../../integrations/datastores/pgxdb

replace github.com/gopernicus/gopernicus/sdk => ../../../../sdk
