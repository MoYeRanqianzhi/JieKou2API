package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Run() {
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

	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", proxy)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/status/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeKeyStatus(w, pool)
	})

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
		log.Printf("Upstream: %s", cfg.Upstream.BaseURL)
		log.Printf("Default model: %s", cfg.Upstream.DefaultModel)
		log.Printf("Auths dir: %s | watch interval: %s", cfg.Auth.Dir, cfg.Auth.WatchInterval)
		log.Printf("Auth tokens: %d (round-robin, breaker=%d fails/%s cooldown)",
			pool.Size(), cfg.Auth.Breaker.Threshold, cfg.Auth.Breaker.Cooldown)
		for _, e := range pool.Snapshot() {
			log.Printf("  %s  %s", fingerprint(e.Key), e.Label)
		}
		if n := len(cfg.Server.APIKeys); n > 0 {
			log.Printf("Client API key authentication: enabled (%d key(s))", n)
		} else {
			log.Print("Client API key authentication: disabled (open access)")
		}
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
