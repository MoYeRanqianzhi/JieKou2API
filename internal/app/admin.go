package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type AdminHandler struct {
	reloader *Reloader
	pool     *KeyPool
}

func NewAdminHandler(reloader *Reloader, pool *KeyPool) *AdminHandler {
	return &AdminHandler{reloader: reloader, pool: pool}
}

func (a *AdminHandler) AdminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := a.reloader.AdminToken()
		if token == "" {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/admin/api/") {
			auth := r.Header.Get("Authorization")
			xToken := r.Header.Get("X-Admin-Token")
			clientToken := ""
			if strings.HasPrefix(auth, "Bearer ") {
				clientToken = strings.TrimPrefix(auth, "Bearer ")
			} else if xToken != "" {
				clientToken = xToken
			}
			if clientToken != token {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *AdminHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/admin/api/status", a.handleStatus)
	mux.HandleFunc("/admin/api/config", a.handleConfig)
	mux.HandleFunc("/admin/api/keys", a.handleKeys)
	mux.HandleFunc("/admin/api/keys/", a.handleKeyAction)
	mux.HandleFunc("/admin/api/reload", a.handleReload)
	mux.HandleFunc("/admin/api/apikeys", a.handleAPIKeys)
}

func (a *AdminHandler) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	cfg := a.reloader.Current()
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"keys": cfg.Server.APIKeys,
		})
	case http.MethodDelete:
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"key is required"}`, http.StatusBadRequest)
			return
		}
		a.reloader.mu.Lock()
		newKeys := make([]string, 0)
		for _, k := range a.reloader.current.Server.APIKeys {
			if k != key {
				newKeys = append(newKeys, k)
			}
		}
		a.reloader.current.Server.APIKeys = newKeys
		a.reloader.mu.Unlock()
		a.persistAPIKeys()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	case http.MethodPost:
		var req struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
			// Generate a new key if none provided
			req.Key = GenerateAPIKey()
		}
		a.reloader.mu.Lock()
		a.reloader.current.Server.APIKeys = append(a.reloader.current.Server.APIKeys, req.Key)
		a.reloader.mu.Unlock()
		a.persistAPIKeys()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"key": req.Key})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *AdminHandler) persistAPIKeys() {
	a.reloader.mu.RLock()
	keys := a.reloader.current.Server.APIKeys
	configPath := a.reloader.configPath
	a.reloader.mu.RUnlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("admin: read config: %v", err)
		return
	}
	lines := strings.Split(string(data), "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.Contains(line, "api_keys:") && i < len(lines)/2 {
			out = append(out, "  api_keys:")
			if len(keys) == 0 {
				out = append(out, "    []")
			} else {
				for _, k := range keys {
					out = append(out, fmt.Sprintf("    - \"%s\"", k))
				}
			}
			for j := i + 1; j < len(lines); j++ {
				trimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(trimmed, "- ") || trimmed == "[]" {
					continue
				}
				i = j - 1
				break
			}
			continue
		}
		out = append(out, line)
	}
	os.WriteFile(configPath, []byte(strings.Join(out, "\n")), 0o644)
}

func (a *AdminHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := a.pool.Snapshot()
	type keyView struct {
		Index       int    `json:"index"`
		Fingerprint string `json:"fingerprint"`
		Label       string `json:"label"`
		Fails       int    `json:"fails"`
		Broken      bool   `json:"broken"`
		BrokenUntil string `json:"broken_until,omitempty"`
	}
	keys := make([]keyView, 0, len(snap))
	for i, e := range snap {
		kv := keyView{
			Index:       i,
			Fingerprint: fingerprint(e.Key),
			Label:       e.Label,
			Fails:       e.Fails,
			Broken:      e.Broken,
		}
		if e.Broken {
			kv.BrokenUntil = e.BrokenUntil.Format(time.RFC3339)
		}
		keys = append(keys, kv)
	}
	cfg := a.reloader.Current()
	resp := map[string]any{
		"total":     len(snap),
		"healthy":   a.pool.HealthySize(),
		"threshold": a.pool.Threshold(),
		"cooldown":  a.pool.Cooldown().String(),
		"keys":      keys,
		"config": map[string]any{
			"listen":        cfg.Server.ListenAddr,
			"upstream":      cfg.Upstream.BaseURL,
			"default_model": cfg.Upstream.DefaultModel,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *AdminHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(a.reloader.configPath)
		if err != nil {
			http.Error(w, `{"error":"failed to read config"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}
		var test Config
		if err := yaml.Unmarshal(body, &test); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid YAML: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(a.reloader.configPath, body, 0o644); err != nil {
			http.Error(w, `{"error":"failed to write config"}`, http.StatusInternalServerError)
			return
		}
		a.reloader.Reload("admin-config-save")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *AdminHandler) handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Token string `json:"token"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if req.Token == "" {
			http.Error(w, `{"error":"token is required"}`, http.StatusBadRequest)
			return
		}
		if req.Label == "" {
			req.Label = "key-" + fmt.Sprintf("%d", time.Now().Unix())
		}
		cfg := a.reloader.Current()
		dir := cfg.Auth.Dir
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, `{"error":"failed to create auths dir"}`, http.StatusInternalServerError)
			return
		}
		af := authFile{Token: req.Token, Label: req.Label}
		data, _ := json.MarshalIndent(af, "", "  ")
		path := filepath.Join(dir, sanitizeLabel(req.Label)+".json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			http.Error(w, `{"error":"failed to write auth file"}`, http.StatusInternalServerError)
			return
		}
		a.reloader.Reload("admin-add-key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	case http.MethodDelete:
		label := r.URL.Query().Get("label")
		if label == "" {
			http.Error(w, `{"error":"label is required"}`, http.StatusBadRequest)
			return
		}
		cfg := a.reloader.Current()
		path := filepath.Join(cfg.Auth.Dir, sanitizeLabel(label)+".json")
		if err := os.Remove(path); err != nil {
			log.Printf("admin: remove %s: %v", path, err)
			http.Error(w, `{"error":"failed to remove key"}`, http.StatusInternalServerError)
			return
		}
		a.reloader.Reload("admin-delete-key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *AdminHandler) handleKeyAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/api/keys/"), "/")
	if len(parts) < 2 {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, `{"error":"invalid index"}`, http.StatusBadRequest)
		return
	}
	action := parts[1]
	switch action {
	case "trip":
		a.pool.MarkFailure(idx)
		a.pool.MarkFailure(idx)
		a.pool.MarkFailure(idx)
	case "reset":
		a.pool.MarkSuccess(idx)
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (a *AdminHandler) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.reloader.Reload("admin-manual")
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		}
	}
	result := b.String()
	if len(result) > 64 {
		result = result[:64]
	}
	if result == "" {
		result = "unnamed"
	}
	return result
}
