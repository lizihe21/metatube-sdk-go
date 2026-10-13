package javdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/sitehtml"
)

func fixtureClient(base string) *JavDB {
	return &JavDB{Client: sitehtml.New(Name, base, Priority, language.Chinese,
		provider.RequestPolicyConfig{MaxConcurrency: 1, MaxAttempts: 1, MaxWait: time.Second})}
}
func readDoc(t *testing.T, raw string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil { t.Fatal(err) }
	return doc
}

func TestSearchResultPreservesOpaqueID(t *testing.T) {
	items, err := parseSearch(readDoc(t, searchHTML), "https://javdb.com/search?q=SSIS-001")
	if err != nil { t.Fatal(err) }
	if len(items) != 2 || items[0].ID != "AbC9" || items[0].Number != "SSIS-001" ||
		items[0].Homepage != "https://javdb.com/v/AbC9" || items[1].ID != "xYz89" {
		t.Fatalf("invalid search entries: %+v", items)
	}
}

func TestDetailParsesFieldsAndFiltersMaleOnlyActors(t *testing.T) {
	info, err := parseDetail(readDoc(t, detailHTML), "https://javdb.com/v/AbC9", "AbC9")
	if err != nil { t.Fatal(err) }
	if info.ID != "AbC9" || info.Number != "SSIS-001" || info.Title != "Fixture title A" ||
		info.Maker != "Maker B" || info.Label != "Label B" || info.Runtime != 120 {
		t.Fatalf("metadata incorrect: %+v", info)
	}
	if len(info.Actors) != 2 || info.Actors[0] != "Actress A" || len(info.Genres) != 2 ||
		len(info.PreviewImages) != 2 || info.PreviewVideoURL == "" || !info.IsValid() {
		t.Fatalf("missing fields: %+v", info)
	}
}

func TestURLParseRejectsForeignAndMalformed(t *testing.T) {
	p := New()
	id, err := p.ParseMovieIDFromURL("https://javdb.com/v/xYz89")
	if err != nil || id != "xYz89" { t.Fatalf("roundtrip id=%q err=%v", id, err) }
	for _, raw := range []string{"https://evil.test/v/xYz89", "https://javdb.com/search?q=SSIS-001",
		"https://javdb.com/v/", "https://javdb.com/v/../../admin"} {
		if _, err := p.ParseMovieIDFromURL(raw); !errors.Is(err, provider.ErrInvalidURL) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
}

func TestNumberLookupSelectsExactResult(t *testing.T) {
	var searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			searches.Add(1)
			_, _ = w.Write([]byte(searchHTML))
			return
		}
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer server.Close()
	info, err := fixtureClient(server.URL).GetMovieInfoByIDContext(context.Background(), "SSIS-001")
	if err != nil { t.Fatal(err) }
	if info.ID != "AbC9" || searches.Load() != 1 { t.Fatalf("wrong lookup %+v searches=%d", info, searches.Load()) }
}

func TestSearchBlockedAndUnknownPages(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want error
	}{
		{"challenge", "<html><title>Just a moment</title></html>", provider.ErrChallengeRequired},
		{"unknown", "<html><body>unexpected structure</body></html>", provider.ErrIncompleteMetadata},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.content))
			}))
			defer server.Close()
			_, err := fixtureClient(server.URL).SearchMovie("SSIS-001")
			if !errors.Is(err, tc.want) { t.Fatalf("want %v got %v", tc.want, err) }
		})
	}
}

func TestSearchHandlesSameOriginDetailRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			http.Redirect(w, r, "/v/AbC9", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer server.Close()
	items, err := fixtureClient(server.URL).SearchMovie("SSIS-001")
	if err != nil || len(items) != 1 || items[0].ID != "AbC9" {
		t.Fatalf("unexpected redirect results: %v %+v", err, items)
	}
}

func TestSearchResultHandlesAnchorWrappingItem(t *testing.T) {
	html := `<html><body><div class="movie-list"><a href="/v/QwE7"><div class="item"><div class="video-title"><strong>ABCD-123</strong> Wrapped</div></div></a></div></body></html>`
	items, err := parseSearch(readDoc(t, html), "https://javdb.com/search?q=ABCD-123")
	if err != nil || len(items) != 1 || items[0].ID != "QwE7" {
		t.Fatalf("wrapped anchor not parsed: %v %+v", err, items)
	}
}

func TestSearchRejectsCrossSiteLinks(t *testing.T) {
	html := `<html><body><div class="movie-list"><div class="item"><a href="http://169.254.169.254/v/AbC9">Bad</a><div class="video-title"><strong>ABCD-123</strong> Bad</div></div></div></body></html>`
	items, err := parseSearch(readDoc(t, html), "https://javdb.com/search?q=ABCD-123")
	if err == nil || len(items) != 0 {
		t.Fatalf("foreign search link became a valid item: %v %+v", err, items)
	}
}

func TestRecognizedEmptySearchIsNotParserError(t *testing.T) {
	doc := readDoc(t, "<html><body><p>No results found</p></body></html>")
	results, err := parseSearch(doc, "https://javdb.com/search?q=none")
	if err != nil || len(results) != 0 {
		t.Fatalf("empty result not classified: %v %+v", err, results)
	}
}
