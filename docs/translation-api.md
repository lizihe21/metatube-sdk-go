# Translation endpoint migration (G1)

A previous route exposed `GET /v1/translate` under the long-lived public
cache group, and query parameters can contain `openai-api-key`.
The endpoint now belongs to the authenticated API group (authentication
applies whenever a server token validator is configured) and emits
`Cache-Control: no-store`. The default access logger strips all query
parameters.

New clients should use **POST /v1/translate** with one JSON object:

```json
{
  "q": "Example text",
  "from": "auto",
  "to": "zh-CN",
  "engine": "OpenAI",
  "openai-api-key": "YOUR_KEY",
  "openai-api-url": "https://api.openai.com/v1",
  "openai-model": "YOUR_MODEL"
}
```

Provide the service's bearer Authorization header when server
authentication is enabled. The success response retains the previous
`data.from`, `data.to` and `data.translated_text` fields.

The legacy GET endpoint is retained for existing clients, but passing
credentials in query parameters is **deprecated**: browsers and
reverse proxies may log them even though this service's own logger does
not. POST request bodies should also be excluded from application and
proxy logs. A configured server authentication token should be used
before exposing the service to the Internet; deployments without a
validator still have an unauthenticated application API.

Server-managed named translation backends and removal of caller-defined
upstream destinations are separate hardening tasks. Do not expose
anonymous OpenAI proxying to the public Internet.
