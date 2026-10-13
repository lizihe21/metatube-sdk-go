package javlibrary

import (
	"context"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/sitehtml"
)

const (
	Name     = "JavLibrary"
	Priority = 1000 - 15
	baseURL  = "https://www.javlibrary.com/"
)

var (
	_ provider.MovieProvider        = (*JavLibrary)(nil)
	_ provider.MovieSearcher        = (*JavLibrary)(nil)
	_ provider.RequestPolicySetter  = (*JavLibrary)(nil)
	_ provider.ProxySetter          = (*JavLibrary)(nil)
	_ provider.RequestTimeoutSetter = (*JavLibrary)(nil)
	_ provider.Fetcher              = (*JavLibrary)(nil)
	opaqueIDPattern                 = regexp.MustCompile(`^[a-zA-Z0-9]{5,64}$`)
	runtimeDigits                   = regexp.MustCompile(`[0-9]+`)
)

// JavLibrary exposes native opaque video IDs, not movie numbers, to callers.
type JavLibrary struct {
	*sitehtml.Client
}

func New() *JavLibrary {
	return &JavLibrary{Client: sitehtml.New(Name, baseURL, Priority, language.Japanese,
		provider.RequestPolicyConfig{
			MinInterval:       5 * time.Second,
			Jitter:            2 * time.Second,
			MaxConcurrency:    1,
			MaxWait:           20 * time.Second,
			RateLimitCooldown: 90 * time.Second,
			ForbiddenCooldown: 5 * time.Minute,
			MaxAttempts:       1,
		})}
}

func (p *JavLibrary) NormalizeMovieID(id string) string {
	return strings.TrimSpace(id) // Native website IDs are case sensitive.
}

func (p *JavLibrary) NormalizeMovieKeyword(keyword string) string {
	return strings.TrimSpace(keyword)
}

func isLanguageDir(s string) bool {
	switch strings.Trim(s, "/") {
	case "", "cn", "tw", "ja", "en":
		return true
	}
	return false
}

func (p *JavLibrary) ParseMovieIDFromURL(rawURL string) (string, error) {
	if !p.ValidURL(rawURL) {
		return "", provider.ErrInvalidURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", provider.ErrInvalidURL
	}
	identifier := ""
	if isLanguageDir(u.Path) {
		identifier = u.Query().Get("v")
	} else if strings.EqualFold(path.Ext(u.Path), ".html") {
		if !isLanguageDir(path.Dir(u.Path)) {
			return "", provider.ErrInvalidURL
		}
		identifier = strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
	}
	if !opaqueIDPattern.MatchString(identifier) {
		return "", provider.ErrInvalidURL
	}
	return identifier, nil
}

func (p *JavLibrary) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	return p.GetMovieInfoByIDContext(context.Background(), id)
}

func (p *JavLibrary) GetMovieInfoByIDContext(ctx context.Context, id string) (*model.MovieInfo, error) {
	id = p.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	if opaqueIDPattern.MatchString(id) && strings.HasPrefix(strings.ToLower(id), "javli") {
		u := strings.TrimRight(p.URL().String(), "/") + "/cn/?v=" + url.QueryEscape(id)
		return p.GetMovieInfoByURLContext(ctx, u)
	}
	// A movie number is not the JavLibrary database's opaque video ID.
	// Resolve it via an exact search result, never guess the first hit.
	results, err := p.SearchMovieContext(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, item := range results {
		if normalizedNumber(item.Number) == normalizedNumber(id) {
			info, err := p.GetMovieInfoByURLContext(ctx, item.Homepage)
			if err != nil {
				return nil, err
			}
			if normalizedNumber(info.Number) != normalizedNumber(id) {
				return nil, provider.ErrInfoNotFound
			}
			return info, nil
		}
	}
	return nil, provider.ErrInfoNotFound
}

func (p *JavLibrary) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	return p.GetMovieInfoByURLContext(context.Background(), rawURL)
}

func (p *JavLibrary) GetMovieInfoByURLContext(ctx context.Context, rawURL string) (*model.MovieInfo, error) {
	id, err := p.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	doc, finalURL, err := p.Client.Get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	// Prefer the stable opaque ID from the final URL after same-site redirects.
	if redirectedID, parseErr := p.ParseMovieIDFromURL(finalURL); parseErr == nil {
		id = redirectedID
	}
	return parseDetail(doc, finalURL, id)
}

