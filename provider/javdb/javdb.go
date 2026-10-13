package javdb

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/sitehtml"
)

const (
	Name     = "JavDB"
	Priority = 1000 - 14
	baseURL  = "https://javdb.com/"
)

var (
	_               provider.MovieProvider        = (*JavDB)(nil)
	_               provider.MovieSearcher        = (*JavDB)(nil)
	_               provider.RequestPolicySetter  = (*JavDB)(nil)
	_               provider.ConfigSetter         = (*JavDB)(nil)
	_               provider.ProxySetter          = (*JavDB)(nil)
	_               provider.RequestTimeoutSetter = (*JavDB)(nil)
	_               provider.Fetcher              = (*JavDB)(nil)
	opaqueIDPattern                               = regexp.MustCompile(`^[A-Za-z0-9]{3,40}$`)
)

// JavDB identifies titles by the opaque ID used in /v/{id}, not by movie number.
type JavDB struct {
	*sitehtml.Client
}

func New() *JavDB {
	return &JavDB{Client: sitehtml.New(Name, baseURL, Priority, language.Chinese,
		provider.RequestPolicyConfig{
			MinInterval:       8 * time.Second,
			Jitter:            2 * time.Second,
			MaxConcurrency:    1,
			MaxWait:           25 * time.Second,
			RateLimitCooldown: 90 * time.Second,
			ForbiddenCooldown: 5 * time.Minute,
			MaxAttempts:       1,
		})}
}

func (p *JavDB) NormalizeMovieID(id string) string {
	return strings.TrimSpace(id)
}

func (p *JavDB) NormalizeMovieKeyword(keyword string) string {
	return strings.TrimSpace(keyword)
}

func (p *JavDB) ParseMovieIDFromURL(rawURL string) (string, error) {
	if !p.ValidURL(rawURL) {
		return "", provider.ErrInvalidURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", provider.ErrInvalidURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "v" || !opaqueIDPattern.MatchString(parts[1]) {
		return "", provider.ErrInvalidURL
	}
	return parts[1], nil
}

func (p *JavDB) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	return p.GetMovieInfoByIDContext(context.Background(), id)
}

func (p *JavDB) GetMovieInfoByIDContext(ctx context.Context, id string) (*model.MovieInfo, error) {
	id = p.NormalizeMovieID(id)
	if id == "" {
		return nil, provider.ErrInvalidID
	}
	// IDs returned by JavDB search do not have dashes. For simple IDs,
	// first check the native detail URL. A verified 404 can fall back to
	// exact code search; restrictions and network failures never do.
	if opaqueIDPattern.MatchString(id) {
		detail := strings.TrimRight(p.URL().String(), "/") + "/v/" + url.PathEscape(id)
		info, err := p.GetMovieInfoByURLContext(ctx, detail)
		if err == nil {
			return info, nil
		}
		if !errors.Is(err, provider.ErrInfoNotFound) {
			return nil, err
		}
	}
	items, err := p.SearchMovieContext(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if normalizedNumber(item.Number) != normalizedNumber(id) {
			continue
		}
		info, err := p.GetMovieInfoByURLContext(ctx, item.Homepage)
		if err != nil {
			return nil, err
		}
		if normalizedNumber(info.Number) != normalizedNumber(id) {
			return nil, provider.ErrInfoNotFound
		}
		return info, nil
	}
	return nil, provider.ErrInfoNotFound
}

func (p *JavDB) GetMovieInfoByURL(rawURL string) (*model.MovieInfo, error) {
	return p.GetMovieInfoByURLContext(context.Background(), rawURL)
}

func (p *JavDB) GetMovieInfoByURLContext(ctx context.Context, rawURL string) (*model.MovieInfo, error) {
	id, err := p.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}
	doc, finalURL, err := p.Get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if redirectedID, parseErr := p.ParseMovieIDFromURL(finalURL); parseErr == nil {
		id = redirectedID
	}
	return parseDetail(doc, finalURL, id)
}

func (p *JavDB) SearchMovie(keyword string) ([]*model.MovieSearchResult, error) {
	return p.SearchMovieContext(context.Background(), keyword)
}

func (p *JavDB) SearchMovieContext(ctx context.Context, keyword string) ([]*model.MovieSearchResult, error) {
	keyword = p.NormalizeMovieKeyword(keyword)
	if keyword == "" {
		return nil, provider.ErrInvalidKeyword
	}
	searchURL := strings.TrimRight(p.URL().String(), "/") + "/search?q=" +
		url.QueryEscape(keyword) + "&f=all"
	doc, finalURL, err := p.Get(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	if doc.Find(".movie-panel-info, .video-detail .title.is-4").Length() > 0 {
		id, idErr := p.ParseMovieIDFromURL(finalURL)
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

func normalizedNumber(s string) string {
	var b strings.Builder
	for _, ch := range strings.ToUpper(strings.TrimSpace(s)) {
		if ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func init() {
	provider.Register(Name, New)
}
