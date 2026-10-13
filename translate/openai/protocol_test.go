package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatJSONAndExistingConfiguration(t *testing.T) {
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer key" || r.Method != "POST" {
			t.Errorf("request: %v %q", r.URL, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"index":0,"message":{"content":"你好"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	oa := &OpenAI{APIKey: "key", APIUrl: server.URL + "/v1", Model: "mock-model"}
	txt, err := oa.Translate("Hello", "en", "zh-CN")
	if err != nil || txt != "你好" {
		t.Fatalf("result=%q err=%v", txt, err)
	}
	if request.Model != "mock-model" || request.Stream || len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "Hello") || !strings.Contains(request.Messages[1].Content, "zh-CN") {
		t.Fatalf("bad payload: %+v", request)
	}
}

func TestChatContentArrayAndURLNormalization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.Path)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":[{"type":"text","text":"第一"},{"type":"text","text":"第二"}]},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL + "/proxy/v1/chat/completions", Model: "model"}
	txt, err := o.Translate("a", "auto", "zh")
	if err != nil || txt != "第一第二" {
		t.Fatalf("%q %v", txt, err)
	}
}

func TestChatSSEWithCommentsCRLFUsageAndSplitChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream=%v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		for _, piece := range []string{
			": heartbeat\r\n\r\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你\"},\"finish_reason\":null}]}\r\n\r\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		} {
			io.WriteString(w, piece)
			f.Flush()
		}
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", Stream: true}
	got, err := o.Translate("hi", "en", "zh-CN")
	if got != "你好" || err != nil {
		t.Fatalf("%q %v", got, err)
	}
}

func TestChatSSEMultilineDataAndMislabelledMIME(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "data: {\"choices\":[\n")
		io.WriteString(w, "data: {\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test"}
	got, err := o.Translate("x", "en", "fr")
	if got != "OK" || err != nil {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResponseJSONAndRequestsMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("url=%s", r.URL)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["instructions"] == nil || req["input"] == nil || req["messages"] != nil || req["stream"] != false {
			t.Errorf("payload=%v", req)
		}
		io.WriteString(w, `{"status":"completed","output":[{"type":"reasoning"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Bonjour "},{"type":"output_text","text":"à tous"}]}]}`)
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL + "/v1", Model: "test", APIMode: "responses"}
	got, err := o.Translate("Hello", "en", "fr")
	if err != nil || got != "Bonjour à tous" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResponsesSSEDoesNotDoubleAppendFinalSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"测试\"}\n\n")
		fmt.Fprint(w, "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"测试\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"测试\"}]}]}}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: "responses", Stream: true}
	got, err := o.Translate("x", "en", "zh")
	if err != nil || got != "测试" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestIncompleteFailedAndTruncatedResults(t *testing.T) {
	cases := []struct{ name, mode, mime, body string }{
		{"chat-length", "chat_completions", "application/json", `{"choices":[{"message":{"content":"short"},"finish_reason":"length"}]}`},
		{"chat-refusal", "chat_completions", "application/json", `{"choices":[{"message":{"refusal":"no","content":null},"finish_reason":"stop"}]}`},
		{"chat-empty", "chat_completions", "application/json", `{"choices":[]}`},
		{"chat-cut-stream", "chat_completions", "text/event-stream", `data: {"choices":[{"index":0,"delta":{"content":"prefix"}}]}` + "\n\n"},
		{"chat-filter-stream", "chat_completions", "text/event-stream", `data: {"choices":[{"index":0,"delta":{"content":"prefix"},"finish_reason":"content_filter"}]}` + "\n\ndata: [DONE]\n\n"},
		{"responses-incomplete", "responses", "application/json", `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`},
		{"responses-error", "responses", "text/event-stream", "event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n"},
		{"responses-cut-stream", "responses", "text/event-stream", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mime)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: tc.mode, Stream: tc.mime == "text/event-stream"}
			got, err := o.Translate("hi", "en", "fr")
			if err == nil || got != "" {
				t.Fatalf("accepted incomplete: %q %v", got, err)
			}
		})
	}
}

func TestRetriesOnlyEarlyTransientHTTPResponses(t *testing.T) {
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"Recovered"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test"}
	got, err := o.Translate("hi", "en", "fr")
	if err != nil || got != "Recovered" || n.Load() != 2 {
		t.Fatalf("got %q err=%v attempts=%d", got, err, n.Load())
	}
}

