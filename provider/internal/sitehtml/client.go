// Package sitehtml provides guarded HTML fetching for site-specific parsers.
// When configured, it delegates verification challenges to an already running
// external FlareSolverr HTTP service; no browser is started within this SDK.
package sitehtml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/internal/flaresolverr"
	"github.com/metatube-community/metatube-sdk-go/internal/requestpolicy"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

const maxRedirects = 2

// Client owns independent parsing/metadata and media HTTP stacks, but configures
// both against one shared, process-local request budget before first use.
type Client struct {
	*scraper.Scraper
	*fetch.Fetcher
	mu                sync.Mutex
	configured        bool
	defaults          provider.RequestPolicyConfig
	policyConfig      provider.RequestPolicyConfig
	governor          *requestpolicy.Governor
	solver            *flaresolverr.Client
	solverMode        string
	preferSolverUntil time.Time
	userAgent         string
}

// New does not send requests. Policies are installed lazily so Engine's
// per-provider configuration has priority over the conservative defaults.
func New(name, base string, priority float64, lang language.Tag, defaults provider.RequestPolicyConfig) *Client {
	return &Client{
		Scraper: scraper.NewDefaultScraper(name, base, priority, lang,
			scraper.WithDisableRedirects(),
			scraper.WithDetectCharset(),
			scraper.WithHeaders(map[string]string{
				"Accept": "text/html,application/xhtml+xml",
			}),
		),
		Fetcher: fetch.Default(&fetch.Config{
			Referer: base,
			Timeout: 35 * time.Second,
		}),
		defaults: defaults,
	}
}

func (c *Client) applyPolicyLocked(cfg provider.RequestPolicyConfig) error {
	if c.configured {
		return fmt.Errorf("sitehtml: request policy already installed")
	}
	if cfg.Scope == "" {
		cfg.Scope = c.URL().Host
	}
	if cfg.MinInterval == 0 {
		cfg.MinInterval = c.defaults.MinInterval
	}
	if cfg.Jitter == 0 {
		cfg.Jitter = c.defaults.Jitter
	}
	if cfg.MaxConcurrency == 0 {
		cfg.MaxConcurrency = c.defaults.MaxConcurrency
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = c.defaults.MaxWait
	}
	if cfg.RateLimitCooldown == 0 {
		cfg.RateLimitCooldown = c.defaults.RateLimitCooldown
	}
	if cfg.ForbiddenCooldown == 0 {
		cfg.ForbiddenCooldown = c.defaults.ForbiddenCooldown
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = c.defaults.MaxAttempts
	}
	gov, err := requestpolicy.Shared(cfg.Scope, requestpolicy.Config{
		MinInterval: cfg.MinInterval,
		Jitter: cfg.Jitter,
		MaxConcurrency: cfg.MaxConcurrency,
		MaxWait: cfg.MaxWait,
		RateLimitCooldown: cfg.RateLimitCooldown,
		ForbiddenCooldown: cfg.ForbiddenCooldown,
		MaxAttempts: cfg.MaxAttempts,
	})
	if err != nil {
		return err
	}
	if err := c.Scraper.SetRequestPolicy(cfg); err != nil {
		return err
	}
	if err := c.ConfigurePolicy(cfg); err != nil {
		return err
	}
	c.governor = gov
	c.policyConfig = cfg
	c.configured = true
	return nil
}

// SetRequestPolicy overrides defaults during initialization. Call before any
// requests; changing transport settings while active is not supported.
func (c *Client) SetRequestPolicy(cfg provider.RequestPolicyConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applyPolicyLocked(cfg)
}

func (c *Client) ensurePolicy() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.configured {
		return nil
	}
	return c.applyPolicyLocked(provider.RequestPolicyConfig{})
}

// SetProxy applies the same proxy to scraper and media fetcher.
func (c *Client) SetProxy(proxyURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.configured {
		return fmt.Errorf("sitehtml: configure proxy before first request")
	}
	if err := c.Scraper.SetProxy(proxyURL); err != nil {
		return err
	}
	return c.ConfigureProxy(proxyURL)
}

