// Package flaresolverr speaks the HTTP API of an already running FlareSolverr
// service. It never starts or manages a local browser or Docker container.
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes int64 = 12 << 20

var (
	ErrInvalidEndpoint = errors.New("flaresolverr: invalid endpoint")
	ErrInvalidTarget   = errors.New("flaresolverr: invalid target URL")
	ErrService         = errors.New("flaresolverr: service returned an error")
	ErrProtocol        = errors.New("flaresolverr: invalid API response")
	ErrUnsolved        = errors.New("flaresolverr: target verification was not resolved")
)

// Cookie models fields returned by FlareSolverr in solution.cookies.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
}

// Result contains the complete HTML response and optional browser identity.
type Result struct {
	URL       string
	Status    int
	HTML      string
	UserAgent string
	Cookies   []Cookie
}

type Client struct {
	endpoint   string
	timeout    time.Duration
	httpClient *http.Client
}

// New accepts a Docker hostname endpoint such as http://flaresolverr:8191.
// A /v1 path is added when absent. The provided transport can enforce a
// separate shared request budget for browser-backed requests.
func New(endpoint string, timeout time.Duration, transport http.RoundTripper) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrInvalidEndpoint
	}
	if timeout < 0 || timeout > 3*time.Minute {
		return nil, fmt.Errorf("%w: timeout must be between 0 and 3 minutes", ErrInvalidEndpoint)
	}
	if timeout == 0 {
		timeout = time.Minute
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path += "/v1"
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		endpoint: u.String(),
		timeout:  timeout,
		httpClient: &http.Client{
			Transport: transport,
			// Includes the separately budgeted site queue wait (up to 2*timeout),
			// the remote solve (up to timeout), and network overhead.
			Timeout:       3*timeout + 15*time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

type apiRequest struct {
	Command    string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int64  `json:"maxTimeout"`
}

type apiResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL       string   `json:"url"`
		Status    int      `json:"status"`
		Response  string   `json:"response"`
		UserAgent string   `json:"userAgent"`
		Cookies   []Cookie `json:"cookies"`
	} `json:"solution"`
}

// Get invokes FlareSolverr's request.get once, without any hidden retries.
// The caller must independently validate that solution.url is on the intended
// source origin before trusting or parsing the returned HTML.
func (c *Client) Get(ctx context.Context, target string) (*Result, error) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, ErrInvalidTarget
	}
	data, err := json.Marshal(apiRequest{Command: "request.get", URL: target, MaxTimeout: c.timeout.Milliseconds()})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrService, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrProtocol, err)
	}
	if int64(len(raw)) > maxResponseBytes {
		return nil, fmt.Errorf("%w: response too large", ErrProtocol)
	}
	var result apiResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON", ErrProtocol)
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("%w: status=%q", ErrService, result.Status)
	}
	if result.Solution.Status != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnsolved, result.Solution.Status)
	}
	if result.Solution.URL == "" || result.Solution.Response == "" {
		return nil, ErrProtocol
	}
	return &Result{URL: result.Solution.URL, Status: result.Solution.Status, HTML: result.Solution.Response, UserAgent: result.Solution.UserAgent, Cookies: result.Solution.Cookies}, nil
}
