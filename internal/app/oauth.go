package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type OAuthHandler struct {
	reloader  *Reloader
	pool      *KeyPool
	client    *http.Client
	pollCache sync.Map
}

func NewOAuthHandler(reloader *Reloader, pool *KeyPool) *OAuthHandler {
	return &OAuthHandler{
		reloader: reloader,
		pool:     pool,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OAuthHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/public/oauth/start", o.handleStart)
	mux.HandleFunc("/public/oauth/poll", o.handlePoll)
	mux.HandleFunc("/public/github/config", o.handleGitHubConfig)
}

func (o *OAuthHandler) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	fpID := "fp_pub_" + randomHex(16)
	cfg := o.reloader.Current()

	reqBody := map[string]string{"fingerprintId": fpID}
	bodyBytes, _ := json.Marshal(reqBody)

	apiURL := strings.TrimRight(cfg.Upstream.BaseURL, "/")
	apiURL = strings.Replace(apiURL, "://jiekou.ai", "://api-server.jiekou.ai", 1)

	resp, err := o.client.Post(apiURL+"/api/auth/cli/code", "application/json", strings.NewReader(string(bodyBytes)))
	if err != nil {
		log.Printf("oauth start: upstream error: %v", err)
		http.Error(w, `{"error":"failed to initiate OAuth"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Printf("oauth start: upstream %d: %s", resp.StatusCode, string(data))
		http.Error(w, `{"error":"upstream rejected OAuth request"}`, http.StatusBadGateway)
		return
	}

	var upstream map[string]any
	if err := json.Unmarshal(data, &upstream); err != nil {
		http.Error(w, `{"error":"invalid upstream response"}`, http.StatusBadGateway)
		return
	}

	loginURL, _ := upstream["loginUrl"].(string)
	fpHash, _ := upstream["fingerprintHash"].(string)

	var expiresAt string
	switch v := upstream["expiresAt"].(type) {
	case string:
		expiresAt = v
	case float64:
		expiresAt = fmt.Sprintf("%.0f", v)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"fingerprint_id":   fpID,
		"fingerprint_hash": fpHash,
		"login_url":        loginURL,
		"expires_at":       expiresAt,
	})
}

type pollResult struct {
	Email  string `json:"email,omitempty"`
	APIKey string `json:"api_key,omitempty"`
	Done   bool   `json:"done"`
}

func (o *OAuthHandler) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	fp := r.URL.Query().Get("fp")
	fph := r.URL.Query().Get("fph")
	exp := r.URL.Query().Get("exp")
	if fp == "" || fph == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}

	if cached, ok := o.pollCache.Load(fp); ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}

	cfg := o.reloader.Current()
	apiURL := strings.TrimRight(cfg.Upstream.BaseURL, "/")
	apiURL = strings.Replace(apiURL, "://jiekou.ai", "://api-server.jiekou.ai", 1)

	params := url.Values{
		"fingerprintId":   {fp},
		"fingerprintHash": {fph},
		"expiresAt":       {exp},
	}
	resp, err := o.client.Get(apiURL + "/api/auth/cli/status?" + params.Encode())
	if err != nil {
		log.Printf("oauth poll: upstream error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pollResult{Done: false})
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var upstream map[string]any
	if err := json.Unmarshal(data, &upstream); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pollResult{Done: false})
		return
	}

	if pending, _ := upstream["pending"].(bool); pending {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pollResult{Done: false})
		return
	}

	user, _ := upstream["user"].(map[string]any)
	if user == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pollResult{Done: false})
		return
	}

	authToken, _ := user["authToken"].(string)
	email, _ := user["email"].(string)
	name, _ := user["name"].(string)

	if authToken == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pollResult{Done: false})
		return
	}

	label := sanitizeLabel(email)
	if label == "" || label == "unnamed" {
		label = sanitizeLabel(name)
	}
	if label == "" || label == "unnamed" {
		label = "oauth-" + randomHex(3)
	}

	dir := cfg.Auth.Dir
	os.MkdirAll(dir, 0o755)
	af := authFile{Token: authToken, Label: label}
	afData, _ := json.MarshalIndent(af, "", "  ")
	path := filepath.Join(dir, label+".json")
	if err := os.WriteFile(path, afData, 0o644); err != nil {
		log.Printf("oauth: save auth file: %v", err)
	}
	o.reloader.Reload("oauth-login")

	apiKey := GenerateAPIKey()

	result := pollResult{
		Email:  maskEmail(email),
		APIKey: apiKey,
		Done:   true,
	}
	o.pollCache.Store(fp, result)

	go func() {
		time.Sleep(10 * time.Minute)
		o.pollCache.Delete(fp)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (o *OAuthHandler) handleGitHubConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"repo": "JieKou2API",
	})
}

func maskEmail(email string) string {
	at := strings.Index(email, "@")
	if at < 0 {
		return "***"
	}
	if at <= 2 {
		return "***" + email[at:]
	}
	return email[:2] + "***" + email[at:]
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
