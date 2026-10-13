package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"strings"
)

func decodeResponse(body io.Reader, contentType, mode string) (string, error) {
	reader := bufio.NewReader(io.LimitReader(body, maxResponseBytes+1))
	mimeType, _, _ := mime.ParseMediaType(contentType)
	peek, _ := reader.Peek(32)
	trimmed := bytes.TrimSpace(peek)
	// Some compatible gateways send JSON while retaining the event-stream
	// MIME type (or SSE while reporting application/json). Sniff the actual
	// body before using the Content-Type as a fallback.
	startsJSON := bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("["))
	if !startsJSON && (mimeType == "text/event-stream" || bytes.HasPrefix(trimmed, []byte("data:")) ||
		bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte(":"))) {
		return decodeSSE(reader, mode)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", ErrProtocol, err)
	}
	if int64(len(data)) > maxResponseBytes {
		return "", fmt.Errorf("%w: oversized JSON response", ErrProtocol)
	}
	if mode == "responses" {
		return decodeResponsesJSON(data)
	}
	return decodeChatJSON(data)
}

// readMessageContent accommodates standard Chat Completions' string content as
// well as text content-part arrays returned by several compatible gateways.
func readMessageContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("%w: invalid content type", ErrProtocol)
	}
	var out strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" || part.Type == "" {
			out.WriteString(part.Text)
		}
	}
	return out.String(), nil
}

type chatChunk struct {
	Choices []struct {
		Index        int     `json:"index"`
		FinishReason *string `json:"finish_reason"`
		Message      struct {
			Content json.RawMessage `json:"content"`
			Refusal json.RawMessage `json:"refusal"`
		} `json:"message"`
		Delta struct {
			Content json.RawMessage `json:"content"`
			Refusal json.RawMessage `json:"refusal"`
		} `json:"delta"`
		Text string `json:"text"`
	} `json:"choices"`
	Error json.RawMessage `json:"error"`
}

func isRefusal(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != `""`
}

func checkFinish(reason *string) error {
	if reason == nil || *reason == "" || *reason == "stop" {
		return nil
	}
	if *reason == "content_filter" {
		return ErrRefusal
	}
	return fmt.Errorf("%w: finish_reason=%s", ErrIncomplete, *reason)
}

func decodeChatJSON(data []byte) (string, error) {
	var chunk chatChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return "", fmt.Errorf("%w: invalid Chat JSON", ErrProtocol)
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return "", ErrUpstream
	}
	if len(chunk.Choices) == 0 {
		return "", ErrIncomplete
	}
	c := chunk.Choices[0]
	if isRefusal(c.Message.Refusal) {
		return "", ErrRefusal
	}
	if err := checkFinish(c.FinishReason); err != nil {
		return "", err
	}
	text, err := readMessageContent(c.Message.Content)
	if err != nil {
		return "", err
	}
	if text == "" {
		text = c.Text
	} // legacy completion-compatible gateways
	if strings.TrimSpace(text) == "" {
		return "", ErrIncomplete
	}
	return text, nil
}

type responseJSON struct {
	Status     string `json:"status"`
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Error json.RawMessage `json:"error"`
}

func decodeResponsesJSON(data []byte) (string, error) {
	var resp responseJSON
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("%w: invalid Responses JSON", ErrProtocol)
	}
	switch resp.Status {
	case "failed":
		return "", ErrUpstream
	case "incomplete", "cancelled":
		return "", ErrIncomplete
	case "", "completed":
	default:
		return "", fmt.Errorf("%w: response status=%s", ErrIncomplete, resp.Status)
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		return "", ErrUpstream
	}
	var out strings.Builder
	for _, item := range resp.Output {
		if item.Type != "" && item.Type != "message" {
			continue
		}
		if item.Role != "" && item.Role != "assistant" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "refusal" || part.Refusal != "" {
				return "", ErrRefusal
			}
			if part.Type == "output_text" || part.Type == "text" {
				out.WriteString(part.Text)
			}
		}
	}
	if out.Len() == 0 {
		out.WriteString(resp.OutputText)
	} // vendor-specific convenient field
	if strings.TrimSpace(out.String()) == "" {
		return "", ErrIncomplete
	}
	return out.String(), nil
}

func decodeSSE(reader io.Reader, mode string) (string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20) // per-line limit; total 8MiB above
	var dataLines []string
	var eventName string
	var out strings.Builder
	chatTerminal, responseComplete := false, false
	consume := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		name := eventName
		eventName = ""
		if strings.TrimSpace(data) == "[DONE]" {
			chatTerminal = true
			return nil
		}
		if mode == "responses" {
			var event struct {
				Type     string          `json:"type"`
				Delta    string          `json:"delta"`
				Text     string          `json:"text"`
				Response json.RawMessage `json:"response"`
				Error    json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return fmt.Errorf("%w: invalid Responses SSE", ErrProtocol)
			}
			if event.Type == "" {
				event.Type = name
			}
			switch event.Type {
			case "response.output_text.delta":
				out.WriteString(event.Delta)
			case "response.output_text.done":
				if out.Len() == 0 {
					out.WriteString(event.Text)
				}
			case "response.refusal.delta", "response.refusal.done":
				return ErrRefusal
			case "response.failed", "error":
				return ErrUpstream
			case "response.incomplete":
				return ErrIncomplete
			case "response.completed":
				responseComplete = true
				if len(event.Response) > 0 && string(event.Response) != "null" {
					var snapshot responseJSON
					if err := json.Unmarshal(event.Response, &snapshot); err != nil {
						return fmt.Errorf("%w: malformed completed snapshot", ErrProtocol)
					}
					if snapshot.Status != "" && snapshot.Status != "completed" {
						return ErrIncomplete
					}
					if len(snapshot.Error) > 0 && string(snapshot.Error) != "null" {
						return ErrUpstream
					}
					// Even when text deltas exist, final content can contain
					// a refusal. Reject it without duplicating the snapshot.
					for _, item := range snapshot.Output {
						for _, part := range item.Content {
							if part.Type == "refusal" || part.Refusal != "" {
								return ErrRefusal
							}
						}
					}
					if out.Len() == 0 && (len(snapshot.Output) > 0 || snapshot.OutputText != "") {
						full, err := decodeResponsesJSON(event.Response)
						if err != nil {
							return err
						}
						out.WriteString(full)
					}
				}
			}
			return nil
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("%w: invalid Chat SSE", ErrProtocol)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return ErrUpstream
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if isRefusal(choice.Delta.Refusal) {
				return ErrRefusal
			}
			if err := checkFinish(choice.FinishReason); err != nil {
				return err
			}
			delta, err := readMessageContent(choice.Delta.Content)
			if err != nil {
				return err
			}
			if delta == "" {
				delta = choice.Text // legacy completion-compatible streaming gateways
			}
			out.WriteString(delta)
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := consume(); err != nil {
				return "", err
			}
			if chatTerminal || responseComplete {
				break
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("%w: SSE stream read: %v", ErrProtocol, err)
	}
	// A complete SSE frame without a terminating empty line is still a
	// dispatchable frame; it cannot by itself imply stream completion.
	if len(dataLines) > 0 {
		if err := consume(); err != nil {
			return "", err
		}
	}
	if mode == "responses" {
		if !responseComplete {
			return "", ErrIncomplete
		}
	} else if !chatTerminal {
		return "", ErrIncomplete
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", ErrIncomplete
	}
	return out.String(), nil
}
