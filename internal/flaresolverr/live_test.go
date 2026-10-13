package flaresolverr

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Live API test for a reachable, operator-managed FlareSolverr service.
// Never executed by the standard offline test suite.
func TestExternalFlareSolverrLiveSmoke(t *testing.T) {
	if os.Getenv("METATUBE_LIVE_TEST") != "1" {
		t.Skip("real FlareSolverr requests disabled; set METATUBE_LIVE_TEST=1")
	}
	endpoint := os.Getenv("METATUBE_FLARESOLVERR_URL")
	target := os.Getenv("METATUBE_FLARESOLVERR_TARGET")
	if endpoint == "" || target == "" {
		t.Skip("set METATUBE_FLARESOLVERR_URL and METATUBE_FLARESOLVERR_TARGET")
	}
	client, err := New(endpoint, 60*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(context.Background(), target)
	if err != nil {
		t.Fatalf("live FlareSolverr service: %v", err)
	}
	if resp.Status != 200 || !strings.Contains(strings.ToLower(resp.HTML), "<html") {
		t.Fatalf("unexpected live response: status=%d, len=%d", resp.Status, len(resp.HTML))
	}
}
