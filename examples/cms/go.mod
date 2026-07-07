module github.com/gopernicus/gopernicus/examples/cms

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/features/cms/stores/turso v0.0.0
	github.com/gopernicus/gopernicus/integrations/datastores/turso v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/a-h/parse v0.0.0-20250122154542-74294addb73e // indirect
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.0 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cli/browser v1.3.0 // indirect
	github.com/coder/websocket v1.8.12 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/natefinch/atomic v1.0.1 // indirect
	github.com/tursodatabase/libsql-client-go v0.0.0-20260528064733-9d5d30a29a60 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/exp v0.0.0-20240325151524-a685a6edb6d8 // indirect
	golang.org/x/mod v0.26.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/tools v0.35.0 // indirect
)

replace github.com/gopernicus/gopernicus/sdk => ../../sdk

replace github.com/gopernicus/gopernicus/integrations/datastores/turso => ../../integrations/datastores/turso

replace github.com/gopernicus/gopernicus/features/cms => ../../features/cms

replace github.com/gopernicus/gopernicus/features/cms/stores/turso => ../../features/cms/stores/turso

tool github.com/a-h/templ/cmd/templ
