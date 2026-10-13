package engine

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

func (e *Engine) init() *Engine {
	e.initLogger()
	e.initFetcher()
	e.initActorProviders()
	e.initMovieProviders()
	return e
}

func (e *Engine) initLogger() {
	e.logger = log.New(os.Stdout, "[ENGINE]\u0020", log.LstdFlags|log.Llongfile)
}

func (e *Engine) initFetcher() {
	e.fetcher = fetch.Default(&fetch.Config{Timeout: e.timeout})
}

// initActorProviders initializes actor providers.
func (e *Engine) initActorProviders() {
	for name, factory := range mt.RangeActorFactory {
		provider := factory()

		if s, ok := provider.(mt.RequestTimeoutSetter); ok {
			s.SetRequestTimeout(e.timeout /* global timeout */)
		}

		if config, hasConfig := e.actorProviderConfigs.Get(name); hasConfig {
			e.applyProviderConfig("actor", provider, config)
		}

		if provider.Priority() <= 0 {
			e.logger.Printf("Disable actor provider: %s", provider.Name())
			continue
		}

		// Add actor provider by name.
		e.actorProviders.Set(name, provider)
		// Add actor provider by host.
		host := provider.URL().Hostname()
		e.actorHostProviders.Set(host,
			append(e.actorHostProviders.
				GetOrDefault(host, nil), provider))
	}
}

// initMovieProviders initializes movie providers.
func (e *Engine) initMovieProviders() {
	for name, factory := range mt.RangeMovieFactory {
		provider := factory()

		if s, ok := provider.(mt.RequestTimeoutSetter); ok {
			s.SetRequestTimeout(e.timeout /* global timeout */)
		}

		if config, hasConfig := e.movieProviderConfigs.Get(name); hasConfig {
			e.applyProviderConfig("movie", provider, config)
		}

		if provider.Priority() <= 0 {
			e.logger.Printf("Disable movie provider: %s", provider.Name())
			continue
		}

		// Add movie provider by name.
		e.movieProviders.Set(name, provider)
		// Add movie provider by host.
		host := provider.URL().Hostname()
		e.movieHostProviders.Set(host,
			append(e.movieHostProviders.
				GetOrDefault(host, nil), provider))
	}
}

func (e *Engine) applyProviderConfig(providerType string, provider mt.Provider, config mt.Config) {
	const (
		proxyConfigKey    = "proxy"
		priorityConfigKey = "priority"
		timeoutConfigKey  = "timeout"
	)

	// Apply overridden priority.
	if config.Has(priorityConfigKey) {
		if v, err := config.GetFloat64(priorityConfigKey); err == nil {
			e.logger.Printf("Override %s provider priority: %s=%.2f", providerType, provider.Name(), v)
			provider.SetPriority(v)
		}
	}

	// Apply overridden proxy.
	if s, ok := provider.(mt.ProxySetter); ok && config.Has(proxyConfigKey) {
		if v, err := config.GetString(proxyConfigKey); err == nil {
			if err := s.SetProxy(v); err != nil {
				e.logger.Printf("Set proxy for %s provider %s failed: %v", providerType, provider.Name(), err)
					provider.SetPriority(0)
					return
			}
			e.logger.Printf("Override %s provider proxy: %s (credentials redacted)", providerType, provider.Name())
		}
	}

	// Apply request timeout.
	if s, ok := provider.(mt.RequestTimeoutSetter); ok && config.Has(timeoutConfigKey) {
		if v, err := config.GetDuration(timeoutConfigKey); err == nil {
			e.logger.Printf("Override %s provider request timeout: %s=%s", providerType, provider.Name(), v)
			s.SetRequestTimeout(v)
		}
	}

	// Opt-in policy configuration, applied after proxy/timeout so the
	// transport wrapper remains the final outbound HTTP gate.
	if hasRequestPolicyConfig(config) {
		setter, ok := provider.(mt.RequestPolicySetter)
		if !ok {
			e.logger.Printf("Disable %s provider %s: request policy unsupported", providerType, provider.Name())
			provider.SetPriority(0)
			return
		}
		policy, err := readRequestPolicyConfig(config)
		if err == nil {
			err = setter.SetRequestPolicy(policy)
		}
		if err != nil {
			e.logger.Printf("Disable %s provider %s: invalid request policy: %v", providerType, provider.Name(), err)
			provider.SetPriority(0)
			return
		}
	}

	// Apply full config.
	if s, ok := provider.(mt.ConfigSetter); ok {
		if err := s.SetConfig(config); err != nil {
			e.logger.Printf("Disable %s provider %s: invalid provider config: %v", providerType, provider.Name(), err)
				provider.SetPriority(0)
				return
		}
	}
}

var requestPolicyKeys = []string{
	"min_interval", "jitter", "max_concurrency", "max_wait",
	"rate_limit_cooldown", "forbidden_cooldown", "max_attempts",
}

func hasRequestPolicyConfig(c mt.Config) bool {
	for _, key := range requestPolicyKeys {
		if c.Has(key) {
			return true
		}
	}
	return false
}

func readRequestPolicyConfig(c mt.Config) (mt.RequestPolicyConfig, error) {
	var result mt.RequestPolicyConfig
	for _, field := range []struct {
		name   string
		target *time.Duration
	}{
		{"min_interval", &result.MinInterval},
		{"jitter", &result.Jitter},
		{"max_wait", &result.MaxWait},
		{"rate_limit_cooldown", &result.RateLimitCooldown},
		{"forbidden_cooldown", &result.ForbiddenCooldown},
	} {
		if !c.Has(field.name) {
			continue
		}
		value, err := c.GetDuration(field.name)
		if err != nil {
			return result, fmt.Errorf("%s: %w", field.name, err)
		}
		*field.target = value
	}
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"max_concurrency", &result.MaxConcurrency},
		{"max_attempts", &result.MaxAttempts},
	} {
		if !c.Has(field.name) {
			continue
		}
		value, err := c.GetInt64(field.name)
		if err != nil || value < 0 || value > 100000 {
			return result, fmt.Errorf("%s: invalid integer value", field.name)
		}
		*field.target = int(value)
	}
	return result, nil
}
