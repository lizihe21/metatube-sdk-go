# JavLibrary and JavDB providers (G3/G4)

## Scope

Both providers are registered as normal movie providers and implement
`MovieProvider`, `MovieSearcher`, `Fetcher`, `ProxySetter`,
`RequestTimeoutSetter`, and `RequestPolicySetter`. No extra dependency
is required beyond the existing Go module.

They retrieve *public HTML metadata* only: one search result page and
one detail page on demand. They do not crawl actors, paginate catalogs,
download media, attempt login, solve CAPTCHA, or bypass any access control.

### Source identity

- JavLibrary's movie ID is the site's opaque `v` token (for example
  `javliAB12`), not its public movie number. Both `?v=token` and
  `token.html` detail URLs are recognized.
- JavDB's movie ID is the opaque `/v/token` path component. IDs are
  case-sensitive and are not forcibly uppercased.
- Searching by a movie number resolves a detail page **only through an
  exact normalized number match**; a fuzzy first result is not selected.
- Unrecognized page structures and verification/login pages are errors,
  not legitimate empty search results.
- All metadata redirects must stay on the configured site origin.
  Cross-origin HTML links are rejected before any outbound request.

### Default request governance

The conservative per-process defaults are deliberately low. These are
application-chosen starting values, **not published site quotas or
guarantees against rate limiting**.

| Setting | JavLibrary | JavDB |
|---|---:|---:|
| Min. outbound start spacing | 5s | 8s |
| Additional start jitter | 2s | 2s |
| Max concurrent in-flight requests | 1 | 1 |
| Max queue wait | 20s | 25s |
| Fallback 429 cooldown | 90s | 90s |
| 403 cooldown | 5m | 5m |
| Total attempts | 1 | 1 |

429 `Retry-After` wins if it is longer than the fallback. Challenges,
login/403 errors and malformed HTML never trigger bypasses or automated
high-rate retries. Multiple providers targeting the same scope share a
budget within the **same process only**.

### Environment overrides

The existing Engine configuration loader recognizes the following
per-provider environment variables (all are optional):

```dotenv
MT_PROVIDER_JAVLIBRARY__MIN_INTERVAL=8s
MT_PROVIDER_JAVLIBRARY__MAX_CONCURRENCY=1
MT_PROVIDER_JAVLIBRARY__MAX_WAIT=30s
MT_PROVIDER_JAVLIBRARY__RATE_LIMIT_COOLDOWN=2m
MT_PROVIDER_JAVLIBRARY__FORBIDDEN_COOLDOWN=5m
MT_PROVIDER_JAVLIBRARY__MAX_ATTEMPTS=1
MT_PROVIDER_JAVLIBRARY__PROXY=http://127.0.0.1:7890

MT_PROVIDER_JAVDB__MIN_INTERVAL=10s
MT_PROVIDER_JAVDB__JITTER=3s
MT_PROVIDER_JAVDB__MAX_CONCURRENCY=1
MT_PROVIDER_JAVDB__MAX_WAIT=35s
MT_PROVIDER_JAVDB__RATE_LIMIT_COOLDOWN=2m
MT_PROVIDER_JAVDB__MAX_ATTEMPTS=1
```

Zero-valued optional policy fields inherit each provider's conservative
defaults. An explicit `MAX_ATTEMPTS=1` disables retries beyond the
initial request. Set `MT_PROVIDER_JAVLIBRARY__PRIORITY=0` or
`MT_PROVIDER_JAVDB__PRIORITY=0` to disable either provider. If both
use the same configured origin, policy configurations must match.

### Behavior and testing

Public API behavior remains unchanged:
`SearchMovie`, `GetMovieInfoByID` and `GetMovieInfoByURL` still use
the original signatures. The new providers additionally expose Context
variants to support caller cancellation.

Fixture-driven tests are in
`provider/javlibrary/*_test.go`,
`provider/javdb/*_test.go`, and
`provider/internal/sitehtml/client_test.go`.

Run before merging:

```shell
go test ./provider/javlibrary ./provider/javdb ./provider/internal/sitehtml
go test -race ./provider/javlibrary ./provider/javdb ./provider/internal/sitehtml
go test ./...
go vet ./...
```

The target sites blocked automated direct inspection during development.
Parsing selectors were cross-checked against independent open-source
implementations and synthetic offline fixtures, **not verified against
the live HTML delivered to this deployment**. A deliberately low-rate,
operator-authorized live smoke test is still necessary before production
use. When a site's markup changes, update a fixture and its pure parser
rather than loosening error classification to treat every unknown page
as a valid absence of results.

## Reference implementations for selectors

- [Javinizer-Go JavLibrary scraper](https://github.com/javinizer/javinizer-go/tree/main/internal/scraper/javlibrary)
- [Javinizer-Go JavDB scraper](https://github.com/javinizer/javinizer-go/tree/main/internal/scraper/javdb)
- [JavBoss browser-extension parser tests](https://github.com/Solr159/JavBoss/tree/main/browser-extension/tests)

Those projects are used only as independent structural references; no
source code is copied into the new implementations.
