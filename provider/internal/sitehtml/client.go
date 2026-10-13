// Package sitehtml provides a guarded HTML fetcher for site-specific parsers.
// It deliberately does not solve CAPTCHAs or bypass source access controls.
package sitehtml

import (
	"bytes"
	"context"
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
	mu         sync.Mutex
	configured bool
	defaults   provider.RequestPolicyConfig
	governor   *requestpolicy.Governor
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
		cfg.Scope = c.Scraper.URL().Host
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
	if err := c.Fetcher.ConfigurePolicy(cfg); err != nil {
		return err
	}
	c.governor = gov
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
	return c.Fetcher.ConfigureProxy(proxyURL)
}

// SetRequestTimeout applies request timeout to both HTTP stacks.
func (c *Client) SetRequestTimeout(timeout time.Duration) {
	c.Scraper.SetRequestTimeout(timeout)
	c.Fetcher.ConfigureTimeout(timeout)
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
	current := rawURL
	for hop := 0; hop <= maxRedirects; hop++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		coll := c.Scraper.ClonedCollectorWithContext(ctx)
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
		if isChallengePage(body) {
			// Verification pages can be HTTP 200; they must still engage
			// the same shared cooldown as an explicit access denial.
			c.governor.ObserveResponse(http.StatusForbidden, nil)
			return nil, "", provider.ErrChallengeRequired
		}
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return nil, "", fmt.Errorf("sitehtml: parse HTML: %w", err)
		}
		if doc.Find("form[action*='login'], form[action*='sign_in']").Length() > 0 {
			return nil, "", provider.ErrAccessDenied
		}
		return doc, current, nil
	}
	return nil, "", provider.ErrUpstreamUnavailable
}
