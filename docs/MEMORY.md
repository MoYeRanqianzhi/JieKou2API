# JieKou2API - Shared Memory

## Implementation Status (2026-04-21)

### v0.1.0 - Core Reverse Proxy
- OpenAI-compatible `/v1/chat/completions` endpoint (stream + non-stream)
- Multi-account round-robin with circuit breaker (auths/*.json)
- Hot-reload: fsnotify + 15s poll for config.yaml and auths/ directory
- SSE format conversion: JieKou `text-delta` → OpenAI `chat.completion.chunk`
- Error handling: JieKou SSE error events (e.g. `FREE_TRIAL_REQUEST_COUNT_EXCEEDED`)
- Middleware: CORS, logging, recovery, optional API key auth

### Known Limitations
- JieKou free trial has per-account request quota (resets periodically)
- SSE error event (`type: "error"`) returned inside HTTP 200 stream — handled in sse.go
- Extended thinking (`enable_thinking`) not yet exposed to clients
- Tool/function calling pass-through not yet tested

## API Exploration Results (2026-04-21)

### Core Endpoint
- **URL**: `POST https://jiekou.ai/api/free-trial/chat`
- **Auth**: Cookie-based (`token=<jwt>`, obtained via GitHub OAuth)
- **Response**: SSE stream (`text/event-stream`)

### Request Format
```json
{
  "system_content": "system prompt here",
  "response_format": {"type": "text"},
  "max_tokens": 4096,
  "temperature": 1,
  "presence_penalty": 0,
  "frequency_penalty": 0,
  "min_p": 0,
  "top_k": 50,
  "model": "claude-opus-4-7",
  "tools": [],
  "enable_thinking": true,
  "isReasoningModel": true,
  "id": "playground-none",
  "messages": [
    {
      "parts": [{"type": "text", "text": "user message"}],
      "id": "unique-msg-id",
      "role": "user"
    }
  ],
  "trigger": "submit-message"
}
```

### SSE Response Events
```
data: {"type":"start"}
data: {"type":"start-step"}
data: {"type":"text-start","id":"txt-0"}
data: {"type":"text-delta","id":"txt-0","delta":"token text","providerMetadata":{"provider":{"tps":79.96,"ttft_ms":2945}}}
data: {"type":"text-end","id":"txt-0"}
data: {"type":"finish-step"}
data: {"type":"finish"}
data: [DONE]
```

### Key Observations
- No Authorization header needed in chat request; auth is cookie-based
- Cookie `token` value = the same Bearer token used in other API calls
- `enable_thinking: true` + `isReasoningModel: true` enables extended thinking
- Message format uses `parts` array (not OpenAI's `content` string)
- Each message needs a unique `id` field
- `trigger: "submit-message"` is required

### Reverse Proxy Technical Path
1. Expose OpenAI-compatible `/v1/chat/completions` endpoint
2. Convert OpenAI message format → JieKou `parts` format
3. Forward to `https://jiekou.ai/api/free-trial/chat` with cookie auth
4. Convert JieKou SSE → OpenAI SSE format
5. Handle cookie/token lifecycle (GitHub OAuth login → token extraction)