// SetConfig enables an optional external FlareSolverr HTTP API. It is
// promoted to JavLibrary and JavDB via the embedded Client and therefore
// picked up by Engine's existing per-provider environment configuration.
func (c *Client) SetConfig(config provider.Config) error {
	if !config.Has("flaresolverr_url") {
		if config.Has("flaresolverr_mode") || config.Has("flaresolverr_timeout") {
			return fmt.Errorf("sitehtml: flaresolverr_url is required when other solver settings are set")
		}
		return nil
	}
	endpoint, err := config.GetString("flaresolverr_url")
	if err != nil {
		return err
	}
	mode := "fallback"
	if config.Has("flaresolverr_mode") {
		mode, err = config.GetString("flaresolverr_mode")
		if err != nil {
			return err
		}
		mode = strings.ToLower(strings.TrimSpace(mode))
	}
	if mode != "fallback" && mode != "always" {
		return fmt.Errorf("sitehtml: flaresolverr_mode must be fallback or always")
	}
	timeout := time.Minute
	if config.Has("flaresolverr_timeout") {
		timeout, err = config.GetDuration("flaresolverr_timeout")
		if err != nil {
			return fmt.Errorf("sitehtml: invalid flaresolverr_timeout: %w", err)
		}
	}
	// Validate before modifying either transport or request policy.
	if _, err := flaresolverr.New(endpoint, timeout, nil); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.solver != nil {
		return fmt.Errorf("sitehtml: FlareSolverr is already configured")
	}
	if !c.configured {
		if err := c.applyPolicyLocked(provider.RequestPolicyConfig{}); err != nil {
			return err
		}
	}
	// Browser requests use an independent site-scoped queue because an
	// HTTP 403 from the direct path has already put its transport in cooldown.
	// The two paths cannot share that cooldown without blocking the fallback.
	solverGovernor, err := requestpolicy.Shared("flaresolverr:"+c.Scraper.URL().Host,
		requestpolicy.Config{
			MinInterval:    c.policyConfig.MinInterval,
			Jitter:         c.policyConfig.Jitter,
			MaxConcurrency: 1,
			MaxWait:        2 * timeout,
			MaxAttempts:    1,
		})
	if err != nil {
		return err
	}
	transport := requestpolicy.Wrap(http.DefaultTransport.(*http.Transport).Clone(), solverGovernor)
	api, err := flaresolverr.New(endpoint, timeout, transport)
	if err != nil {
		return err
	}
	c.solver = api
	c.solverMode = mode
	return nil
}

func (c *Client) preferSolver() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.solver != nil && (c.solverMode == "always" || time.Now().Before(c.preferSolverUntil))
}

func (c *Client) hasSolver() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.solver != nil
}

// getWithSolver performs exactly one remote request.get and accepts only a
// successful same-origin response. FlareSolverr is an external Docker service;
// no browser process or session lifecycle is managed by this SDK.
func (c *Client) getWithSolver(ctx context.Context, target string) (*goquery.Document, string, error) {
	c.mu.Lock()
	api := c.solver
	c.mu.Unlock()
	if api == nil {
		return nil, "", provider.ErrChallengeRequired
	}
	result, err := api.Get(ctx, target)
	if err != nil {
		if errors.Is(err, flaresolverr.ErrUnsolved) {
			c.governor.ObserveResponse(http.StatusForbidden, nil)
			return nil, "", provider.ErrChallengeRequired
		}
		return nil, "", fmt.Errorf("sitehtml: FlareSolverr request failed: %w", err)
	}
	if !c.ValidURL(result.URL) {
		return nil, "", provider.ErrInvalidURL
	}
	if isChallengePage([]byte(result.HTML)) {
		c.governor.ObserveResponse(http.StatusForbidden, nil)
		return nil, "", provider.ErrChallengeRequired
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result.HTML))
	if err != nil {
		return nil, "", fmt.Errorf("sitehtml: invalid FlareSolverr HTML: %w", err)
	}
	if doc.Find("form[action*='login'], form[action*='sign_in']").Length() > 0 &&
		doc.Find("#video_info, .movie-list, .movie-panel-info").Length() == 0 {
		return nil, "", provider.ErrAccessDenied
	}
	// Browser clearance cookies and User-Agent are a matching pair. Save only
	// cookies scoped to this source host; never import unrelated domains.
	hostname := c.Scraper.URL().Hostname()
	httpCookies := make([]*http.Cookie, 0, len(result.Cookies))
	for _, cookie := range result.Cookies {
		if cookie.Name == "" {
			continue
		}
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if domain != "" && hostname != domain && !strings.HasSuffix(hostname, "."+domain) {
			continue
		}
		p := cookie.Path
		if p == "" {
			p = "/"
		}
		hc := &http.Cookie{
			Name: cookie.Name, Value: cookie.Value, Path: p,
			Domain: cookie.Domain, Secure: cookie.Secure, HttpOnly: cookie.HTTPOnly,
		}
		if cookie.Expires > 0 && cookie.Expires < 1e12 {
			hc.Expires = time.Unix(int64(cookie.Expires), 0)
		}
		httpCookies = append(httpCookies, hc)
	}
	if len(httpCookies) > 0 {
		_ = c.Scraper.ClonedCollector().SetCookies(result.URL, httpCookies)
	}
	c.mu.Lock()
	if result.UserAgent != "" {
		c.userAgent = result.UserAgent
	}
	// A direct 403 may have imposed a five-minute cooldown on the original
	// governor. During that interval use the separate, rate-limited API path.
	if time.Now().After(c.preferSolverUntil) {
		cooldown := c.policyConfig.ForbiddenCooldown
		if cooldown < 30*time.Second {
			cooldown = 30 * time.Second
		}
		c.preferSolverUntil = time.Now().Add(cooldown)
	}
	c.mu.Unlock()
	return doc, result.URL, nil
}

// SetRequestTimeout applies request timeout to both HTTP stacks.
func (c *Client) SetRequestTimeout(timeout time.Duration) {
	c.Scraper.SetRequestTimeout(timeout)
	c.ConfigureTimeout(timeout)
}

