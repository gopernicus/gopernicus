module github.com/gopernicus/gopernicus/examples/jobs-minimal

go 1.26.1

require (
	github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron v0.0.0
	github.com/gopernicus/gopernicus/pockets/jobs v0.3.0
	github.com/gopernicus/gopernicus/sdk v0.5.0
)

require github.com/robfig/cron/v3 v3.0.1 // indirect

replace github.com/gopernicus/gopernicus/pockets/jobs => ../../pockets/jobs

replace github.com/gopernicus/gopernicus/integrations/scheduling/robfig-cron => ../../integrations/scheduling/robfig-cron

replace github.com/gopernicus/gopernicus/sdk => ../../sdk
