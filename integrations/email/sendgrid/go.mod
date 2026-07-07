module github.com/gopernicus/gopernicus/integrations/email/sendgrid

go 1.26.1

require (
	github.com/sendgrid/sendgrid-go v3.16.1+incompatible
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/sendgrid/rest v2.6.9+incompatible // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/net v0.56.0 // indirect
)

replace github.com/gopernicus/gopernicus/sdk => ../../../sdk