// Fetch fulfills provider.Fetcher for associated media downloads.
func (c *Client) Fetch(rawURL string) (*http.Response, error) {
	if err := c.ensurePolicy(); err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, provider.ErrInvalidURL
	}
	return c.Fetcher.Fetch(rawURL)
}

// ValidURL accepts only this client's configured scheme and origin. This also
// prevents external links in scraped HTML from causing arbitrary HTTP fetches.
func (c *Client) ValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil {
		return false
	}
	base := c.Scraper.URL()
	return u.IsAbs() && u.Scheme == base.Scheme &&
		strings.EqualFold(u.Host, base.Host) &&
		(u.Scheme == "http" || u.Scheme == "https")
}

// ResolveURL resolves an href against a page only if it stays on this origin.
func (c *Client) ResolveURL(page, href string) (string, error) {
	base, err := url.Parse(page)
	if err != nil || !c.ValidURL(page) {
		return "", provider.ErrInvalidURL
	}
	relative, err := url.Parse(strings.TrimSpace(href))
	if err != nil || relative.User != nil {
		return "", provider.ErrInvalidURL
	}
	next := base.ResolveReference(relative)
	next.Fragment = ""
	if !c.ValidURL(next.String()) {
		return "", provider.ErrInvalidURL
	}
	return next.String(), nil
}

func isChallengePage(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "cf-chl-") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "challenge-form") ||
		strings.Contains(lower, "just a moment") ||
		strings.Contains(lower, "cf-turnstile-response") ||
		strings.Contains(lower, "checking your browser")
}

// Get downloads one same-origin HTML page with bounded, explicit redirects.
// On each redirect a new Colly clone is used, so network callbacks are never
// accidentally inherited or duplicated across cloned Collectors.
func (c *Client) Get(ctx context.Context, rawURL string) (*goquery.Document, string, error) {
	if ctx == nil {
		return nil, "", fmt.Errorf("sitehtml: nil context")
	}
	if !c.ValidURL(rawURL) {
		return nil, "", provider.ErrInvalidURL
	}
	if err := c.ensurePolicy(); err != nil {
		return nil, "", err
	}
	if c.preferSolver() {
		return c.getWithSolver(ctx, rawURL)
	}
	current := rawURL
	for hop := 0; hop <= maxRedirects; hop++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		coll := c.ClonedCollectorWithContext(ctx)
		c.mu.Lock()
		ua := c.userAgent
		c.mu.Unlock()
		if ua != "" {
			coll.UserAgent = ua
		}
		coll.ParseHTTPErrorResponse = true

		var status int
		var body []byte
		var headers http.Header
		coll.OnResponse(func(r *colly.Response) {
			status = r.StatusCode
			body = append([]byte(nil), r.Body...)
			if r.Headers != nil {
				headers = r.Headers.Clone()
			}
		})
		if err := coll.Visit(current); err != nil {
			return nil, "", fmt.Errorf("sitehtml: request failed: %w", err)
		}
		// A challenge can be returned as HTTP 200, 403, or 503.
		// Only recognized verification responses trigger the API fallback;
		// plain access denials never invoke the external browser.
		if status == http.StatusOK || status == http.StatusForbidden ||
			status == http.StatusServiceUnavailable {
			if isChallengePage(body) || strings.EqualFold(headers.Get("Cf-Mitigated"), "challenge") {
				if c.hasSolver() {
					return c.getWithSolver(ctx, current)
				}
				c.governor.ObserveResponse(http.StatusForbidden, nil)
				return nil, "", provider.ErrChallengeRequired
			}
		}
		switch {
		case status >= 300 && status < 400:
			location := headers.Get("Location")
			if location == "" {
				return nil, "", provider.ErrUpstreamUnavailable
			}
			next, err := c.ResolveURL(current, location)
			if err != nil {
				return nil, "", err
			}
			if strings.Contains(strings.ToLower(next), "/login") {
				return nil, "", provider.ErrAccessDenied
			}
			current = next
			continue
		case status == http.StatusNotFound:
			return nil, "", provider.ErrInfoNotFound
		case status == http.StatusTooManyRequests:
			return nil, "", provider.ErrRateLimited
		case status == http.StatusForbidden || status == http.StatusUnauthorized:
			return nil, "", provider.ErrAccessDenied
		case status >= 500:
			return nil, "", provider.ErrUpstreamUnavailable
		case status != http.StatusOK:
			return nil, "", provider.ErrUpstreamUnavailable
		}
		if len(body) == 0 {
			return nil, "", provider.ErrIncompleteMetadata
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return nil, "", fmt.Errorf("sitehtml: parse HTML: %w", err)
		}
		if doc.Find("form[action*='login'], form[action*='sign_in']").Length() > 0 &&
			doc.Find("#video_info, .movie-list, .movie-panel-info").Length() == 0 {
			c.governor.ObserveResponse(http.StatusForbidden, nil)
			return nil, "", provider.ErrAccessDenied
		}
		return doc, current, nil
	}
	return nil, "", provider.ErrUpstreamUnavailable
}
