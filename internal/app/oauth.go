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
}

func (p *PublicHandler) handleContribute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		http.Error(w, `{"error":"token is required"}`, http.StatusBadRequest)
		return
	}

	if req.Label == "" {
		req.Label = "contrib-" + randomHex(4)
	} else {
		req.Label = sanitizeLabel(req.Label)
	}

	cfg := p.reloader.Current()
	dir := cfg.Auth.Dir
	os.MkdirAll(dir, 0o755)

	af := authFile{Token: req.Token, Label: req.Label}
	afData, _ := json.MarshalIndent(af, "", "  ")
	path := filepath.Join(dir, req.Label+".json")
	if err := os.WriteFile(path, afData, 0o644); err != nil {
		log.Printf("contribute: save auth file: %v", err)
		http.Error(w, `{"error":"failed to save token"}`, http.StatusInternalServerError)
		return
	}
	p.reloader.Reload("contribute")

	apiKey := GenerateAPIKey()
	p.addAPIKeyToConfig(apiKey)

	log.Printf("contribute: saved token %s, issued key %s", req.Label, fingerprint(apiKey))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"api_key": apiKey,
		"label":   req.Label,
	})
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
