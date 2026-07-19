module github.com/gopernicus/gopernicus/examples/goth-showcase

go 1.26.1

require (
	github.com/a-h/templ v0.3.1020
	github.com/gopernicus/gopernicus/sdk v0.0.0
	github.com/gopernicus/gopernicus/ui/goth v0.0.0
)

replace github.com/gopernicus/gopernicus/sdk => ../../sdk

replace github.com/gopernicus/gopernicus/ui/goth => ../../ui/goth
