# TODO

## Phase 1: Core Reverse Proxy
- [x] Explore JieKou free-trial chat API endpoint
- [x] Capture request/response format
- [x] Document technical path
- [x] Implement OpenAI-compatible `/v1/chat/completions` endpoint
- [x] Implement message format conversion (OpenAI ↔ JieKou)
- [x] Implement SSE stream conversion (JieKou SSE → OpenAI SSE)
- [x] Implement cookie/token management (auths/*.json)
- [x] Stream mode tested and working
- [x] SSE error event handling (quota exceeded etc.)
- [ ] Test with Claude Code (待额度恢复后测试)

## Phase 2: Enhancements
- [ ] Extended thinking support
- [ ] Tool use / function calling pass-through
- [ ] Multi-turn conversation support
- [ ] Token refresh / auto re-login
- [ ] Error handling & retry logic
