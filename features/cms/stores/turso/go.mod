module github.com/gopernicus/gopernicus/features/cms/stores/turso

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/turso v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/antlr4-go/antlr/v4 v4.13.0 // indirect
	github.com/coder/websocket v1.8.12 // indirect
	github.com/tursodatabase/libsql-client-go v0.0.0-20260528064733-9d5d30a29a60 // indirect
	golang.org/x/exp v0.0.0-20240325151524-a685a6edb6d8 // indirect
)

replace github.com/gopernicus/gopernicus/features/cms => ../..

replace github.com/gopernicus/gopernicus/integrations/datastores/turso => ../../../../integrations/datastores/turso

replace github.com/gopernicus/gopernicus/sdk => ../../../../sdk
