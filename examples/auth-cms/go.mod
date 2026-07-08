module github.com/gopernicus/gopernicus/examples/auth-cms

go 1.26.1

require (
	github.com/gopernicus/gopernicus/features/auth v0.0.0
	github.com/gopernicus/gopernicus/features/cms v0.0.0
	github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt v0.0.0
	github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt v0.0.0
	github.com/gopernicus/gopernicus/sdk v0.0.0
)

require (
	github.com/a-h/templ v0.3.1020 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.51.0 // indirect
)

replace github.com/gopernicus/gopernicus/features/auth => ../../features/auth

replace github.com/gopernicus/gopernicus/features/cms => ../../features/cms

replace github.com/gopernicus/gopernicus/integrations/cryptids/bcrypt => ../../integrations/cryptids/bcrypt

replace github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt => ../../integrations/cryptids/golang-jwt

replace github.com/gopernicus/gopernicus/sdk => ../../sdk
