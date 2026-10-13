package scraper

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

func TestPolicyAppliesAcrossClonedCollectors(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer server.Close()

	s := NewScraper("fixture-site", server.URL, 1, language.English, WithAllowURLRevisit())
	if err := s.SetRequestPolicy(provider.RequestPolicyConfig{
		Scope: "scraper-clones-" + t.Name(),
		MaxConcurrency: 1,
		MinInterval: 65 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.ClonedCollector().Visit(server.URL); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(starts))
	}
	if gap := starts[1].Sub(starts[0]); gap < 55*time.Millisecond {
		t.Fatalf("clone skipped shared delay: %v", gap)
	}
}

func TestPolicySurvivesProxyOverride(t *testing.T) {
	var forwarded atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		_, _ = w.Write([]byte("<html>proxied</html>"))
	}))
	defer proxy.Close()

	s := NewScraper("fixture-proxy", "http://example.invalid", 1, language.English)
	if err := s.SetRequestPolicy(provider.RequestPolicyConfig{
		Scope: "scraper-proxy-" + t.Name(),
		MaxConcurrency: 1,
	}); err != nil { t.Fatal(err) }
	if err := s.SetProxy(proxy.URL); err != nil { t.Fatal(err) }
	if err := s.ClonedCollector().Visit("http://example.invalid/entry"); err != nil { t.Fatal(err) }
	if forwarded.Load() != 1 { t.Fatalf("expected proxied request, got %d", forwarded.Load()) }
}
