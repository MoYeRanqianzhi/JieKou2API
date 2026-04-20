package app

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
)

const jiekeouOfficialAPI = "https://api.jiekou.ai"

// forwardPassthrough forwards the raw request body to JieKou official API
// using the client's own API key. The response is streamed back transparently.
//
// endpoint is the upstream path, e.g. "/openai/chat/completions" or "/v1/messages".
func forwardPassthrough(w http.ResponseWriter, r *http.Request, body []byte, client *http.Client, token, endpoint string) {
	targetURL := jiekeouOfficialAPI + endpoint
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"Failed to create request: %s","type":"server_error"}}`, err.Error()), http.StatusInternalServerError)
		return
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "jiekou2api/1.0")

	// Pass through Anthropic headers if present
	if v := r.Header.Get("anthropic-version"); v != "" {
		req.Header.Set("anthropic-version", v)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("passthrough error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":"Upstream error: %s","type":"server_error"}}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream response with flushing for SSE
	if flusher, ok := w.(http.Flusher); ok {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				flusher.Flush()
			}
			if rerr == io.EOF {
				return
			}
			if rerr != nil {
				log.Printf("passthrough stream read: %v", rerr)
				return
			}
		}
	}
	io.Copy(w, resp.Body)
}
