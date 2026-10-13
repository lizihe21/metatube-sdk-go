package javdb

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
)

var numberInText = regexp.MustCompile(`[0-9]+`)
var decimalInText = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

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
	u, err := url.Parse(base)
	if err != nil || !u.IsAbs() {
		return ""
	}
	r, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || r.User != nil {
		return ""
	}
	actual := u.ResolveReference(r)
	if (actual.Scheme != "http" && actual.Scheme != "https") || actual.Hostname() == "" {
		return ""
	}
	actual.Fragment = ""
	return actual.String()
}

// safeDetailLink does not permit links to a foreign origin or non-video page.
func safeDetailLink(pageURL, href string) (string, string) {
	base, err := url.Parse(pageURL)
	if err != nil || !base.IsAbs() {
		return "", ""
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil || ref.User != nil {
		return "", ""
	}
	target := base.ResolveReference(ref)
	if target.Scheme != base.Scheme || !strings.EqualFold(target.Host, base.Host) {
		return "", ""
	}
	parts := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "v" || !opaqueIDPattern.MatchString(parts[1]) {
		return "", ""
	}
	target.Fragment = ""
	return target.String(), parts[1]
}

func uniqueTexts(nodes *goquery.Selection) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	nodes.Each(func(_ int, sel *goquery.Selection) {
		val := clean(sel.Text())
		if val == "" {
			return
		}
		if _, exists := seen[val]; exists {
			return
		}
		seen[val] = struct{}{}
		result = append(result, val)
	})
	return result
}

func parseSearch(doc *goquery.Document, pageURL string) ([]*model.MovieSearchResult, error) {
	results := make([]*model.MovieSearchResult, 0)
	seen := make(map[string]bool)
	doc.Find(".movie-list .item").Each(func(_ int, item *goquery.Selection) {
		href := item.AttrOr("href", "")
		if href == "" {
			href = item.Find("a[href*='/v/']").First().AttrOr("href", "")
		}
		if href == "" {
			href = item.Closest("a[href]").AttrOr("href", "")
		}
		homepage, token := safeDetailLink(pageURL, href)
		if homepage == "" || seen[token] {
			return
		}
		number := clean(item.Find(".uid").First().Text())
		if number == "" {
			number = clean(item.Find(".video-title strong").First().Text())
		}
		if number == "" {
			return
		}
		title := clean(item.Find(".video-title").First().Text())
		title = clean(strings.TrimPrefix(title, number))
		if title == "" {
			title = number
		}
		cover := resolveImage(pageURL, attrFirst(item.Find("img").First(),
			"data-original", "data-src", "src"))
		seen[token] = true
		results = append(results, &model.MovieSearchResult{
			ID: token, Number: number, Title: title, Provider: Name,
			Homepage: homepage, ThumbURL: cover, CoverURL: cover,
		})
	})
	if len(results) == 0 && !isSearchEmpty(doc) {
		return nil, provider.ErrIncompleteMetadata
	}
	return results, nil
}

func isSearchEmpty(doc *goquery.Document) bool {
	if doc.Find(".movie-list .empty, .movie-list .no-results, .empty-state").Length() > 0 {
		return true
	}
	body := strings.ToLower(clean(doc.Find("body").Text()))
	for _, message := range []string{
		"no results", "nothing found", "0 results", "沒有結果", "找不到", "暂无结果",
		"查無結果", "没有搜索结果", "找不到符合",
	} {
		if strings.Contains(body, message) {
			return true
		}
	}
	return false
}

func detailText(doc *goquery.Document, selectors ...string) string {
	for _, selector := range selectors {
		if val := clean(doc.Find(selector).First().Text()); val != "" {
			return val
		}
	}
	return ""
}

func labelKey(s string) string {
	s = strings.ToLower(clean(s))
	s = strings.Trim(s, "：: ")
	return s
}

func labelMatches(label string, variants ...string) bool {
	for _, variant := range variants {
		if strings.Contains(label, variant) {
			return true
		}
	}
	return false
}

func parseRuntime(value string) int {
	if result := parser.ParseRuntime(value); result > 0 {
		return result
	}
	if digits := numberInText.FindString(value); digits != "" {
		n, _ := strconv.Atoi(digits)
		return n
	}
	return 0
}

func parseRating(value string) float64 {
	n := decimalInText.FindString(value)
	score, _ := strconv.ParseFloat(n, 64)
	return score
}

