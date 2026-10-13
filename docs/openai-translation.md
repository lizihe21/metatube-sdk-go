# OpenAI translation protocol compatibility (G5)

The existing `GET /v1/translate` and `POST /v1/translate` return a single
JSON response containing `data.translated_text`. G5 adds support for
two upstream OpenAI-compatible APIs with either JSON or Server-Sent Events
(SSE). **The public MetaTube HTTP response is still JSON**: even when
`openai-stream=true`, upstream chunks are collected into one validated result.

## Configuration

```json
{
  "engine": "OpenAI",
  "q": "Example text",
  "from": "en",
  "to": "zh-CN",
  "openai-api-key": "YOUR_API_KEY",
  "openai-api-url": "https://api.openai.com/v1",
  "openai-model": "YOUR_MODEL",
  "openai-api-mode": "chat_completions",
  "openai-stream": true,
  "openai-timeout": "90s"
}
```

- `openai-api-mode`: `chat_completions` (default; aliases `chat`,
  `chat-completions`), or `responses` (alias `response`).
- `openai-stream`: JSON boolean, defaults to `false`. Set `true` to
  request upstream SSE; tolerant parsing accepts JSON even if the provider
  ignores `stream=true`, and SSE even if the provider ignores
  `stream=false`.
- `openai-timeout`: Go duration such as `90s`, default `60s`, maximum
  `180s`. Incoming request cancellation propagates to OpenAI.
- `openai-api-url`: base URL, e.g. `https://gateway.example/v1`, or
  a full `/chat/completions` or `/responses` path. Existing path prefix
  is kept; the selected mode determines the final endpoint.
- The default prompt is unchanged. `openai-prompt` overrides it.
- API keys for the official OpenAI endpoint are required, and the official endpoint must use HTTPS. For compatible
  local APIs, an empty key is allowed if the provider permits it.

### Responses API example

```json
{
  "engine": "OpenAI",
  "q": "你好",
  "from": "zh",
  "to": "en",
  "openai-api-key": "YOUR_API_KEY",
  "openai-api-url": "https://api.openai.com/v1",
  "openai-model": "YOUR_RESPONSES_MODEL",
  "openai-api-mode": "responses",
  "openai-stream": true
}
```

## Supported upstream responses

| Mode | JSON | SSE |
|---|---|---|
| Chat Completions | `choices[].message.content` string / text parts, legacy `choices[].text` | `choices[].delta.content`, `[DONE]` |
| Responses | `output[].content[].text` (for `type=output_text`), optional `output_text` | `response.output_text.delta`, `response.completed` |

Parsing treats `finish_reason=length`, content filtering, refusals,
incomplete Responses, incomplete streams, malformed response bodies
and empty output as errors. Responses `completed` snapshot text is not
duplicated with previously received deltas. It ignores Chat usage-only
SSE chunks, SSE comments, CRLF, and multiline `data:` records. Gateway compatibility also covers mislabeled\nJSON/SSE response MIME types and the legacy `choices[].text` field.

Both JSON and SSE responses have an 8 MiB overall read cap; SSE lines
are individually capped at 1 MiB. Requests are capped at 256 KiB of text.
Only early HTTP 429/502/503/504 responses are eligible for **one**
additional attempt; `Retry-After` is honored when within 4 seconds,
and longer waits stop rather than retry immediately. There are no
automatic retries after a successful HTTP response has begun generating
content or after transport ambiguity.

## Validation

Run offline protocol tests:

```sh
go test -race -count=5 ./translate/openai
go vet ./translate/openai
```

The previous live API test is now opt-in:

```sh
OPENAI_TRANSLATION_INTEGRATION=1 OPENAI_API_KEY=... \
  OPENAI_API_URL=... go test ./translate/openai -run '^TestOpenaiTranslate$' -v
```

**Security:** Use authenticated POST, never pass credentials through URL
queries. Avoid exposing custom API URL selection to untrusted anonymous
clients; gateways and reverse proxies must not log authorization headers
or request bodies. This feature does not create server-managed named
backends or provide downstream token-by-token streaming.
