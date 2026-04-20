package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type authFile struct {
	Token string `json:"token"`
	Label string `json:"label"`
}

func LoadAuthsDir(dir string) (keys, labels []string, err error) {
	if dir == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		data, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			log.Printf("auths: skip %s (read error: %v)", name, rerr)
			continue
		}
		var af authFile
		if jerr := json.Unmarshal(data, &af); jerr != nil {
			log.Printf("auths: skip %s (invalid JSON: %v)", name, jerr)
			continue
		}
		tok := strings.TrimSpace(af.Token)
		if tok == "" {
			log.Printf("auths: skip %s (empty token)", name)
			continue
		}
		label := strings.TrimSpace(af.Label)
		if label == "" {
			label = strings.TrimSuffix(name, filepath.Ext(name))
		}
		keys = append(keys, tok)
		labels = append(labels, label)
	}
	return keys, labels, nil
}

type Reloader struct {
	configPath string
	pool       *KeyPool
	mu         sync.RWMutex
	current    *Config
}

func NewReloader(configPath string, initial *Config, pool *KeyPool) *Reloader {
	return &Reloader{
		configPath: configPath,
		pool:       pool,
		current:    initial,
	}
}

func (r *Reloader) Current() *Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

func (r *Reloader) Reload(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	next, err := LoadConfig(r.configPath)
	if err != nil {
		log.Printf("reload (%s): config load failed — keeping previous: %v", reason, err)
		return
	}

	keys, labels, err := LoadAuthsDir(next.Auth.Dir)
	if err != nil {
		log.Printf("reload (%s): key load failed — keeping previous pool: %v", reason, err)
	} else {
		r.pool.SetBreakerTuning(next.Auth.Breaker.Threshold, next.Auth.Breaker.Cooldown)
		added, removed, kept := r.pool.Reload(keys, labels)
		log.Printf("reload (%s): keys added=%d removed=%d kept=%d total=%d healthy=%d",
			reason, added, removed, kept, r.pool.Size(), r.pool.HealthySize())
	}
	r.current = next
}

type Watcher struct {
	configPath   string
	authsDir     string
	pollInterval time.Duration
	reloader     *Reloader
	debounce     time.Duration
	mu           sync.Mutex
	pending      *time.Timer
	lastSig      string
}

func NewWatcher(configPath string, reloader *Reloader) *Watcher {
	cfg := reloader.Current()
	return &Watcher{
		configPath:   configPath,
		authsDir:     cfg.Auth.Dir,
		pollInterval: cfg.Auth.WatchInterval,
		reloader:     reloader,
		debounce:     200 * time.Millisecond,
	}
}

func (w *Watcher) Start(ctx context.Context) error {
	w.lastSig = w.signature()

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify init: %w", err)
	}

	if w.configPath != "" {
		configDir := filepath.Dir(w.configPath)
		if configDir == "" {
			configDir = "."
		}
		if err := fw.Add(configDir); err != nil {
			log.Printf("watcher: add config dir %q: %v (continuing)", configDir, err)
		}
	}
	if w.authsDir != "" {
		if err := os.MkdirAll(w.authsDir, 0o755); err == nil {
			if err := fw.Add(w.authsDir); err != nil {
				log.Printf("watcher: add auths dir %q: %v (continuing)", w.authsDir, err)
			}
		}
	}

	go func() {
		defer fw.Close()
		poll := time.NewTicker(w.pollInterval)
		defer poll.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				if w.isRelevant(ev) {
					w.schedule(ev.Name)
				}
			case err, ok := <-fw.Errors:
				if !ok {
					return
				}
				log.Printf("watcher: fsnotify error: %v", err)
			case <-poll.C:
				sig := w.signature()
				if sig != w.lastSig {
					w.lastSig = sig
					w.reloader.Reload("poll")
				}
			}
		}
	}()
	return nil
}

func (w *Watcher) isRelevant(ev fsnotify.Event) bool {
	base := filepath.Base(ev.Name)
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") {
		return false
	}
	if w.configPath != "" && sameFile(ev.Name, w.configPath) {
		return true
	}
	if w.authsDir != "" {
		dir := filepath.Dir(ev.Name)
		if sameFile(dir, w.authsDir) && strings.HasSuffix(strings.ToLower(base), ".json") {
			return true
		}
	}
	return false
}

func (w *Watcher) schedule(trigger string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != nil {
		w.pending.Stop()
	}
	w.pending = time.AfterFunc(w.debounce, func() {
		sig := w.signature()
		w.mu.Lock()
		w.lastSig = sig
		w.mu.Unlock()
		w.reloader.Reload("fsnotify:" + filepath.Base(trigger))
	})
}

func (w *Watcher) signature() string {
	var b strings.Builder
	if w.configPath != "" {
		if st, err := os.Stat(w.configPath); err == nil {
			fmt.Fprintf(&b, "cfg:%d:%d|", st.Size(), st.ModTime().UnixNano())
		} else {
			b.WriteString("cfg:missing|")
		}
	}
	if w.authsDir != "" {
		entries, err := os.ReadDir(w.authsDir)
		if err != nil {
			b.WriteString("auths:err|")
		} else {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
					continue
				}
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, n := range names {
				st, err := os.Stat(filepath.Join(w.authsDir, n))
				if err != nil {
					continue
				}
				fmt.Fprintf(&b, "%s:%d:%d|", n, st.Size(), st.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

func sameFile(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