func parseDetail(doc *goquery.Document, pageURL, opaqueID string) (*model.MovieInfo, error) {
	if !opaqueIDPattern.MatchString(opaqueID) || doc.Find(".video-detail, .movie-panel-info").Length() == 0 {
		return nil, provider.ErrIncompleteMetadata
	}

	number := detailText(doc, ".video-detail .title.is-4 strong", ".title.is-4 strong")
	title := detailText(doc, ".video-detail .origin-title", ".video-detail .current-title",
		".video-detail .title.is-4", ".title.is-4")
	if title == "" {
		title = clean(doc.Find("meta[property='og:title']").First().AttrOr("content", ""))
	}
	info := &model.MovieInfo{
		ID: opaqueID, Provider: Name, Homepage: pageURL,
		CoverURL: resolveImage(pageURL, attrFirst(doc.Find(".column-video-cover img.video-cover, .column-video-cover img, .video-meta-panel img.video-cover").First(),
			"data-original", "data-src", "src")),
		Summary: detailText(doc, "span[itemprop='description']", ".movie-panel-info .movie-description"),
	}
	if info.CoverURL == "" {
		info.CoverURL = resolveImage(pageURL, doc.Find("meta[property='og:image']").First().AttrOr("content", ""))
	}
	info.ThumbURL = info.CoverURL

	var femaleActors []string
	var genericActors []string
	doc.Find(".movie-panel-info .panel-block").Each(func(_ int, block *goquery.Selection) {
		label := labelKey(block.Find("strong").First().Text())
		valueNode := block.Find(".value").First()
		fallbackToBlock := valueNode.Length() == 0
		if fallbackToBlock {
			// Some templates place the value directly after <strong>.
			valueNode = block
		}
		valueText := clean(valueNode.Text())
		if fallbackToBlock {
			valueText = clean(strings.TrimPrefix(valueText, block.Find("strong").First().Text()))
		}
		switch {
		case labelMatches(label, "番號", "番号", "識別碼", "识别码", "dvd id"):
			if number == "" {
				number = valueText
			}
		case labelMatches(label, "日期", "發行日期", "发行日期", "release date", "発売日"):
			info.ReleaseDate = parser.ParseDate(valueText)
		case labelMatches(label, "時長", "时长", "長度", "长度", "runtime", "duration"):
			info.Runtime = parseRuntime(valueText)
		case labelMatches(label, "導演", "导演", "director"):
			info.Director = valueText
		case labelMatches(label, "片商", "maker", "studio"):
			info.Maker = valueText
		case labelMatches(label, "發行", "发行", "label", "publisher"):
			info.Label = valueText
		case labelMatches(label, "系列", "series"):
			info.Series = valueText
		case labelMatches(label, "評分", "评分", "rating", "score"):
			info.Score = parseRating(valueText)
		case labelMatches(label, "類別", "类别", "標籤", "标签", "genres", "tags"):
			info.Genres = uniqueTexts(valueNode.Find("a"))
			if len(info.Genres) == 0 && valueText != "" {
				info.Genres = []string{valueText}
			}
		case labelMatches(label, "男優", "男演员", "male actor", "male cast"):
			// Do not silently combine explicitly male-only rows into female cast.
		case labelMatches(label, "女優", "女优", "actress", "female cast"):
			femaleActors = uniqueTexts(valueNode.Find("a"))
			if len(femaleActors) == 0 && valueText != "" {
				femaleActors = []string{valueText}
			}
		case labelMatches(label, "演員", "演员", "cast", "actors"):
			genericActors = uniqueTexts(valueNode.Find("a"))
		}
	})
	if len(femaleActors) > 0 {
		info.Actors = femaleActors
	} else {
		info.Actors = genericActors
	}
	info.Number = number
	info.Title = clean(strings.TrimPrefix(title, number))
	info.Title = clean(strings.TrimPrefix(info.Title, "["+number+"]"))

	seenImages := make(map[string]bool)
	doc.Find(".tile-images.preview-images a[href], .preview-images a[href]").Each(func(_ int, item *goquery.Selection) {
		if strings.Contains(item.AttrOr("class", ""), "preview-video-container") {
			return
		}
		raw := item.AttrOr("href", "")
		resolved := resolveImage(pageURL, raw)
		if resolved != "" && resolved != info.CoverURL && !seenImages[resolved] {
			seenImages[resolved] = true
			info.PreviewImages = append(info.PreviewImages, resolved)
		}
	})
	if len(info.PreviewImages) == 0 {
		doc.Find(".preview-images img").Each(func(_ int, item *goquery.Selection) {
			resolved := resolveImage(pageURL, attrFirst(item, "data-original", "data-src", "src"))
			if resolved != "" && resolved != info.CoverURL && !seenImages[resolved] {
				seenImages[resolved] = true
				info.PreviewImages = append(info.PreviewImages, resolved)
			}
		})
	}
	info.PreviewVideoURL = resolveImage(pageURL,
		attrFirst(doc.Find("#preview-video source[src], video#preview-video source[src], video source[src]").First(), "src"))
	if !info.IsValid() {
		return nil, provider.ErrIncompleteMetadata
	}
	return info, nil
}
