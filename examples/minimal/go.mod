module github.com/gopernicus/gopernicus/examples/minimal

go 1.26.1

require (
	github.com/gopernicus/gopernicus/pockets/cms v0.2.0
	github.com/gopernicus/gopernicus/pockets/cms/views/goth v0.2.0
	github.com/gopernicus/gopernicus/sdk v0.5.0
	github.com/gopernicus/gopernicus/ui/goth v0.1.0
)

require github.com/a-h/templ v0.3.1020 // indirect

replace github.com/gopernicus/gopernicus/pockets/cms => ../../pockets/cms

replace github.com/gopernicus/gopernicus/pockets/cms/views/goth => ../../pockets/cms/views/goth

replace github.com/gopernicus/gopernicus/sdk => ../../sdk

replace github.com/gopernicus/gopernicus/ui/goth => ../../ui/goth
