package fetch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

func TestPolicyRetryBudgetDoesNotMultiplyWithRetryableHTTP(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	fetcher := Default(&Config{Timeout: 2 * time.Second})
	err := fetcher.ConfigurePolicy(provider.RequestPolicyConfig{
		Scope: "fetch-budget-" + t.Name(),
		MinInterval: 15 * time.Millisecond,
		MaxAttempts: 2,
	})
	if err != nil { t.Fatal(err) }
	response, err := fetcher.Get(server.URL)
	if err != nil { t.Fatal(err) }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || requests != 2 {
		t.Fatalf("status=%d requests=%d", response.StatusCode, requests)
	}
}

func TestPolicyDisablesLegacyAutomaticRetry(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	fetcher := Default(&Config{})
	if err := fetcher.ConfigurePolicy(provider.RequestPolicyConfig{
		Scope: "fetch-no-double-retry-" + t.Name(),
		MaxAttempts: 1,
	}); err != nil { t.Fatal(err) }
	_, err := fetcher.Get(server.URL)
	if err == nil { t.Fatal("expected status error") }
	if requests != 1 { t.Fatalf("expected exactly one request, got %d", requests) }
}
