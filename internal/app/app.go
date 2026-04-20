package app

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var Assets embed.FS

func Run(assets embed.FS) {
	Assets = assets

	configPath := flag.String("config", DefaultConfigPath, "path to YAML config")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	keys, labels, err := LoadAuthsDir(cfg.Auth.Dir)
	if err != nil {
		log.Printf("WARNING: failed to load auths dir: %v", err)
	}
	if len(keys) == 0 {
		log.Printf("WARNING: no auth tokens found — /v1/* will 503 until tokens are added to %s/", cfg.Auth.Dir)
	}

	pool := NewKeyPool(keys, labels)
	pool.SetBreakerTuning(cfg.Auth.Breaker.Threshold, cfg.Auth.Breaker.Cooldown)

	reloader := NewReloader(*configPath, cfg, pool)
	proxy := NewProxyHandler(reloader, pool)
	admin := NewAdminHandler(reloader, pool)
	oauth := NewOAuthHandler(reloader, pool)

	mux := http.NewServeMux()

	// API endpoints
	mux.Handle("POST /v1/chat/completions", proxy)
	mux.HandleFunc("POST /v1/messages", proxy.ServeClaudeHTTP)
	mux.HandleFunc("GET /v1/models", ModelsHandler)

	// Health & status
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/status/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeKeyStatus(w, pool)
	})

	// Admin UI + API (guarded by token.key)
	adminMux := http.NewServeMux()
	admin.Mount(adminMux)
	adminMux.Handle("/admin/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staticFS, err := fs.Sub(Assets, "static")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/admin/" || r.URL.Path == "/admin" {
			data, err := fs.ReadFile(staticFS, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		http.StripPrefix("/admin/", http.FileServer(http.FS(staticFS))).ServeHTTP(w, r)
	}))
	mux.Handle("/admin/", admin.AdminGuard(adminMux))

	// Public pages
	mux.HandleFunc("/login.html", func(w http.ResponseWriter, r *http.Request) {
		serveStaticFile(w, r, "login.html")
	})
	mux.HandleFunc("/authorize.html", func(w http.ResponseWriter, r *http.Request) {
		serveStaticFile(w, r, "authorize.html")
	})

	// Public OAuth
	oauth.Mount(mux)

	srv := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      withMiddleware(mux, reloader),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := NewWatcher(*configPath, reloader)
	if err := watcher.Start(ctx); err != nil {
		log.Fatalf("watcher start: %v", err)
	}

	go func() {
		log.Printf("JieKou2API listening on %s", cfg.Server.ListenAddr)
		log.Printf("Upstream: %s | Model: %s", cfg.Upstream.BaseURL, cfg.Upstream.DefaultModel)
		log.Printf("Auth tokens: %d | Auths dir: %s", pool.Size(), cfg.Auth.Dir)
		for _, e := range pool.Snapshot() {
			log.Printf("  %s  %s", fingerprint(e.Key), e.Label)
		}
		if n := len(cfg.Server.APIKeys); n > 0 {
			log.Printf("Client auth: enabled (%d key(s))", n)
		} else {
			log.Print("Client auth: disabled (open access)")
		}
		if reloader.AdminToken() != "" {
			log.Print("Admin UI: enabled at /admin/")
		} else {
			log.Printf("Admin UI: disabled (create %s to enable)", DefaultAdminTokenPath)
		}
		log.Print("Public login: /login.html")
		log.Print("Endpoints: /v1/messages (Anthropic) | /v1/chat/completions (OpenAI) | /v1/models")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Print("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

func serveStaticFile(w http.ResponseWriter, _ *http.Request, name string) {
	staticFS, err := fs.Sub(Assets, "static")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	data, err := fs.ReadFile(staticFS, name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func writeKeyStatus(w http.ResponseWriter, pool *KeyPool) {
	type keyView struct {
		Index       int    `json:"index"`
		Fingerprint string `json:"fingerprint"`
		Label       string `json:"label"`
		Fails       int    `json:"fails"`
		Broken      bool   `json:"broken"`
		BrokenUntil string `json:"broken_until,omitempty"`
	}
	snap := pool.Snapshot()
	out := struct {
		Total     int       `json:"total"`
		Healthy   int       `json:"healthy"`
		Threshold int       `json:"breaker_threshold"`
		Cooldown  string    `json:"breaker_cooldown"`
		Keys      []keyView `json:"keys"`
	}{
		Total:     len(snap),
		Healthy:   pool.HealthySize(),
		Threshold: pool.Threshold(),
		Cooldown:  pool.Cooldown().String(),
		Keys:      make([]keyView, 0, len(snap)),
	}
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
		out.Keys = append(out.Keys, kv)
	}
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(w, string(b))
}
