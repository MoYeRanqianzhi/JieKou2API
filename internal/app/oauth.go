package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type PublicHandler struct {
	reloader *Reloader
	pool     *KeyPool
}

func NewPublicHandler(reloader *Reloader, pool *KeyPool) *PublicHandler {
	return &PublicHandler{reloader: reloader, pool: pool}
}

func (p *PublicHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/public/contribute", p.handleContribute)
	mux.HandleFunc("/public/donate", p.handleDonate)
	mux.HandleFunc("/public/upload", p.handleUpload)
}

type contributeResult struct {
	Label string `json:"label"`
	APIKey string `json:"api_key,omitempty"`
	OK    bool   `json:"ok"`
}

func (p *PublicHandler) parseTokenRequest(r *http.Request) (token, label string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		r.ParseMultipartForm(1 << 20)
		token = r.FormValue("token")
		label = r.FormValue("label")
	} else {
		var req struct {
			Token string `json:"token"`
			Label string `json:"label"`
		}
		if jerr := json.NewDecoder(r.Body).Decode(&req); jerr == nil {
			token = req.Token
			label = req.Label
		}
	}
	if token == "" {
		return "", "", fmt.Errorf("token is required")
	}
	return token, label, nil
}

func (p *PublicHandler) saveToken(token, label string, withKey bool) (*contributeResult, error) {
	if label == "" {
		label = "contrib-" + randomHex(4)
	} else {
		label = sanitizeLabel(label)
	}

	cfg := p.reloader.Current()
	dir := cfg.Auth.Dir
	os.MkdirAll(dir, 0o755)

	var apiKey string
	if withKey {
		apiKey = GenerateAPIKey()
	}

	af := authFile{Token: token, Label: label, DonorKey: apiKey}
	afData, _ := json.MarshalIndent(af, "", "  ")
	path := filepath.Join(dir, label+".json")
	if err := os.WriteFile(path, afData, 0o644); err != nil {
		return nil, err
	}
	p.reloader.Reload("contribute")

	if withKey {
		p.addAPIKeyToConfig(apiKey)
		log.Printf("contribute: saved token %s, issued key %s", label, fingerprint(apiKey))
	} else {
		log.Printf("upload: saved token %s (no key issued)", label)
	}

	return &contributeResult{Label: label, APIKey: apiKey, OK: true}, nil
}

// POST /public/contribute — 提交 token，返回 API Key（login.html 使用）
func (p *PublicHandler) handleContribute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	token, label, err := p.parseTokenRequest(r)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	result, err := p.saveToken(token, label, true)
	if err != nil {
		http.Error(w, `{"error":"failed to save token"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// POST /public/donate — 提交 token，返回 API Key（开发者集成用，等同 contribute）
func (p *PublicHandler) handleDonate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	token, label, err := p.parseTokenRequest(r)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	result, err := p.saveToken(token, label, true)
	if err != nil {
		http.Error(w, `{"error":"failed to save token"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// POST /public/upload — 仅上传 token，不返回 API Key
func (p *PublicHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	token, label, err := p.parseTokenRequest(r)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	result, err := p.saveToken(token, label, false)
	if err != nil {
		http.Error(w, `{"error":"failed to save token"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (p *PublicHandler) addAPIKeyToConfig(apiKey string) {
	p.reloader.mu.Lock()
	defer p.reloader.mu.Unlock()

	cfg := p.reloader.current
	for _, k := range cfg.Server.APIKeys {
		if k == apiKey {
			return
		}
	}
	cfg.Server.APIKeys = append(cfg.Server.APIKeys, apiKey)

	data, err := os.ReadFile(p.reloader.configPath)
	if err != nil {
		log.Printf("contribute: read config: %v", err)
		return
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	replaced := false
	for i, line := range lines {
		if strings.Contains(line, "api_keys:") && !strings.Contains(line, "# auth") && i < len(lines)/2 {
			out = append(out, "  api_keys:")
			for _, k := range cfg.Server.APIKeys {
				out = append(out, fmt.Sprintf("    - \"%s\"", k))
			}
			for j := i + 1; j < len(lines); j++ {
				trimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trimmed, "- ") || trimmed == "[]" {
					continue
				}
				i = j - 1
				break
			}
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		log.Printf("contribute: api_keys not found in config, key %s only in memory", fingerprint(apiKey))
		return
	}

	if err := os.WriteFile(p.reloader.configPath, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		log.Printf("contribute: write config: %v", err)
	}
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
