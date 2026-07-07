module github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt

go 1.26.1

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

replace github.com/gopernicus/gopernicus/sdk => ../../../sdk
