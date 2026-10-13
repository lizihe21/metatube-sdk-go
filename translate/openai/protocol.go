package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultSystemPrompt = `You are a professional translator for adult video content. Your sole task is to translate the user's input accurately and naturally.
Rules:
1. Translate the user's input as provided, treating it as the source text.
2. Use official translations for actor/actress names if available; otherwise, keep them unchanged.
3. Do not invent translations for names without official versions.
4. Maintain any numbers, dates, and measurements in their original format.
5. Translate naturally and fluently, avoiding word-for-word translation.
6. Do not add any explanations, notes, or comments under any circumstances.
7. Only output the translation result, with no additional content.`

const (
	maxResponseBytes int64 = 8 << 20
	maxInputBytes          = 256 << 10
	defaultTimeout         = 60 * time.Second
	maxTimeout             = 180 * time.Second
	maxRetryWait           = 4 * time.Second
)

var (
	ErrConfig     = errors.New("openai translation: invalid configuration")
	ErrUpstream   = errors.New("openai translation: upstream failure")
	ErrIncomplete = errors.New("openai translation: incomplete or truncated result")
	ErrRefusal    = errors.New("openai translation: refused or filtered result")
	ErrProtocol   = errors.New("openai translation: unsupported response format")
)

// OpenAI can call OpenAI-style Chat Completions or Responses endpoints.
// Stream controls the upstream request; the public Translate interface still
// returns a single completed string. Both JSON and SSE are accepted even when
// a compatible upstream ignores the requested Stream flag.
type OpenAI struct {
	APIKey  string `json:"openai-api-key"`
	APIUrl  string `json:"openai-api-url"`
	Model   string `json:"openai-model"`
	Prompt  string `json:"openai-prompt"`
	APIMode string `json:"openai-api-mode"` // chat_completions (default), responses
	Stream  bool   `json:"openai-stream"`
	Timeout string `json:"openai-timeout"` // Go duration, default 60s
}

func (oa *OpenAI) Translate(q, from, to string) (string, error) {
	return oa.TranslateContext(context.Background(), q, from, to)
}

func (oa *OpenAI) TranslateContext(ctx context.Context, q, from, to string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("%w: nil context", ErrConfig)
	}
	if strings.TrimSpace(oa.Model) == "" || strings.TrimSpace(to) == "" || strings.TrimSpace(q) == "" || len(q) > maxInputBytes {
		return "", fmt.Errorf("%w: model, target language and nonempty bounded text required", ErrConfig)
	}
	mode, err := normalizeMode(oa.APIMode)
	if err != nil {
		return "", err
	}
	endpoint, err := normalizeEndpoint(oa.APIUrl, mode)
	if err != nil {
		return "", err
	}
	if oa.APIKey == "" && (oa.APIUrl == "" || endpoint.Hostname() == "api.openai.com") {
		return "", fmt.Errorf("%w: API key required for official endpoint", ErrConfig)
	}
	timeout := defaultTimeout
	if oa.Timeout != "" {
		timeout, err = time.ParseDuration(oa.Timeout)
		if err != nil || timeout <= 0 || timeout > maxTimeout {
			return "", fmt.Errorf("%w: invalid openai-timeout", ErrConfig)
		}
	}
	systemPrompt := oa.Prompt
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	if from == "" {
		from = "auto"
	}
	userPrompt := fmt.Sprintf("Translate the following text from %s to %s. Return only the translation.\n\n%s", from, to, q)
	body := make(map[string]any, 4)
	body["model"] = oa.Model
	body["stream"] = oa.Stream
	if mode == "responses" {
		body["instructions"] = systemPrompt
		body["input"] = userPrompt
	} else {
		body["messages"] = []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": userPrompt}}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if oa.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+oa.APIKey)
		}
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: request transport: %v", ErrUpstream, err)
		}
		if response.StatusCode != http.StatusOK {
			status := response.StatusCode
			wait, retry := retryDelay(response, attempt)
			io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			response.Body.Close()
			if retry {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
					continue
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return "", ctx.Err()
				}
			}
			return "", fmt.Errorf("%w: HTTP %d", ErrUpstream, status)
		}
		translated, decodeErr := decodeResponse(response.Body, response.Header.Get("Content-Type"), mode)
		response.Body.Close()
		if decodeErr != nil {
			return "", decodeErr
		} // never retry a started generation
		translated = strings.TrimSpace(translated)
		if translated == "" {
			return "", ErrIncomplete
		}
		return translated, nil
	}
	return "", ErrUpstream
}

func normalizeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "chat", "chat_completions", "chat-completions":
		return "chat_completions", nil
	case "responses", "response":
		return "responses", nil
	default:
		return "", fmt.Errorf("%w: unsupported openai-api-mode", ErrConfig)
	}
}

func normalizeEndpoint(raw, mode string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "https://api.openai.com/v1"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid openai-api-url", ErrConfig)
	}
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	if mode == "responses" {
		u.Path = path + "/responses"
	} else {
		u.Path = path + "/chat/completions"
	}
	return u, nil
}

func retryDelay(resp *http.Response, attempt int) (time.Duration, bool) {
	if attempt != 0 {
		return 0, false
	}
	switch resp.StatusCode {
	case 429, 502, 503, 504:
	default:
		return 0, false
	}
	header := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if header != "" {
		if sec, err := time.ParseDuration(header + "s"); err == nil {
			if sec >= 0 && sec <= maxRetryWait {
				return sec, true
			}
			return 0, false
		}
		if date, err := http.ParseTime(header); err == nil {
			delay := time.Until(date)
			if delay < 0 {
				delay = 0
			}
			if delay <= maxRetryWait {
				return delay, true
			}
		}
		return 0, false
	}
	return 250 * time.Millisecond, true
}
