# TODO

## Phase 1: Core Reverse Proxy ✅
- [x] Explore JieKou free-trial chat API endpoint
- [x] Capture request/response format
- [x] Implement OpenAI-compatible `/v1/chat/completions` endpoint
- [x] Implement SSE stream conversion (JieKou SSE → OpenAI SSE)
- [x] Implement cookie/token management (auths/*.json)
- [x] SSE error event handling (quota exceeded etc.)

## Phase 2: Anthropic API + Admin + OAuth ✅
- [x] Anthropic Messages API `/v1/messages` (stream + non-stream)
- [x] Models endpoint `/v1/models`
- [x] Admin UI (B&W minimal design, /admin/)
- [x] Admin API (status, config, keys, reload)
- [x] OAuth login flow (GitHub → JieKou → API key)
- [x] API key generation (sk-ant-api01-xxx format)
- [x] x-api-key header support (Anthropic style)
- [x] Embedded static assets (go:embed)

## Phase 3: Hardening & Testing
- [ ] Test with Claude Code (ANTHROPIC_BASE_URL=http://localhost:8080)
- [ ] Verify max_tokens > 4096 works (前端限制 vs 后端限制)
- [ ] Extended thinking support (enable_thinking pass-through)
- [ ] Tool use / function calling pass-through
- [ ] Token refresh / auto re-login
- [ ] Error handling improvements
- [ ] Dockerfile + docker-compose
