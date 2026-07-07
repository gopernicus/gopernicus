module github.com/gopernicus/gopernicus/integrations/oauth/google

go 1.26.1

require (
	github.com/coreos/go-oidc/v3 v3.17.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/go-jose/go-jose/v4 v4.1.3 // indirect
	golang.org/x/oauth2 v0.34.0 // indirect
)

replace github.com/gopernicus/gopernicus/sdk => ../../../sdk
