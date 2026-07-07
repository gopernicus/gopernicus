module github.com/gopernicus/gopernicus/examples/minimal

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/net v0.51.0 // indirect
)

replace github.com/gopernicus/gopernicus/features/cms => ../../features/cms

replace github.com/gopernicus/gopernicus/sdk => ../../sdk
