# Segovia v2: cross-origin protection for host API routes

Paste everything below the line into a session working in
`/Users/jrazmi/code/segovia/segovia/v2`. This was prepared on 2026-10-21 from a read-only look
at that checkout, which was on branch `josh/clips-template-key` at `a1c6b9ab` with 5 uncommitted
files. **Land this BEFORE the authentication v0.19.1 repin**
(`plans/segovia-v2-authentication-v0.19.1-repin-handoff.md`). It does not depend on any
Gopernicus version.

---

Add browser cross-origin (CSRF) protection to Segovia v2's own HTTP routes. This is a small
security fix, not a feature. Plan it briefly, implement it, and verify it against the running
server and the flightdeck SPA.

## The gap

- The flightdeck SPA authenticates **with the session cookie**: `credentials: 'include'` on every
  call (`ui/ts/packages/shared/src/api/http.ts`). So every Segovia API mutation is
  cookie-authenticated, and the browser attaches that cookie to forged requests too.
- The Gopernicus authentication pocket protects only its bundled `/auth/*` routes (origin
  allowlist + double-submit `X-CSRF-Token`). It does **not** protect host routes such as
  `/api/v1/...`.
- Segovia's global middleware (`cmd/server/main.go`, the `router.Use(...)` line, ~:203) has
  `TrustProxies`, `RequestID`, `Logger`, `Panics` and CORS, but **no origin check**. A
  read-only grep found no `CrossOriginProtection`, `Sec-Fetch-Site` or CSRF check on host routes
  outside tests. **CORS is not CSRF protection:** it controls which origins may read a response,
  and a forged form POST never needs to read one.
- So host mutations rely only on the `SameSite=Lax` cookie. That blocks cross-site forgery but
  not forgery from a sibling subdomain of the same registrable domain. Segovia builds per-tenant
  client and frame origins (`cmd/server/clips.go`, `dashboards.go`, `clips_publish.go`). **Find
  out** whether any tenant- or user-controlled content is served on a same-site origin, and say
  so in your report. If it is, this fix is urgent.

## The fix (follow gps-360-go's pattern)

gps-360-go's `cmd/server/main.go` (~:253–274) is the reference:

```go
origins := http.NewCrossOriginProtection()
for _, origin := range allowedOrigins { // the host's own origin(s) + the dev-loop origin
	if err := origins.AddTrustedOrigin(origin); err != nil { return err }
}
origins.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	web.RespondJSONError(w, web.ErrForbidden("cross-origin mutation is not allowed"))
}))
cors := web.CORSWithConfig(...) // CORS BEFORE the origin guard, so preflights are answered first
router.Use(..., cors, origins.Handler)
```

For Segovia:
- **Trusted origins:** `srv.Origin()`, plus whatever origins the existing `corsConfig(hosts)`
  admits for the dev loop. Read how `corsConfig` builds them and reuse the same list, so CORS and
  the origin guard never disagree.
- **Order:** keep CORS before `origins.Handler`.
- **Scope:** global. `CrossOriginProtection` only rejects unsafe methods (POST, PUT, PATCH,
  DELETE) from a cross origin; same-origin SPA calls (`Sec-Fetch-Site: same-origin`), GETs and
  non-browser clients (no `Origin`/`Sec-Fetch-Site`) pass.
- **Check every exception before shipping:** cross-origin POSTs Segovia legitimately receives,
  such as webhooks, OAuth or OIDC callbacks (`callbackHop` is mounted right after the global
  stack), embedded dashboards or clips that post back, and the unsubscribe link
  (`/api/v1` … `UnsubscribePath`). Server-to-server callers send no `Origin` and pass. A
  browser-initiated cross-origin POST that must work needs an explicit, justified bypass
  (`origins.AddInsecureBypassPattern`) or a trusted origin. List each one in your report.

## Verify

- `go build ./... && go vet ./... && go test ./...`. Add a `cmd/server` test: a cross-site POST
  (`Sec-Fetch-Site: cross-site`, foreign `Origin`) to a host `/api/v1` mutation → 403; the same
  request same-origin → passes; a GET cross-site → passes; a preflight `OPTIONS` from the dev
  origin → still answered by CORS.
- Run the server and the SPA:
  - the whole app still works: sign in, then perform a few mutations across features;
  - a `curl` POST with the session cookie plus `-H 'Origin: https://evil.example'
    -H 'Sec-Fetch-Site: cross-site'` → 403;
  - the dev loop (Vite on its own origin) still works.
- Report: the trusted-origin list, every bypass and why, the same-site tenant-content finding,
  and the PR link.