func (p *JavLibrary) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	return p.SearchMovieContext(context.Background(), keyword)
}

func (p *JavLibrary) SearchMovieContext(ctx context.Context, keyword string) ([]*model.MovieSearchResult, error) {
	keyword = p.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	doc, finalURL, err := p.Client.Get(ctx, searchURL(p.URL().String(), keyword))
	if err != nil {
		return nil, err
	}
	// An exact search can redirect to a full detail page.
	if doc.Find("#video_info").Length() > 0 {
		id, idErr := p.ParseMovieIDFromURL(finalURL)
		if idErr != nil {
			if canonical := doc.Find("link[rel='canonical']").First().AttrOr("href", ""); canonical != "" {
				if actual, resolveErr := p.ResolveURL(finalURL, canonical); resolveErr == nil {
					id, idErr = p.ParseMovieIDFromURL(actual)
					finalURL = actual
				}
			}
		}
		if idErr != nil {
			return nil, provider.ErrIncompleteMetadata
		}
		info, parseErr := parseDetail(doc, finalURL, id)
		if parseErr != nil {
			return nil, parseErr
		}
		return []*model.MovieSearchResult{info.ToSearchResult()}, nil
	}
	return parseSearch(doc, finalURL)
}

func searchURL(base, keyword string) string {
	return strings.TrimRight(base, "/") + "/cn/vl_searchbyid.php?keyword=" + url.QueryEscape(keyword)
}

func normalizedNumber(s string) string {
	var result strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func isSearchEmpty(doc *goquery.Document) bool {
	body := strings.ToLower(strings.TrimSpace(doc.Find("body").Text()))
	for _, msg := range []string{
		"no results", "nothing found", "not found", "沒有找到", "找不到", "搜索結果為空",
		"検索結果がありません", "該当する作品はありません",
	} {
		if strings.Contains(body, msg) {
			return true
		}
	}
	return false
}

func parseSearch(doc *goquery.Document, pageURL string) ([]*model.MovieSearchResult, error) {
	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]struct{})
	doc.Find(".videothumblist .video, #videos .video, .video[id^='vid_']").Each(func(_ int, item *goquery.Selection) {
		number := clean(item.Find(".id").First().Text())
		if number == "" {
			return
		}
		token := strings.TrimPrefix(item.AttrOr("id", ""), "vid_")
		href := item.Find("a[href]").First().AttrOr("href", "")
		if token == "" && href != "" {
			if u, err := url.Parse(href); err == nil {
				token = u.Query().Get("v")
				if token == "" && path.Ext(u.Path) == ".html" {
					token = strings.TrimSuffix(path.Base(u.Path), ".html")
				}
			}
		}
		if !opaqueIDPattern.MatchString(token) {
			return
		}
		homepage := makeVideoURL(pageURL, href, token)
		if homepage == "" {
			return
		}
		if _, found := seen[token]; found {
			return
		}
		seen[token] = struct{}{}
		title := clean(item.Find(".title").First().Text())
		img := item.Find("img").First()
		if title == "" {
			title = clean(img.AttrOr("alt", ""))
		}
		if title == "" {
			title = number
		}
		thumb := resolveImage(pageURL, attrFirst(img, "data-original", "data-src", "src"))
		results = append(results, &model.MovieSearchResult{
			ID: token, Number: number, Title: title, Provider: Name,
			Homepage: homepage, ThumbURL: thumb, CoverURL: thumb,
		})
	})
	if len(results) == 0 && !isSearchEmpty(doc) {
		return nil, provider.ErrIncompleteMetadata
	}
	return results, nil
}

func makeVideoURL(pageURL, href, token string) string {
	u, err := url.Parse(pageURL)
	if err != nil || !u.IsAbs() {
		return ""
	}
	// JavLibrary's relative '?v=' search links open the language index,
	// not the search endpoint carrying the original query.
	langIndex := *u
	if !strings.HasSuffix(langIndex.Path, "/") {
		langIndex.Path = path.Dir(langIndex.Path) + "/"
	}
	langIndex.RawQuery = ""
	langIndex.Fragment = ""

	target := &langIndex
	if href != "" {
		ref, err := url.Parse(href)
		if err == nil {
			if strings.HasPrefix(href, "?v=") {
				target = langIndex.ResolveReference(ref)
			} else {
				target = u.ResolveReference(ref)
			}
		}
	}
	if target.Query().Get("v") != token &&
		!strings.EqualFold(strings.TrimSuffix(path.Base(target.Path), path.Ext(target.Path)), token) {
		target = langIndex.ResolveReference(&url.URL{RawQuery: "v=" + url.QueryEscape(token)})
	}
	if (target.Scheme != "https" && target.Scheme != "http") || !strings.EqualFold(target.Host, u.Host) {
		return ""
	}
	if target.Query().Get("v") != token && !(strings.EqualFold(path.Ext(target.Path), ".html") &&
		strings.EqualFold(strings.TrimSuffix(path.Base(target.Path), path.Ext(target.Path)), token)) {
		return ""
	}
	target.Fragment = ""
	return target.String()
}

