package javlibrary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/sitehtml"
)

func fixtureClient(serverURL string) *JavLibrary {
	return &JavLibrary{Client: sitehtml.New(Name, serverURL, Priority, language.Japanese,
		provider.RequestPolicyConfig{MaxConcurrency: 1, MaxAttempts: 1, MaxWait: time.Second})}
}

func readDoc(t *testing.T, raw string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseSearchPreservesOpaqueID(t *testing.T) {
	page := "https://www.javlibrary.com/cn/vl_searchbyid.php?keyword=SSIS-001"
	result, err := parseSearch(readDoc(t, searchHTML), page)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].ID != "javliAB12" || result[0].Number != "SSIS-001" ||
		result[0].Homepage != "https://www.javlibrary.com/cn/?v=javliAB12" {
		t.Fatalf("wrong opaque ID mapping: %+v", result[0])
	}
	if result[1].ID != "javliCD34" || !strings.HasSuffix(result[1].Homepage, "/cn/javliCD34.html") {
		t.Fatalf("unexpected legacy URL: %+v", result[1])
	}
}

func TestParseDetailMetadata(t *testing.T) {
	p := "https://www.javlibrary.com/cn/?v=javliAB12"
	info, err := parseDetail(readDoc(t, detailHTML), p, "javliAB12")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "javliAB12" || info.Number != "SSIS-001" || info.Title != "A title" {
		t.Fatalf("incorrect identity/title: %+v", info)
	}
	if info.Maker != "Maker A" || info.Label != "Label A" || info.Runtime != 120 ||
		len(info.Actors) != 2 || len(info.Genres) != 2 || len(info.PreviewImages) != 1 {
		t.Fatalf("incomplete parsed info: %+v", info)
	}
	if !info.IsValid() {
		t.Fatalf("invalid movie info: %+v", info)
	}
}

func TestGetMovieInfoByIDResolvesNumberThroughSearch(t *testing.T) {
	var detailCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("keyword") != "" {
			_, _ = w.Write([]byte(searchHTML))
			return
		}
		detailCount++
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer server.Close()
	c := fixtureClient(server.URL)
	info, err := c.GetMovieInfoByIDContext(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatal(err)
	}
	if info.Number != "SSIS-001" || info.ID != "javliAB12" || detailCount != 1 {
		t.Fatalf("wrong search/detail routing: %+v detailCount=%d", info, detailCount)
	}
}

func TestSearchRedirectToDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("keyword") != "" {
			http.Redirect(w, r, "/cn/?v=javliAB12", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(detailHTML))
	}))
	defer server.Close()
	c := fixtureClient(server.URL)
	results, err := c.SearchMovieContext(context.Background(), "SSIS-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "javliAB12" {
		t.Fatalf("redirect not resolved: %+v", results)
	}
}

func TestSearchDoesNotTreatUnknownHTMLAsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><title>Unexpected markup</title></html>"))
	}))
	defer server.Close()
	_, err := fixtureClient(server.URL).SearchMovie("SSIS-001")
	if !errors.Is(err, provider.ErrIncompleteMetadata) {
		t.Fatalf("unknown HTML incorrectly treated as no results: %v", err)
	}
}

func TestCrossDomainAndMalformedURLRejected(t *testing.T) {
	c := New()
	for _, raw := range []string{
		"https://evil.example/cn/?v=javliAB12",
		"https://www.javlibrary.com/cn/?v=",
		"https://www.javlibrary.com/cn/vl_searchbyid.php?keyword=SSIS-001",
	} {
		if _, err := c.ParseMovieIDFromURL(raw); !errors.Is(err, provider.ErrInvalidURL) {
			t.Fatalf("accepted invalid detail URL: %s (%v)", raw, err)
		}
	}
	token, err := c.ParseMovieIDFromURL("https://www.javlibrary.com/cn/javliCD34.html")
	if err != nil || token != "javliCD34" {
		t.Fatalf("legacy page ID parse failed: %q %v", token, err)
	}
	if got := c.NormalizeMovieID("javliAB12"); got != "javliAB12" {
		t.Fatalf("opaque ID changed case: %q", got)
	}
}

func TestSearchQueryEncoding(t *testing.T) {
	key := "SSIS-001 & more"
	u, err := url.Parse(searchURL("https://www.javlibrary.com/", key))
	if err != nil || u.Query().Get("keyword") != key {
		t.Fatalf("query not encoded: %v %v", u, err)
	}
}
