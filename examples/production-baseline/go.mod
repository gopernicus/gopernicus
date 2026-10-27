module github.com/gopernicus/gopernicus/examples/production-baseline

go 1.26.10

require (
	github.com/gopernicus/gopernicus/integrations/cryptids/go-oidc v0.2.0
	github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt v0.3.0
	github.com/gopernicus/gopernicus/integrations/datastores/pgxdb v0.14.0
	github.com/gopernicus/gopernicus/pockets v0.1.0
	github.com/gopernicus/gopernicus/pockets/authentication v0.32.0
	github.com/gopernicus/gopernicus/pockets/authentication/stores/pgx v0.16.0
	github.com/gopernicus/gopernicus/pockets/authorization v0.31.0
	github.com/gopernicus/gopernicus/pockets/authorization/stores/pgx v0.21.0
	github.com/gopernicus/gopernicus/sdk v0.20.0
)

require (
	github.com/coreos/go-oidc/v3 v3.17.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
