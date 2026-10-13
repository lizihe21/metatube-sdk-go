package scraper

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gocolly/colly/v2"
	"go.uber.org/atomic"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/internal/requestpolicy"
)

var (
	_ provider.Provider             = (*Scraper)(nil)
	_ provider.ProxySetter          = (*Scraper)(nil)
	_ provider.RequestTimeoutSetter = (*Scraper)(nil)
	_ provider.RequestPolicySetter = (*Scraper)(nil)
)

// Scraper implements the basic Provider interface.
type Scraper struct {
	name     string
	baseURL  *url.URL
	priority *atomic.Float64
	language language.Tag
	c        *colly.Collector
	baseTransport http.RoundTripper
	proxyURL string
	policy *requestpolicy.Governor
}

// NewScraper returns a *Scraper that implements provider.Provider.
func NewScraper(name, base string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	baseURL, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	s := &Scraper{
		name:     name,
		baseURL:  baseURL,
		priority: atomic.NewFloat64(priority),
		language: lang,
		c:        colly.NewCollector(),
	}
	for _, opt := range opts {
		// Apply options.
		if err := opt(s); err != nil {
			panic(err)
		}
	}
	return s
}

// NewDefaultScraper returns a *Scraper with default options enabled.
func NewDefaultScraper(name, baseURL string, priority float64, lang language.Tag, opts ...Option) *Scraper {
	return NewScraper(name, baseURL, priority, lang, append([]Option{
		WithAllowURLRevisit(),
		WithIgnoreRobotsTxt(),
		WithRandomUserAgent(),
	}, opts...)...)
}

func (s *Scraper) Name() string { return s.name }

func (s *Scraper) URL() *url.URL { return s.baseURL }

func (s *Scraper) Priority() float64 { return s.priority.Load() }

func (s *Scraper) SetPriority(v float64) { s.priority.Store(v) }

func (s *Scraper) Language() language.Tag { return s.language }

func (s *Scraper) NormalizeMovieID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseMovieIDFromURL(string) (string, error) { panic("unimplemented") }

func (s *Scraper) NormalizeActorID(id string) string { return id /* AS IS */ }

func (s *Scraper) ParseActorIDFromURL(string) (string, error) { panic("unimplemented") }

// ClonedCollector returns cloned internal collector.
func (s *Scraper) ClonedCollector() *colly.Collector { return s.c.Clone() }

// SetProxy sets http or socks5 proxy for HTTP requests.
func (s *Scraper) SetProxy(proxyURL string) error {
	if _, err := url.Parse(proxyURL); err != nil { return err }
	if s.policy == nil {
		if err := s.c.SetProxy(proxyURL); err != nil { return err }
		s.proxyURL = proxyURL
		return nil
	}
	previous := s.proxyURL
	s.proxyURL = proxyURL
	if err := s.installPolicyTransport(); err != nil {
		s.proxyURL = previous
		return err
	}
	return nil
}

// SetRequestPolicy installs governance in the Colly transport, which is
// shared by cloned Collectors. Do not change policy during active requests.
func (s *Scraper) SetRequestPolicy(cfg provider.RequestPolicyConfig) error {
	if cfg.Scope == "" { cfg.Scope = s.baseURL.Hostname() }
	gov, err := requestpolicy.Shared(cfg.Scope, requestpolicy.Config{
		MinInterval:cfg.MinInterval, Jitter:cfg.Jitter,
		MaxConcurrency:cfg.MaxConcurrency, MaxWait:cfg.MaxWait,
		RateLimitCooldown:cfg.RateLimitCooldown,
		ForbiddenCooldown:cfg.ForbiddenCooldown,
		MaxAttempts:cfg.MaxAttempts,
	})
	if err != nil { return err }
	if s.policy != nil && s.policy != gov {
		return fmt.Errorf("scraper: cannot change an installed request policy")
	}
	s.policy = gov
	return s.installPolicyTransport()
}

func (s *Scraper) installPolicyTransport() error {
	base := s.baseTransport
	if base == nil { base = http.DefaultTransport.(*http.Transport).Clone() }
	if s.proxyURL != "" {
		t, ok := base.(*http.Transport)
		if !ok { return fmt.Errorf("scraper: configured custom transport does not support proxy override") }
		u, err := url.Parse(s.proxyURL)
		if err != nil { return err }
		cloned := t.Clone()
		cloned.Proxy = http.ProxyURL(u)
		base = cloned
	}
	s.c.WithTransport(requestpolicy.Wrap(base, s.policy))
	return nil
}

// SetRequestTimeout sets timeout for HTTP requests.
func (s *Scraper) SetRequestTimeout(timeout time.Duration) { s.c.SetRequestTimeout(timeout) }
