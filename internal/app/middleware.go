package app

import (
	"context"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

type ctxKey string

const (
	ctxKeyPassthrough    ctxKey = "passthrough"
	ctxKeyClientToken    ctxKey = "client_token"
)

func withMiddleware(h http.Handler, reloader *Reloader) http.Handler {
	return recovery(cors(logging(authGuard(h, reloader))))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

// authGuard Matrix:
//   - api_keys empty       → pass-through (no auth required)
//   - token in api_keys    → accept, use free-trial pool
//   - token not in api_keys → accept, mark passthrough to JieKou official API
//   - no token             → 401
func authGuard(next http.Handler, reloader *Reloader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		cfg := reloader.Current()
		expected := cfg.Server.APIKeys

		if len(expected) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		token := ""
		if xKey := r.Header.Get("x-api-key"); xKey != "" {
			token = xKey
		} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}

		if token == "" {
			http.Error(w, `{"error":{"type":"authentication_error","message":"Missing API key"}}`, http.StatusUnauthorized)
			return
		}

		for _, k := range expected {
			if k == token {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Key not in our list → passthrough to JieKou official API with this key
		log.Printf("auth: key %s not in api_keys, passthrough to official API", fingerprint(token))
		ctx := context.WithValue(r.Context(), ctxKeyPassthrough, true)
		ctx = context.WithValue(ctx, ctxKeyClientToken, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v\n%s", err, debug.Stack())
				http.Error(w, `{"error":{"message":"Internal server error","type":"server_error"}}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
