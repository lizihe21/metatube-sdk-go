package javlibrary

import (
	"os"
	"testing"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

// This smoke check is intentionally skipped in all offline and CI runs.
// Supply a known public catalog number only in an authorized deployment.
func TestJavLibraryLiveSmoke(t *testing.T) {
	if os.Getenv("METATUBE_LIVE_TEST") != "1" {
		t.Skip("real JavLibrary requests disabled; set METATUBE_LIVE_TEST=1")
	}
	number := os.Getenv("METATUBE_JAVLIBRARY_NUMBER")
	if number == "" {
		t.Skip("supply METATUBE_JAVLIBRARY_NUMBER to enable a known-number live smoke test")
	}
	p := New()
	if endpoint := os.Getenv("METATUBE_FLARESOLVERR_URL"); endpoint != "" {
		cfg := envconfig.NewConfig()
		cfg.Set("flaresolverr_url", endpoint)
		if mode := os.Getenv("METATUBE_FLARESOLVERR_MODE"); mode != "" {
			cfg.Set("flaresolverr_mode", mode)
		}
		if err := p.SetConfig(cfg); err != nil {
			t.Fatalf("configure external FlareSolverr: %v", err)
		}
	}
	results, err := p.SearchMovie(number)
	if err != nil {
		t.Fatalf("live search for %q: %v", number, err)
	}
	for _, result := range results {
		if normalizedNumber(result.Number) != normalizedNumber(number) {
			continue
		}
		if !result.IsValid() {
			t.Fatalf("incomplete live search result: %+v", result)
		}
		info, err := p.GetMovieInfoByURL(result.Homepage)
		if err != nil {
			t.Fatalf("live detail for %q: %v", number, err)
		}
		if !info.IsValid() || info.ID != result.ID ||
			normalizedNumber(info.Number) != normalizedNumber(number) {
			t.Fatalf("inconsistent live detail: %+v (search=%+v)", info, result)
		}
		return
	}
	t.Fatalf("no exact catalog number %q in live JavLibrary search results", number)
}