func clean(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func attrFirst(s *goquery.Selection, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(s.AttrOr(key, "")); v != "" {
			return v
		}
	}
	return ""
}

func resolveImage(base, raw string) string {
	if raw == "" {
		return ""
	}
	origin, err := url.Parse(base)
	if err != nil || !origin.IsAbs() {
		return ""
	}
	relative, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || relative.User != nil {
		return ""
	}
	u := origin.ResolveReference(relative)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func uniqueTexts(selection *goquery.Selection) []string {
	result := make([]string, 0)
	seen := make(map[string]bool)
	selection.Each(func(_ int, s *goquery.Selection) {
		text := clean(s.Text())
		if text != "" && !seen[text] {
			seen[text] = true
			result = append(result, text)
		}
	})
	return result
}

func detailText(doc *goquery.Document, selectors ...string) string {
	for _, selector := range selectors {
		if value := clean(doc.Find(selector).First().Text()); value != "" {
			return value
		}
	}
	return ""
}

func parseDetail(doc *goquery.Document, pageURL, opaqueID string) (*model.MovieInfo, error) {
	if !opaqueIDPattern.MatchString(opaqueID) || doc.Find("#video_info").Length() == 0 {
		return nil, provider.ErrIncompleteMetadata
	}
	number := detailText(doc, "#video_id .text", "#video_id td.text")
	title := detailText(doc, "#video_title h3 a", "#video_title a", "#video_title h3")
	if title == "" {
		title = clean(doc.Find("meta[property='og:title']").First().AttrOr("content", ""))
	}
	if number != "" {
		title = clean(strings.TrimPrefix(title, number))
		title = clean(strings.TrimPrefix(title, "["+number+"]"))
	}
	img := doc.Find("#video_jacket_img").First()
	cover := resolveImage(pageURL, attrFirst(img, "data-original", "data-src", "src"))
	if cover == "" {
		cover = resolveImage(pageURL, doc.Find("#video_jacket a").First().AttrOr("href", ""))
	}
	if number == "" || title == "" || cover == "" {
		return nil, provider.ErrIncompleteMetadata
	}
	length := detailText(doc, "#video_length .text")
	runtime := parser.ParseRuntime(length)
	if runtime == 0 {
		if digits := runtimeDigits.FindString(length); digits != "" {
			runtime = parser.ParseInt(digits)
		}
	}
	info := &model.MovieInfo{
		ID: opaqueID, Number: number, Title: title, Provider: Name, Homepage: pageURL,
		CoverURL: cover, ThumbURL: cover,
		ReleaseDate: parser.ParseDate(detailText(doc, "#video_date .text")),
		Runtime: runtime,
		Director: detailText(doc, "#video_director .text", "#video_director a"),
		Maker: detailText(doc, "#video_maker .text", "#video_maker a"),
		Label: detailText(doc, "#video_label .text", "#video_label a"),
		Series: detailText(doc, "#video_series .text", "#video_series a"),
		Actors: uniqueTexts(doc.Find("#video_cast .star a")),
		Genres: uniqueTexts(doc.Find("#video_genres .genre a")),
		Score: parser.ParseScore(detailText(doc, "#video_rating .num")),
	}
	seen := make(map[string]bool)
	doc.Find("#sample-waterfall a[href], #video_samples a[href]").Each(func(_ int, a *goquery.Selection) {
		resolved := resolveImage(pageURL, a.AttrOr("href", ""))
		if resolved != "" && resolved != cover && !seen[resolved] {
			seen[resolved] = true
			info.PreviewImages = append(info.PreviewImages, resolved)
		}
	})
	return info, nil
}

func init() {
	provider.Register(Name, New)
}
