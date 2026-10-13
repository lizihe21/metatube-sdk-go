package requestpolicy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, retryAfter string) *http.Response {
	headers := make(http.Header)
	if retryAfter != "" {
		headers.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader("ok"))}
}
func req(ctx context.Context) *http.Request {
	r, _ := http.NewRequestWithContext(ctx, "GET", "https://example.org/item", nil)
	return r
}

func TestSharedUsesSameGovernorAndRejectsConflict(t *testing.T) {
	key := "test-shared-" + t.Name()
	a, err := Shared(key, Config{MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Shared(key, Config{MaxConcurrency: 1})
	if err != nil || a != b {
		t.Fatalf("expected shared instance; %v", err)
	}
	_, err = Shared(key, Config{MaxConcurrency: 2})
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
}

func TestDifferentTransportsRespectSharedConcurrency(t *testing.T) {
	g, _ := New(Config{MaxConcurrency: 1})
	entered := make(chan struct{}, 2)
	unblock := make(chan struct{})
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-unblock
		return response(200, ""), nil
	})
	trA, trB := Wrap(base, g), Wrap(base, g)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, tr := range []*Transport{trA, trB} {
		go func(tr *Transport) {
			defer wg.Done()
			res, err := tr.RoundTrip(req(context.Background()))
			if err == nil {
				_ = res.Body.Close()
			}
		}(tr)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first attempt did not start")
	}
	select {
	case <-entered:
		t.Fatal("second request started while first response was open")
	case <-time.After(60 * time.Millisecond):
	}
	close(unblock)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second request never started")
	}
	wg.Wait()
}

func TestMinIntervalBetweenRequests(t *testing.T) {
	g, _ := New(Config{MinInterval: 65 * time.Millisecond})
	var times []time.Time
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		times = append(times, time.Now())
		return response(200, ""), nil
	})
	transport := Wrap(base, g)
	for i := 0; i < 2; i++ {
		r, err := transport.RoundTrip(req(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
	}
	if gap := times[1].Sub(times[0]); gap < 60*time.Millisecond {
		t.Fatalf("spacing too short: %v", gap)
	}
}

func TestRetryAfterCooldownOnSharedGovernor(t *testing.T) {
	g, _ := New(Config{RateLimitCooldown: 10 * time.Millisecond})
	g.ObserveResponse(429, http.Header{"Retry-After": []string{"1"}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()
	_, err := Wrap(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("must not send during cooldown")
		return nil, nil
	}), g).RoundTrip(req(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation before cooldown expired: %v", err)
	}
}

func TestPolicyQueueTimeout(t *testing.T) {
	g, _ := New(Config{MaxWait: 35 * time.Millisecond})
	release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = g.acquire(context.Background())
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("expected bounded wait: %v", err)
	}
}

func TestRetriesTransientStatusWithinBudget(t *testing.T) {
	g, _ := New(Config{MaxAttempts: 2, RateLimitCooldown: 1 * time.Millisecond})
	var count atomic.Int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if count.Add(1) == 1 {
			return response(http.StatusServiceUnavailable, ""), nil
		}
		return response(200, ""), nil
	})
	res, err := Wrap(base, g).RoundTrip(req(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || count.Load() != 2 {
		t.Fatalf("status=%d attempts=%d", res.StatusCode, count.Load())
	}
}

func TestDoesNotRetryPostOrForbidden(t *testing.T) {
	g, _ := New(Config{MaxAttempts: 3})
	var count atomic.Int32
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		count.Add(1)
		return response(403, ""), nil
	})
	r, err := Wrap(base, g).RoundTrip(req(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if count.Load() != 1 {
		t.Fatalf("forbidden retried %d times", count.Load())
	}
}

func TestBodyCloseReleasesConcurrencySlot(t *testing.T) {
	g, _ := New(Config{MaxWait: 100 * time.Millisecond})
	tr := Wrap(roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, ""), nil }), g)
	a, _ := tr.RoundTrip(req(context.Background()))
	_ = a.Body.Close()
	b, err := tr.RoundTrip(req(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Body.Close()
}