func TestNoRetryAfterBadStreamOrLongRetryAfter(t *testing.T) {
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", Stream: true}
	if _, err := o.Translate("hi", "en", "fr"); err == nil || n.Load() != 1 {
		t.Fatalf("unexpected retry on partial stream: %d %v", n.Load(), err)
	}
	var k atomic.Int32
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(429)
	}))
	defer server2.Close()
	o.APIUrl = server2.URL
	o.Stream = false
	if _, err := o.Translate("hi", "en", "fr"); err == nil || k.Load() != 1 {
		t.Fatalf("unexpected retry after long cooldown: %d %v", k.Load(), err)
	}
}

func TestContextCancellationStopsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond): // ensure fixture shutdown even if client cancels before headers
		}
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "model"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, err := o.TranslateContext(ctx, "hello", "en", "zh")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context deadline, got %v", err)
	}
}

func TestRejectsInvalidModeEndpointTimeoutAndMissingModel(t *testing.T) {
	for _, tc := range []OpenAI{
		{APIMode: "invalid", Model: "model"},
		{APIUrl: "file:///etc/passwd", Model: "model"},
		{APIUrl: "http://user:pass@localhost:8080", Model: "model"},
		{APIUrl: "http://localhost:8080/v1?key=secret", Model: "model"},
		{APIUrl: "http://localhost:8080", Model: "model", Timeout: "bad"},
		{APIUrl: "http://localhost:8080", Model: ""},
	} {
		if _, err := tc.Translate("hi", "en", "fr"); err == nil {
			t.Errorf("invalid config accepted: %+v", tc)
		}
	}
}

func TestJSONLimitAndSSEEventSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: "+strings.Repeat("a", 1024*1024+1)+"\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "model", Stream: true}
	if _, err := o.Translate("hi", "en", "fr"); err == nil {
		t.Fatal("oversized event accepted")
	}
}

func TestResponsesCompletedWithoutOutputAcceptsReceivedDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: "responses", Stream: true}
	got, err := o.Translate("x", "en", "fr")
	if err != nil || got != "partial" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestCancelDuringRetryWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "1"); w.WriteHeader(429) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	o := &OpenAI{APIUrl: server.URL, Model: "mock"}
	_, err := o.TranslateContext(ctx, "hi", "en", "fr")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestResponsesSSEDoneSnapshotOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"Hallo\"}\n\n")
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: "responses", Stream: true}
	got, err := o.Translate("x", "en", "de")
	if err != nil || got != "Hallo" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResponsesSnapshotRefusalOverridesEarlierPartialText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"declined\"}]}]}}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: "responses", Stream: true}
	got, err := o.Translate("hi", "en", "fr")
	if !errors.Is(err, ErrRefusal) || got != "" {
		t.Fatalf("refusal must override partial: %q %v", got, err)
	}
}

func TestChatSSERequiresEndMarkerEvenAfterStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "mock", Stream: true}
	_, err := o.Translate("hi", "en", "fr")
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("stream without [DONE] accepted: %v", err)
	}
}

func TestPartialContentHTTPStatusRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		io.WriteString(w, `{"choices":[{"message":{"content":"partial"}}]}`)
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "mock"}
	_, err := o.Translate("hi", "en", "fr")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("206 accepted: %v", err)
	}
}

func TestOfficialAPIEndpointCannotSendKeyOverHTTP(t *testing.T) {
	o := &OpenAI{APIKey: "test-secret", APIUrl: "http://api.openai.com/v1", Model: "model"}
	_, err := o.Translate("hi", "en", "fr")
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("insecure official API URL accepted: %v", err)
	}
}

func TestIncorrectEventStreamMIMEWithJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `{"choices":[{"index":0,"message":{"content":"Fallback JSON"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "model", Stream: true}
	got, err := o.Translate("hi", "en", "de")
	if err != nil || got != "Fallback JSON" {
		t.Fatalf("JSON body rejected because of incorrect header: %q %v", got, err)
	}
}

func TestResponsesSnapshotOutputSkeletonDoesNotDiscardDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[]}]}}\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "test", APIMode: "responses", Stream: true}
	got, err := o.Translate("hi", "en", "fr")
	if err != nil || got != "hi" {
		t.Fatalf("skeleton snapshot lost text: %q %v", got, err)
	}
}

func TestLegacyCompletionSSETextDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"text\":\"legacy\",\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	o := &OpenAI{APIUrl: server.URL, Model: "model", Stream: true}
	got, err := o.Translate("hi", "en", "de")
	if err != nil || got != "legacy" {
		t.Fatalf("legacy SSE text unsupported: %q %v", got, err)
	}
}
