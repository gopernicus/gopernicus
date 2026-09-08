# pockets/authentication — `Optional()`: the optional-credential posture on the one authenticator

**Module:** `pockets/authentication` (next tag `v0.10.0`; additive, no signature change, no
store change, no schema). `sdk` untouched.
**Status:** DRAFT 2026-09-08, built the same day on branch `authentication-optional-posture`
for owner ratification and tagging. Origin: segovia v2 tenancy plan, D11 and leg 6b ("the
anonymous public read"); the "Upstream flags" entry there has named this leg since 2026-09-04.

## Context

`RequirePrincipal(opts…)` (`authentication-principal-posture.md`, v0.9.0) is the ONE
authenticator: an OR-set over credential kinds and transports, a liveness tier, a browser
denial mode. Every posture it can express is **deny-by-absence**: no credential ⇒ 401 (or the
browser 303). A host that serves one resource to both signed-in principals (ReBAC decides) and
anonymous visitors (a per-resource `public` attribute decides) has no posture for it today, and
the segovia owner's standing rule is that such a capability lands in the pocket, not as a
host-side "is there a cookie or header?" probe that re-derives the pocket's credential
vocabulary (`resolveCredential`'s transport rules, the bearer-shape classing, the cookie name).

## Goal

One more `PrincipalOption`:

```go
func Optional() PrincipalOption
```

Semantics, at the OUTERMOST position:

- **No credential presented within the set** ⇒ the request continues with NO principal and NO
  credential on the context (`CurrentPrincipal` / `CurrentCredential` report false), the
  `Live()` tier is not run (there is no session to prove), `Browser()` does not redirect.
- **A credential presented within the set** ⇒ exactly today's behaviour: resolved and stashed
  when valid; **denied when invalid, expired, revoked, or of a kind outside the set**. A stale
  cookie is a 401, never an anonymous pass — otherwise a person whose session lapsed would
  silently see the public view of a private resource as "not found", and a bearer of the wrong
  kind would be laundered into anonymity.
- "Presented within the set" follows the resolver's own transport rules: a bearer header counts
  only when `TransportHeader` is in the set, the access cookie only when `TransportCookie` is;
  a credential on a transport outside the set is IGNORED (not read), exactly as for a required
  posture, so an anonymous pass there is correct.

Nested: an inner `RequirePrincipal` under an outer `Optional()` that passed anonymously finds no
stash and resolves the request itself — the same request, the same answer: an inner REQUIRED
gate denies (nothing to admit), an inner `Optional()` passes anonymously again. An inner
`Optional()` under an outer required gate narrows off the stash as any inner gate does
(`Optional()` is meaningless there and harmless).

## Design

- `principalSet` gains `optional bool`; `Optional()` sets it. `resolveSet` / `admits` unchanged.
- `RequirePrincipal`, outermost branch: when `resolveCredential` returns `!ok`, and
  `set.optional`, and `!s.credentialPresented(r, set)` ⇒ `next.ServeHTTP(w, r)` with the
  request's ORIGINAL context; otherwise `denyPrincipal` as today.
- `credentialPresented(r, set)`: `set.header && bearerToken(r)` present, or `set.cookie && the
  access cookie present` — the resolver's two reads, by presence only, no verification. It lives
  beside `resolveCredential` so the two cannot drift.
- Exported in `authentication.go` next to `Live()` / `Browser()`. No new named helper: the call
  site is `RequirePrincipal(Optional())`, or `RequirePrincipal(Optional(),
  Accept(CredentialAccessToken), Transports(TransportCookie))` for a browser-only surface.

## Tasks

| # | task | where | done when |
|---|---|---|---|
| 1 | `optional` on the set, `Optional()`, `credentialPresented`, the branch in `RequirePrincipal`. | `internal/logic/authsvc/{credential,machine}.go`, `authentication.go` | builds |
| 2 | `TestRequirePrincipalOptional` in `credential_test.go` on the existing harness: (a) no credential ⇒ 200, `CurrentPrincipal`/`CurrentCredential` false, handler reached; (b) valid cookie ⇒ 200 with the user principal and `CredentialAccessToken`/`TransportCookie`; (c) valid bearer ⇒ 200, header authoritative; (d) expired bearer ⇒ 401, not reached; (e) bad-signature cookie ⇒ 401; (f) revoked API key ⇒ 401; (g) `Optional(), Transports(TransportCookie)` with a garbage bearer and no cookie ⇒ 200 anonymous (the header is not read); (h) `Optional(), Live()` anonymous ⇒ 200 with zero session lookups (`sessionGets` unchanged); `Optional(), Live()` with a deleted session's JWT ⇒ 401; (i) nested: `RequirePrincipal()` inside `RequirePrincipal(Optional())` anonymous ⇒ 401; `RequirePrincipal(Optional())` inside `RequirePrincipal(Optional())` anonymous ⇒ 200; (j) `Optional(), Browser()` anonymous ⇒ 200, no 303; with an expired cookie ⇒ 303 (the browser denial still applies to a presented-but-bad credential). | `credential_test.go` | `go test ./...` in the module green |
| 3 | README "The middleware surface": `Optional()` in the primitive block with a one-paragraph rule ("absent passes anonymously; presented-but-invalid still denies; a transport outside the set is not read"), and the nesting paragraph gains the anonymous-outer sentence. RELEASING notes for `v0.10.0`. | `README.md` | docs match the tests |

## Verify (when built)

- `go build ./... && go vet ./... && go test ./...` in `pockets/authentication`.
- segovia v2 (the consumer): `/d/{id}/content` and `GET /api/v1/dashboards/{id}` on
  `RequirePrincipal(Optional())`; signed-out reads a `public` dashboard 200 and a
  `gopernicus` one 404; an expired session cookie on either ⇒ 401; signed-in unchanged.

## Consumers

- segovia v2 leg 6b (D11): the item read and the content route.
- gps-360-go / coordination-hub: none today.
