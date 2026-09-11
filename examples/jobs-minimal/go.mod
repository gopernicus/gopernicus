module github.com/gopernicus/gopernicus/examples/jobs-minimal

go 1.26.1

require (
	github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron v0.2.0
	github.com/gopernicus/gopernicus/pockets/jobs v0.6.0
	github.com/gopernicus/gopernicus/sdk v0.9.0
)

require github.com/robfig/cron/v3 v3.0.1 // indirect
