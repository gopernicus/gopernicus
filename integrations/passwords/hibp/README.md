# integrations/passwords/hibp

A breach checker over the Have I Been Pwned **Pwned Passwords range API**.
`Checker` structurally implements authentication logic's consumer-owned
`CompromisedPasswordChecker` interface; this module imports no pocket. It uses
only the standard library and the SDK. It remains an integration because its
dependency is the vendor's live API contract.

```go
checker, err := hibp.New()
if err != nil {
    return err
}
cfg.CompromisedPasswordChecker = checker
```

## Surface

| Member | Behavior |
|---|---|
| `New(opts ...Option) (*Checker, error)` | Builds a checker with no network I/O. An invalid option or a nil option returns an error classified as `sdk.ErrInvalidInput`. |
| `WithHTTPClient(*http.Client)` | Uses a host client. The client is copied and its redirect policy replaced; the host's client is not mutated. |
| `WithBaseURL(string)` | Overrides `DefaultBaseURL` (`https://api.pwnedpasswords.com`) for tests or a host-run mirror of the range API; absolute http/https, no query, fragment or user info. |
| `WithUserAgent(string)` | Sets the `User-Agent` the API requires; default `gopernicus-hibp`; must not be blank. |
| `WithTimeout(time.Duration)` | Bounds one request, body included; default 3 s; must be positive. |
| `WithMinimumCount(int)` | Breach count at or above which a password is compromised; default 1; must be at least 1. |
| `Checker.IsCompromised(ctx, password) (bool, error)` | One range lookup; see below. |

## The exchange

`IsCompromised` takes the upper-case hex SHA-1 of the password and sends only
its first five characters: `GET {base}/range/{prefix}` with `Add-Padding: true`.
It parses every `SUFFIX:COUNT` line, compares the remaining 35 characters
against every line with no early exit, and ignores padding rows (count 0). The
password, its hash and the suffix never leave the process.

There are no retries, no cache and no logging. Redirects are never followed.
The body is capped at 1 MiB.

## Failures

Every failure is a **fresh** error classified as `sdk.ErrUnavailable`, with a
class: `hibp: upstream status 429`, `hibp: transport failure`, `hibp: timeout`,
`hibp: malformed response`, `hibp: response too large`. The transport error is
never wrapped, because `*url.Error` prints the request URL and the URL carries
the hash prefix. A caller cancellation keeps `context.Canceled` (or the caller's
deadline) in the chain for `errors.Is`; that error has no URL either.

Authentication fails closed by default: a password whose check errors is
refused, and the error maps to 503. An HIBP outage therefore refuses
registrations and password changes until it recovers.
`CompromisedPasswordFailOpen` is the host's hatch to accept the password
instead. Development hosts usually leave the checker unwired so nothing reaches
the network.

## Testing

Run `go test ./...`. Tests use `httptest` servers only, never the real API:
hit, miss, padding-only and zero-count matches, the minimum count, lowercase
suffixes, 429/500, a redirect (never followed, the target never hit), malformed
lines, an oversized body, a closed server, a timeout, a caller cancellation, the
`User-Agent` and `Add-Padding` headers, the request path carrying only the
prefix, option validation, and an assertion that no error carries a run of five
hex characters, the request path, the server address or the password. A local
interface assertion checks the consumer's method set without importing a pocket.
