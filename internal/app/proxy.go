package app

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"time"
)

type ProxyHandler struct {
	reloader *Reloader
	client   *http.Client
	keys     *KeyPool
}

func NewProxyHandler(reloader *Reloader, pool *KeyPool) *ProxyHandler {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 10 * time.Minute,
			IdleConnTimeout:       120 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			ForceAttemptHTTP2:     true,
		},
	}
	return &ProxyHandler{
		reloader: reloader,
		client:   httpClient,
		keys:     pool,
	}
}

type streamHandler func(w http.ResponseWriter, resp *http.Response, model string)
type errorWriter func(w http.ResponseWriter, status int, msg string)

func (p *ProxyHandler) forwardToUpstream(w http.ResponseWriter, r *http.Request, jkReq *JieKouRequest, model string, isStream bool, onStream, onNonStream streamHandler, onError errorWriter) {
	cfg := p.reloader.Current()

	const maxRetriesCap = 3
	healthy := p.keys.HealthySize()
	maxRetries := maxRetriesCap
	if healthy > 0 && healthy < maxRetries {
		maxRetries = healthy
	}
	if maxRetries < 1 {
		maxRetries = 1
	}

	tried := make(map[int]struct{}, maxRetries)
	lastStatus := 0

	for attempt := 0; attempt < maxRetries; attempt++ {
		token, keyIdx := p.selectKey(tried)
		if keyIdx == -1 {
			break
		}
		tried[keyIdx] = struct{}{}
		log.Printf("→ upstream key[%d]=%s (attempt %d/%d)", keyIdx, fingerprint(token), attempt+1, maxRetries)

		jkBody, err := json.Marshal(jkReq)
		if err != nil {
			onError(w, http.StatusInternalServerError, "Failed to encode request")
			return
		}

		targetURL := cfg.Upstream.BaseURL + "/api/free-trial/chat"
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(jkBody))
		if err != nil {
			onError(w, http.StatusInternalServerError, "Failed to create upstream request")
			return
		}
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Cookie", "token="+token)
		upstream.Header.Set("Referer", cfg.Upstream.BaseURL+"/models-console/llm-playground?model="+model)
		upstream.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36")

		resp, err := p.client.Do(upstream)
		if err != nil {
			p.keys.MarkFailure(keyIdx)
			log.Printf("retry %d: upstream net err key[%d]=%s: %v", attempt+1, keyIdx, fingerprint(token), err)
			lastStatus = http.StatusBadGateway
			continue
		}

		if isRetryableStatus(resp.StatusCode) {
			p.keys.MarkFailure(keyIdx)
			log.Printf("retry %d: upstream status %d key[%d]=%s — trying next",
				attempt+1, resp.StatusCode, keyIdx, fingerprint(token))
			lastStatus = resp.StatusCode
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue
		}

		if resp.StatusCode < 400 {
			p.keys.MarkSuccess(keyIdx)
		}

		if resp.StatusCode == http.StatusBadRequest {
			resp.Body.Close()
			onError(w, http.StatusBadRequest, "请求被上游拒绝")
			return
		}

		if resp.StatusCode == http.StatusOK {
			if isStream {
				onStream(w, resp, model)
			} else {
				onNonStream(w, resp, model)
			}
			resp.Body.Close()
			return
		}

		resp.Body.Close()
		lastStatus = resp.StatusCode
	}

	if len(tried) == 0 {
		if p.keys.Size() == 0 {
			onError(w, http.StatusServiceUnavailable, "号池无可用账号，请在 auths/ 目录添加登录态文件")
			return
		}
		onError(w, http.StatusServiceUnavailable, "所有上游账号均已熔断，请稍后重试")
		return
	}

	status, sanitized := sanitizeUpstreamError(lastStatus)
	if sanitized == "" {
		status = http.StatusBadGateway
		sanitized = "上游服务异常，请稍后重试"
	}
	onError(w, status, sanitized)
}

func (p *ProxyHandler) selectKey(tried map[int]struct{}) (string, int) {
	key, idx := p.keys.Next()
	if idx == -1 {
		return "", -1
	}
	n := p.keys.Size()
	for i := 0; i < n; i++ {
		if _, seen := tried[idx]; !seen {
			return key, idx
		}
		key, idx = p.keys.Next()
		if idx == -1 {
			return "", -1
		}
	}
	return "", -1
}

// --- OpenAI-compatible handler ---

func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"Failed to read request body","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var oaiReq OpenAIRequest
	if err := json.Unmarshal(body, &oaiReq); err != nil {
		http.Error(w, `{"error":{"message":"Invalid JSON","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return
	}

	cfg := p.reloader.Current()
	isStream := oaiReq.Stream != nil && *oaiReq.Stream
	jkReq := ConvertRequest(&oaiReq, cfg.Upstream.DefaultModel)

	p.forwardToUpstream(w, r, jkReq, jkReq.Model, isStream,
		handleStreamResponse, handleNonStreamResponse,
		func(w http.ResponseWriter, status int, msg string) {
			writeJSON(w, status, `{"error":{"message":"`+msg+`","type":"upstream_error"}}`)
		},
	)
}

// --- Claude (Anthropic) handler ---

func (p *ProxyHandler) ServeClaudeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(NewClaudeError("invalid_request_error", "Method not allowed"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(NewClaudeError("invalid_request_error", "Failed to read request body"))
		return
	}
	defer r.Body.Close()

	var claudeReq ClaudeRequest
	if err := json.Unmarshal(body, &claudeReq); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(NewClaudeError("invalid_request_error", "Invalid JSON"))
		return
	}

	cfg := p.reloader.Current()
	isStream := claudeReq.Stream != nil && *claudeReq.Stream
	jkReq := ConvertClaudeRequest(&claudeReq, cfg.Upstream.DefaultModel)

	p.forwardToUpstream(w, r, jkReq, jkReq.Model, isStream,
		handleClaudeStreamResponse, handleClaudeNonStreamResponse,
		func(w http.ResponseWriter, status int, msg string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(NewClaudeError("api_error", msg))
		},
	)
}

func isRetryableStatus(status int) bool {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
		status == http.StatusForbidden,
		status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	}
	return false
}

func sanitizeUpstreamError(status int) (int, string) {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
		status == http.StatusForbidden:
		return http.StatusServiceUnavailable, "上游账号不可用，请稍后重试"
	case status == http.StatusTooManyRequests:
		return http.StatusServiceUnavailable, "上游限流，请稍后重试"
	case status >= 500:
		return http.StatusBadGateway, "上游服务异常，请稍后重试"
	}
	return status, ""
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}
