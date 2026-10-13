package fetch

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/go-retryablehttp"

	"github.com/metatube-community/metatube-sdk-go/common/random"
	"github.com/metatube-community/metatube-sdk-go/errors"
	"github.com/metatube-community/metatube-sdk-go/internal/requestpolicy"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

var DefaultFetcher = Default(&Config{RandomUserAgent: true})

type Config struct {
	// Set User-Agent Header.
	UserAgent string

	// Set Referer Header.
	Referer string

	// Enable cookies.
	EnableCookies bool

	// Use random User-Agent.
	RandomUserAgent bool

	// Return error when status is not OK.
	RaiseForStatus bool

	// HTTP Request timeout.
	Timeout time.Duration

	// Custom HTTP Transport.
	Transport http.RoundTripper

	// Skip TLS verification. Applies only
	// to *http.Transport based transport.
	SkipVerify bool
}

type Fetcher struct {
	client      *http.Client
	config      *Config
	retryClient *retryablehttp.Client
	policy      *requestpolicy.Governor
}

func New(c *http.Client, cfg *Config) *Fetcher {
	if cfg.RandomUserAgent {
		// assign a random user-agent.
		cfg.UserAgent = random.UserAgent()
	}
	if cfg.EnableCookies {
		jar, _ := cookiejar.New(nil)
		c.Jar = jar // assign a cookie jar.
	}
	return &Fetcher{
		client: c,
		config: cfg,
	}
}

func Default(cfg *Config) *Fetcher {
	if cfg == nil /* init if nil */ {
		cfg = new(Config)
	}
	// Enable status check by default.
	cfg.RaiseForStatus = true
	// Enable random UA if not set.
	if cfg.UserAgent == "" {
		cfg.RandomUserAgent = true
	}
	c := &retryablehttp.Client{
		HTTPClient:   cleanhttp.DefaultPooledClient(),
		RetryWaitMin: 1 * time.Second,
		RetryWaitMax: 3 * time.Second,
		RetryMax:     3,
		CheckRetry:   retryablehttp.DefaultRetryPolicy,
		Backoff:      retryablehttp.DefaultBackoff,
	}
	if cfg.Timeout > time.Second {
		c.HTTPClient.Timeout = cfg.Timeout
	}
	if cfg.Transport != nil {
		c.HTTPClient.Transport = cfg.Transport
	}
	if cfg.SkipVerify {
		if transport, ok := c.HTTPClient.Transport.(*http.Transport); ok {
			if transport.TLSClientConfig == nil {
				// init TLS config if is nil.
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
	f := New(c.StandardClient(), cfg)
	f.retryClient = c
	return f
}

// ConfigurePolicy attaches governance outside retryablehttp, then disables
// retryablehttp's own retries so each attempt is governed exactly once.
// Configure this during initialization, before concurrent requests begin.
func (f *Fetcher) ConfigurePolicy(cfg provider.RequestPolicyConfig) error {
	if cfg.Scope == "" {
		return fmt.Errorf("fetch: request policy scope required")
	}
	gov, err := requestpolicy.Shared(cfg.Scope, requestpolicy.Config{
		MinInterval:       cfg.MinInterval,
		Jitter:            cfg.Jitter,
		MaxConcurrency:    cfg.MaxConcurrency,
		MaxWait:           cfg.MaxWait,
		RateLimitCooldown: cfg.RateLimitCooldown,
		ForbiddenCooldown: cfg.ForbiddenCooldown,
		MaxAttempts:       cfg.MaxAttempts,
	})
	if err != nil {
		return err
	}
	if f.policy != nil {
		if f.policy == gov {
			return nil
		}
		return fmt.Errorf("fetch: cannot change an installed request policy")
	}
	if f.retryClient != nil {
		f.retryClient.RetryMax = 0
	}
	f.client.Transport = requestpolicy.Wrap(f.client.Transport, gov)
	f.policy = gov
	return nil
}

// ConfigureProxy retains the existing transport and applies the proxy at its
// innermost network layer, without bypassing the governance wrapper.
func (f *Fetcher) ConfigureProxy(proxyURL string) error {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	var t http.RoundTripper
	if f.retryClient != nil {
		t = f.retryClient.HTTPClient.Transport
	} else {
		t = f.client.Transport
	}
	transport, ok := t.(*http.Transport)
	if !ok {
		return fmt.Errorf("fetch: proxy requires an http.Transport")
	}
	transport.Proxy = http.ProxyURL(u)
	return nil
}

func (f *Fetcher) ConfigureTimeout(timeout time.Duration) {
	f.client.Timeout = timeout
}

func (f *Fetcher) Fetch(url string) (resp *http.Response, err error) {
	return f.Get(url)
}

func (f *Fetcher) Get(url string, opts ...Option) (resp *http.Response, err error) {
	return f.Request(http.MethodGet, url, nil, opts...)
}

func (f *Fetcher) Post(url string, body io.Reader, opts ...Option) (resp *http.Response, err error) {
	return f.Request(http.MethodPost, url, body, opts...)
}

func (f *Fetcher) Request(method, url string, body io.Reader, opts ...Option) (resp *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequest(method, url, body); err != nil {
		return
	}
	c := &Context{
		req:    req,
		Config: *f.config, /* clone */
	}
	// compose options.
	var options []Option
	if c.UserAgent != "" {
		options = append(options, WithUserAgent(c.UserAgent))
	}
	if c.Referer != "" {
		options = append(options, WithReferer(c.Referer))
	}
	// apply options.
	for _, option := range append(options, opts...) {
		option.apply(c)
	}
	// make HTTP request.
	if resp, err = f.client.Do(req); err != nil {
		return
	}
	if c.RaiseForStatus && resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, errors.FromCode(resp.StatusCode)
	}
	return
}

func Fetch(url string) (*http.Response, error) {
	return DefaultFetcher.Fetch(url)
}

func Get(url string, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Get(url, opts...)
}

func Post(url string, body io.Reader, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Post(url, body, opts...)
}

func Request(method, url string, body io.Reader, opts ...Option) (*http.Response, error) {
	return DefaultFetcher.Request(method, url, body, opts...)
}

var (
	_ = Fetch
	_ = Get
	_ = Post
	_ = Request
)
