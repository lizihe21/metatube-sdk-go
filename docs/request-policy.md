# Provider request policy (G2)

This version adds **opt-in**, per-site outbound HTTP request governance to
providers implementing `provider.RequestPolicySetter`. Existing providers
without configuration keep their previous behavior.

## Configuration

For the JavBus provider, the Colly metadata scraper **and** the separate
media fetcher share a single process-local budget.

```dotenv
MT_PROVIDER_JAVBUS__MIN_INTERVAL=5s
MT_PROVIDER_JAVBUS__JITTER=2s
MT_PROVIDER_JAVBUS__MAX_CONCURRENCY=1
MT_PROVIDER_JAVBUS__MAX_WAIT=12s
MT_PROVIDER_JAVBUS__RATE_LIMIT_COOLDOWN=30s
MT_PROVIDER_JAVBUS__FORBIDDEN_COOLDOWN=1m
MT_PROVIDER_JAVBUS__MAX_ATTEMPTS=2
```

The same keys can be set through `MT_MOVIE_PROVIDER_<NAME>__...` where
supported by the existing provider environment configuration system.
Duration values follow Go's `time.ParseDuration` grammar.

When any request-policy key is set but the provider does not implement
`RequestPolicySetter`, or when a value cannot be parsed, the Engine
disables that provider rather than silently running unrestricted. A policy
is shared by hostname (unless the implementing provider supplies another
scope), so independent providers on the same site must use **matching**
policy values.

## Behavior and limits

- All HTTP attempts under a configured transport pass through one
  concurrency semaphore and a minimum start-spacing gate.
- The gate respects Context cancellation and a bounded queue wait.
  The default wait ceiling is 15 seconds.
- HTTP 429 sets a shared cooldown. The longer of the configured fallback
  cooldown and a valid `Retry-After` header is honored.
- HTTP 403 can start an optional cooldown; neither 403 nor authentication
  failures are automatically retried by the policy.
- GET/HEAD requests may retry 429/502/503/504 within a maximum of three
  total attempts. Default is **one** attempt. Network errors are not
  automatically retried. Intermediate bodies are closed before retry.
- When governance is enabled on the shared Fetcher, legacy
  `retryablehttp` automatic retries are disabled so retries do not
  multiply behind the governor.
- Response bodies hold a concurrency slot until EOF or Close; callers
  must close all response bodies.

**Scope:** Policies are shared only inside one Go process. Separate
containers require deployment-level quotas or an external coordinator.
This feature does not bypass access restrictions, CAPTCHA, or login
requirements. Successful HTTP 200 challenge pages still require
site-specific classification by the Provider; they are not a valid
empty-search result.

The policy can be configured only at initialization, before requests are
running. Do not dynamically mutate transport/proxy settings while active
requests are underway.
