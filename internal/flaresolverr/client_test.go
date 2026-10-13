package flaresolverr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestGetProtocolAndResult(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		var q struct {
			Cmd        string `json:"cmd"`
			URL        string `json:"url"`
			MaxTimeout int    `json:"maxTimeout"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		if q.Cmd != "request.get" || q.URL != "https://example.com/item" || q.MaxTimeout != 25000 {
			t.Errorf("unexpected payload: %+v", q)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","solution":{"url":"https://example.com/item","status":200,"response":"<html>title</html>","userAgent":"UA-1","cookies":[{"name":"cf_clearance","value":"test","domain":".example.com","path":"/","expires":1700000000.3,"secure":true}]}}`)
	}))
	defer server.Close()
	c, err := New(server.URL, 25*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Get(context.Background(), "https://example.com/item")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || r.URL != "https://example.com/item" || r.Status != http.StatusOK || r.UserAgent != "UA-1" || r.HTML != "<html>title</html>" || len(r.Cookies) != 1 || r.Cookies[0].Name != "cf_clearance" {
		t.Fatalf("unexpected result: %+v calls=%d", r, calls)
	}
}

func TestAPIResponseFailureCases(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"api-error", 200, `{"status":"error","message":"blocked"}`, ErrService},
		{"non-json", 200, "<html>error</html>", ErrProtocol},
		{"http-error", 502, `{"status":"error"}`, ErrService},
		{"no-html", 200, `{"status":"ok","solution":{"url":"https://example.com/item","status":200,"response":""}}`, ErrProtocol},
		{"challenge-still-present", 200, `{"status":"ok","solution":{"url":"https://example.com/item","status":403,"response":"verification"}}`, ErrUnsolved},
		{"oversize", 200, strings.Repeat(" ", int(maxResponseBytes+1)), ErrProtocol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			c, err := New(server.URL, 5*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Get(context.Background(), "https://example.com/item")
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v got %v", tc.want, err)
			}
		})
	}
}

func TestRejectBadEndpointAndTarget(t *testing.T) {
	for _, endpoint := range []string{"", "file:///tmp/a", "http://user:pass@localhost:8191", "http://localhost:8191/v1?token=secret"} {
		if _, err := New(endpoint, 10*time.Second, nil); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
	c, err := New("http://localhost:8191/v1", 10*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"file:///etc/passwd", "http://user:pass@localhost/", "/relative", "javascript:alert(1)"} {
		if _, err := c.Get(context.Background(), target); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
}

func TestContextCancellationIsPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	c, err := New(server.URL, 15*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Get(ctx, "https://example.com/item")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestRefusesHTTPRedirects(t *testing.T) {
	got := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got++
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	c, err := New(source.URL, 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "https://example.com/item")
	if err == nil || got != 0 {
		t.Fatalf("redirect was followed; err=%v count=%d", err, got)
	}
}
