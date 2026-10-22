# Host handoff — toolchain and pgx repin (SOC 2 readiness, slice S8, 2026-10-22)

**Status:** OPEN — apply in every host (gps-360-go, Segovia, coordination-hub) on its own schedule.
Framework PR: `chore/toolchain-pgx-repin`. No framework tag is required for a host to be fixed:
the standard-library advisories are fixed by the toolchain the HOST builds with, and pgx moves by
minimum-version selection the moment the host requires the fixed version itself.

## Why

A `govulncheck` run over the framework at `main` @ 39c92851 (symbol-level reachability) found:

- 30 reachable Go standard-library advisories at the pinned `go 1.26.1`, fixed across
  go1.26.2 … go1.26.10 — among them html/template escaper-bypass XSS (GO-2026-4980, 4982, 4865,
  6091, 6599, 6600), mime/multipart memory-limit bypass (GO-2026-6608), os.Root symlink escape
  (GO-2026-4970, 6604), net/http HTTP/2 server crash and memory exhaustion (GO-2026-6617, 6603,
  6612, 6611, 6609), crypto/x509 distrust of compromised intermediates (GO-2026-6809).
- GO-2026-5004 in `github.com/jackc/pgx/v5` v5.8.0 — "SQL injection via placeholder confusion
  with dollar-quoted string literals", reachable from `pgxdb.DB.Query`; fixed in v5.9.2.
- GO-2026-6629 in `golang.org/x/text` v0.39.0 (precis panic), reachable from
  `pgxpool.NewWithConfig`; fixed in v0.41.0.

- Surfaced by the new gate on its first run and fixed in the same PR: `golang.org/x/net`
  < v0.60.0 (the HTTP/2 advisories again, via the firebase integration), AWS SDK
  `service/s3` < v1.97.3 / `aws/protocol/eventstream` < v1.7.8 (GO-2026-5764), and the OTel
  OTLP trace exporters < v1.45.0 (GO-2026-6505).

## What the framework did

`go 1.26.10` in `go.work` and every `go.mod` (consumers of any republished module will need a
go1.26.10+ toolchain, which `GOTOOLCHAIN=auto` fetches on its own); pgx v5.9.2 and x/text
v0.41.0 in every module that pins them; a `make vuln` target and a `vuln` CI job (govulncheck
v1.9.0, per module); full-SHA-pinned GitHub Actions; `.github/dependabot.yml`.

## What a host does

```sh
# 1. toolchain — the stdlib fixes come from here, not from a framework tag
sed -i '' 's/^go 1\.26\.[0-9]*$/go 1.26.10/' go.mod      # or set `toolchain go1.26.10`
go version                                              # GOTOOLCHAIN=auto downloads 1.26.10

# 2. pgx + x/text — MVS takes the maximum, so the host's own require is enough
go get github.com/jackc/pgx/v5@v5.9.2 golang.org/x/text@v0.41.0 golang.org/x/net@v0.60.0 && go mod tidy
# hosts on the s3 or otel integrations: also service/s3@v1.97.3 and the otlptrace exporters @v1.45.0

# 3. prove it
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

Then add the same `govulncheck` step to the host's CI and keep it required. When the framework
tags carrying these pins exist, repin to them as usual; until then the three steps above are
complete on their own.
