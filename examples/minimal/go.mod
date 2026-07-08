module github.com/gopernicus/gopernicus/examples/minimal

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/features/cms/views/templ v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require github.com/a-h/templ v0.3.1020 // indirect

replace github.com/gopernicus/gopernicus/features/cms => ../../features/cms

replace github.com/gopernicus/gopernicus/features/cms/views/templ => ../../features/cms/views/templ

replace github.com/gopernicus/gopernicus/sdk => ../../sdk
