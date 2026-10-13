package sitehtml

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

func newFixture(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := New("Fixture", server.URL, 1, language.English, provider.RequestPolicyConfig{
		MaxConcurrency: 1,
		MaxAttempts:    1,
		MaxWait:        500 * time.Millisecond,
	})
	t.Cleanup(server.Close)
	return client, server
}

func TestGetRejectsForeignHostsBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	client, _ := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	_, _, err := client.Get(context.Background(), "http://127.0.0.1:1/private")
	if !errors.Is(err, provider.ErrInvalidURL) {
		t.Fatalf("want invalid URL, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("unexpected requests: %d", calls.Load())
	}
}

func TestGetFollowsSameOriginRedirect(t *testing.T) {
	client, server := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			http.Redirect(w, r, "/detail?id=opaqueA", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html><body><div id='video_info'>OK</div></body></html>"))
	})
	doc, finalURL, err := client.Get(context.Background(), server.URL+"/search")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Find("#video_info").Length() != 1 || !strings.Contains(finalURL, "opaqueA") {
		t.Fatalf("failed to follow safe redirect: %q", finalURL)
	}
}

func TestGetRejectsCrossOriginRedirect(t *testing.T) {
	client, server := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data/")
		w.WriteHeader(http.StatusFound)
	})
	_, _, err := client.Get(context.Background(), server.URL+"/search")
	if !errors.Is(err, provider.ErrInvalidURL) {
		t.Fatalf("unsafe redirect was not rejected: %v", err)
	}
}

func TestGetClassifiesRateLimitAndChallenge(t *testing.T) {
	tests := []struct {
		name string
		status int
		body string
		want error
	}{
		{"rate-limit", http.StatusTooManyRequests, "slow down", provider.ErrRateLimited},
		{"forbidden", http.StatusForbidden, "denied", provider.ErrAccessDenied},
		{"challenge", http.StatusOK, "<html><title>Just a moment...</title><form id='challenge-form'></form></html>", provider.ErrChallengeRequired},
		{"server-error", http.StatusServiceUnavailable, "down", provider.ErrUpstreamUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, server := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, _, err := client.Get(context.Background(), server.URL+"/any")
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestGetCanceledBeforeRequest(t *testing.T) {
	var called atomic.Int32
	client, server := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.Get(ctx, server.URL+"/entry")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
	if called.Load() != 0 {
		t.Fatalf("canceled request sent %d times", called.Load())
	}
}
