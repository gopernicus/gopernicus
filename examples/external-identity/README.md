# examples/external-identity

The zero-infrastructure proof host for **external identity providers**
(the external identity providers plan, task-7). Every user signs in
at an identity provider; this host verifies the provider's token, maps its
subject to a local user through the authentication pocket's external engine,
and admits the request as an ordinary `sdk.Principal`. It boots with no
datastore driver, no `TokenSigner`, no sessions and no mail.

The provider is a **development issuer served by this same process**
(`integrations/authentication/devissuer`, over gooidc's `oidctest`). It
exists only in development: `devissuer.New` refuses every other runtime mode,
so this host does not boot in production until a real provider is wired in its
place (`TestProductionRefusesTheDevIssuer`).

## What it proves

- **The A′ engine stands alone.** `authlogic.NewExternalAuthenticator` over four
  in-memory repositories is the whole identity stack, and it is the
  `CredentialService` behind `authenticationhttp.NewAuthenticator`.
- **The mirror maps one subject to one local user.** The first token for
  `sub: u1` provisions a user (one `external_subject_provisioned` event) and
  returns an internal id, never `u1`. Every later token for `u1` reaches the
  same id with no second provisioning event.
- **Unverified addresses are never projected.** A token with
  `email_verified: false` still maps to a user, but `GET /api/me` lists no
  address for it.
- **Unknown issuers and absent credentials are denied** with 401. A token is
  routed to exactly one provider by its `iss`; an unwired `iss` never reaches a
  verifier.
- **The provider's session cookie reaches the same user.** `POST /dev/session`
  sets `__session`, minted under a second issuer string
  (`/dev/issuer/session`). Both issuers are wired under one provider name
  (`dev`), so a header token and a cookie for one subject share one mirror row.
- **One local status read per mutation.** A cookie `POST /api/echo` runs the
  default `LiveOnMutation` tier, and the log shows exactly one
  `memstore: local user read`: the read the engine already made while
  verifying is stamped on the proof and reused.

## Layout

```
cmd/server/                                composition root (wiring only, H10)
integrations/authentication/devissuer/     dev OIDC issuer: JWKS, POST /dev/token, POST /dev/session (H11)
internal/logic/domains/me/                 the me domain: principal → profile via sdk.IdentityResolver
internal/inbound/domains/me/               GET /api/me, POST /api/echo
pockets/authentication/outbound/memstore/  users, identifiers, external subjects, audit rail (in memory)
```

The memory store is deliberately the minimum the external engine reads and
writes, not a copy of auth-cms's `authmem`: it holds no passwords, sessions,
challenges or API keys, and `ApplyVerifiedChange` answers
`ErrContactChangeUnsupported`. It passes `storetest.RunExternalSubjects`. It
logs every user read and audit row, which is what the protocol below counts.

The Auth0 preset (`integrations/authentication/auth0`) supplies the claim
mapping: the dev issuer mints Auth0-shaped claims, and a real Auth0 tenant is a
change to the verifier's issuer and audience in `cmd`.

## Module graph — zero datastore drivers

```sh
cd examples/external-identity && GOWORK=off go list -m all | grep -iE 'libsql|pgx|sqlite'   # empty
```

## The protocol

Port 8080. Every request and response below is from a real run.

```sh
cd examples/external-identity && go run ./cmd/server

# 1. a dev-issuer token -> 200 with an internal user id (not "u1")
TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true}' | jq -r .token)
curl -i -H "Authorization: Bearer $TOKEN" localhost:8080/api/me
#   HTTP/1.1 200 OK
#   {"principal_type":"user","principal_id":"<ID>","display_name":"","addresses":[{"kind":"email","value":"a@x.test"}]}
#   log: msg="memstore: security event" type=external_subject_provisioned status=success user_id=<ID>

# 2. another token for u1 -> the same <ID>, and no second provisioning event in the log
TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","email":"a@x.test","email_verified":true}' | jq -r .token)
curl -i -H "Authorization: Bearer $TOKEN" localhost:8080/api/me

# 3. an unverified email -> 200, a new user, no address projected
TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u2","email":"b@x.test","email_verified":false}' | jq -r .token)
curl -i -H "Authorization: Bearer $TOKEN" localhost:8080/api/me      # "addresses":[]

# 4. a token from an unwired issuer -> 401
TOKEN=$(curl -s -XPOST localhost:8080/dev/token -d '{"sub":"u1","iss":"http://localhost:9999/other"}' | jq -r .token)
curl -i -H "Authorization: Bearer $TOKEN" localhost:8080/api/me      # 401

# 5. no credential -> 401
curl -i localhost:8080/api/me

# 6. the provider session cookie -> 200, the same <ID> as step 1
curl -c jar -XPOST localhost:8080/dev/session -d '{"sub":"u1","email":"a@x.test","email_verified":true}'
curl -b jar -i localhost:8080/api/me

# 7. a cookie mutation -> 200, with exactly ONE local user read in the log for it
curl -b jar -i -XPOST localhost:8080/api/echo -H 'Content-Type: application/json' -d '{"hello":"world"}'
#   {"echo":{"hello":"world"},"principal_id":"<ID>"}
#   log: msg="memstore: local user read" user_id=<ID> found=true
#        msg=request method=POST path=/api/echo status=200

# 8. the same cookie mutation from a cross-site page -> 403
curl -b jar -i -XPOST localhost:8080/api/echo -H 'Sec-Fetch-Site: cross-site' -H 'Origin: https://evil.example'
```

`GET /api/me` shows two user reads: the engine's status check and the
identity projection `Resolve` makes for the response. `POST /api/echo` makes
no projection, so its single read is the status check alone.

## Cookie mutations and CSRF

`/api` reads the provider cookie, so its unsafe methods are protected by Go's
`http.CrossOriginProtection`, the ratified host posture (ARCHITECTURE.md,
"Host HTML cross-origin posture"): a cross-site browser request is refused
with 403, and a request with neither `Sec-Fetch-Site` nor `Origin` (curl) is
treated as same-origin. The pocket's double-submit token (`GET /auth/csrf` +
`X-CSRF-Token`) guards the pocket's **bundled** mutation routes. This host
mounts none: `NewAuthenticator` builds only the middleware, so there is no
`/auth/csrf` here. A host that mounts the bundled routes beside external
providers gets the token bootstrap from the `CSRFBootstrap` group, which
admits provider-cookie users.

## Tests

```sh
cd examples/external-identity && go test ./... -race
```

`cmd/server/main_test.go` runs the protocol above over a real loopback
listener (the verifiers fetch the JWKS from the same server), including the
one-read count and the cross-site 403.
