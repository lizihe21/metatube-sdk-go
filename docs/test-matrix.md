# Test and release gates (G6/G7)

The default `go test ./...` run is **offline**: it uses deterministic Go
unit tests, synthetic HTML fixtures, and local `httptest.Server` instances.
No real JavLibrary, JavDB, FlareSolverr Docker, external translation API,
or PostgreSQL container is required for that command.

## Test classes

| Test class | Default `go test ./...` | Opt-in environment |
|---|---|---|
| Go unit tests, Go protocol parsers, local HTTP fixtures | **RUN** | None |
| All legacy provider network tests using `provider/internal/testkit` | **SKIP** | `METATUBE_LIVE_TEST=1` |
| JavLibrary / JavDB real search and detail smoke tests | **SKIP** | `METATUBE_LIVE_TEST=1`, `METATUBE_JAVLIBRARY_NUMBER` or `METATUBE_JAVDB_NUMBER` |
| External FlareSolverr Docker API live smoke test | **SKIP** | `METATUBE_LIVE_TEST=1`, `METATUBE_FLARESOLVERR_URL`, `METATUBE_FLARESOLVERR_TARGET` |
| FANZA GraphQL live API tests | **SKIP** | `METATUBE_LIVE_TEST=1` |
| Real-image comparison URL tests | **SKIP** | `METATUBE_LIVE_TEST=1` |
| Baidu, DeepL, Google and GoogleFree live translation tests | **SKIP** | `METATUBE_LIVE_TEST=1` |
| OpenAI live translation test | **SKIP** | `OPENAI_TRANSLATION_INTEGRATION=1` and `OPENAI_API_KEY` |
| SQLite in-memory DB engine suite | **RUN** | None |
| PostgreSQL Docker DB engine suite | **SKIP** | `METATUBE_DOCKER_TEST=1` |

Skips are made by the tests (using `t.Skip`), not by removing packages
from `go test ./...`. This means the skipped packages are still compiled
and vetted, and all offline tests in those packages continue to execute.
Unconfigured real-world integrations are not marked as passing.

## Offline verification

Use Go 1.26 as required by `go.mod`. From the repository root:

```sh
go version
go test ./...
go vet ./...
go test -race -count=2 \
  ./internal/requestpolicy \
  ./internal/flaresolverr \
  ./provider/internal/scraper \
  ./provider/internal/sitehtml \
  ./provider/javlibrary \
  ./provider/javdb \
  ./common/fetch \
  ./translate/openai \
  ./route \
  ./engine
```

The PR workflow `.github/workflows/test.yml` executes this offline gate.
It does **not** need Docker-in-Docker or live API keys. A passing exit
code is mandatory; don't infer passing CI from an absent workflow run.

## Explicit live smoke checks (not part of CI)

Enable one integration target at a time and keep per-site request budgets
conservative. Do not set `METATUBE_LIVE_TEST=1` for the entire
`go test ./...` suite.

```sh
# Legacy provider remote HTML/API test cases
METATUBE_LIVE_TEST=1 go test -v ./provider/javbus -run '^TestJavBus_'

# JavLibrary: enable only a known source number and (optionally) existing FlareSolverr
METATUBE_LIVE_TEST=1 METATUBE_JAVLIBRARY_NUMBER=YOUR_KNOWN_NUMBER \
  METATUBE_FLARESOLVERR_URL=http://flaresolverr:8191 \
  go test -v ./provider/javlibrary -run '^TestJavLibraryLiveSmoke$'

# JavDB: check an exact catalog-number search and detail pair
METATUBE_LIVE_TEST=1 METATUBE_JAVDB_NUMBER=YOUR_KNOWN_NUMBER \
  METATUBE_FLARESOLVERR_URL=http://flaresolverr:8191 \
  go test -v ./provider/javdb -run '^TestJavDBLiveSmoke$'

# Confirm the external Docker API is reachable on this host/container
METATUBE_LIVE_TEST=1 METATUBE_FLARESOLVERR_URL=http://flaresolverr:8191 \
  METATUBE_FLARESOLVERR_TARGET=https://javdb.com/ \
  go test -v ./internal/flaresolverr -run '^TestExternalFlareSolverrLiveSmoke$'

# Real OpenAI translation; requires a real API key and reachable endpoint
OPENAI_TRANSLATION_INTEGRATION=1 OPENAI_API_KEY=... \
  go test -v ./translate/openai -run '^TestOpenaiTranslate$'

# PostgreSQL Docker; this also runs the offline SQLite suite
METATUBE_DOCKER_TEST=1 go test -v ./engine/dbengine
```

JavLibrary/JavDB G3/G4 fixture tests run offline; G6 also provides
**explicitly gated** live smoke cases in `live_test.go`. They require known
source identifiers and a reachable deployment environment. If no real source
inputs are provided, these cases are skipped and must be recorded as
**DEFERRED**, not passed. When enabled, they check actual search/detail
round-trips and report challenges, rate limits, access denied and incomplete
metadata separately. They never create a Docker browser or container.

Before promotion to `main`: offline tests and lint should pass in an
actual Go 1.26 environment; GitHub CI must be observed running; and live
smoke coverage should be recorded explicitly as **passed**, **failed**,
or **not run (deferred)**. Do not present deferred tests as a pass.

## Release record template

```text
Revision / commit:
Go version:
Offline go test ./...: PASS / FAIL / NOT RUN
go vet ./...: PASS / FAIL / NOT RUN
Race-sensitive suites: PASS / FAIL / NOT RUN
Code formatting / linter: PASS / FAIL / NOT RUN
JavLibrary live: PASS / FAIL / DEFERRED
JavDB live: PASS / FAIL / DEFERRED
FlareSolverr live: PASS / FAIL / DEFERRED
OpenAI Chat JSON/SSE live: PASS / FAIL / DEFERRED
OpenAI Responses JSON/SSE live: PASS / FAIL / DEFERRED
PostgreSQL Docker: PASS / FAIL / DEFERRED
Production merge approval: pending / approved
```
