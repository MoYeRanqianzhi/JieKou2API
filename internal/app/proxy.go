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

func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"Method not allowed","type":"invalid_request_error"}}`, http.StatusMethodNotAllowed)
		return
	}

	cfg := p.reloader.Current()

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

	isStream := oaiReq.Stream != nil && *oaiReq.Stream

	jkReq := ConvertRequest(&oaiReq, cfg.Upstream.DefaultModel)
	model := jkReq.Model

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
			http.Error(w, `{"error":{"message":"Failed to encode request","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}

		targetURL := cfg.Upstream.BaseURL + "/api/free-trial/chat"
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(jkBody))
		if err != nil {
			http.Error(w, `{"error":{"message":"Failed to create upstream request","type":"server_error"}}`, http.StatusInternalServerError)
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
			http.Error(w, `{"error":{"message":"请求被上游拒绝","type":"invalid_request"}}`, http.StatusBadRequest)
			return
		}

		if resp.StatusCode == http.StatusOK {
			if isStream {
				handleStreamResponse(w, resp, model)
			} else {
				handleNonStreamResponse(w, resp, model)
			}
			resp.Body.Close()
			return
		}

		resp.Body.Close()
		lastStatus = resp.StatusCode
	}

	if len(tried) == 0 {
		if p.keys.Size() == 0 {
			writeJSON(w, http.StatusServiceUnavailable,
				`{"error":{"message":"号池无可用账号，请在 auths/ 目录添加登录态文件","type":"pool_empty"}}`)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable,
			`{"error":{"message":"所有上游账号均已熔断，请稍后重试","type":"pool_all_broken"}}`)
		return
	}

	status, sanitized := sanitizeUpstreamError(lastStatus)
	if sanitized == "" {
		status = http.StatusBadGateway
		sanitized = `{"error":{"message":"上游服务异常，请稍后重试","type":"upstream_error"}}`
	}
	writeJSON(w, status, sanitized)
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
		return http.StatusServiceUnavailable,
			`{"error":{"message":"上游账号不可用，请稍后重试","type":"upstream_unavailable"}}`
	case status == http.StatusTooManyRequests:
		return http.StatusServiceUnavailable,
			`{"error":{"message":"上游限流，请稍后重试","type":"upstream_throttled"}}`
	case status >= 500:
		return http.StatusBadGateway,
			`{"error":{"message":"上游服务异常，请稍后重试","type":"upstream_error"}}`
	}
	return status, ""
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}
