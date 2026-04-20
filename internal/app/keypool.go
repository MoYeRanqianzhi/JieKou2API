package app

import (
	"sync"
	"sync/atomic"
	"time"
)

const DefaultBreakerCooldown = 1 * time.Hour
const DefaultBreakerThreshold = 3

type KeyEntry struct {
	Key         string
	Label       string
	Fails       int
	Broken      bool
	BrokenUntil time.Time
}

type KeyPool struct {
	mu        sync.RWMutex
	entries   []*KeyEntry
	counter   uint64
	threshold int
	cooldown  time.Duration
}

func NewKeyPool(keys, labels []string) *KeyPool {
	p := &KeyPool{
		threshold: DefaultBreakerThreshold,
		cooldown:  DefaultBreakerCooldown,
	}
	p.Reload(keys, labels)
	return p
}

func (p *KeyPool) SetBreakerTuning(threshold int, cooldown time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if threshold > 0 {
		p.threshold = threshold
	}
	if cooldown > 0 {
		p.cooldown = cooldown
	}
}

func (p *KeyPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

func (p *KeyPool) HealthySize() int {
	now := time.Now()
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, e := range p.entries {
		if !isBroken(e, now) {
			n++
		}
	}
	return n
}

func (p *KeyPool) Snapshot() []KeyEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]KeyEntry, len(p.entries))
	for i, e := range p.entries {
		out[i] = *e
	}
	return out
}

func (p *KeyPool) Threshold() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.threshold
}

func (p *KeyPool) Cooldown() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cooldown
}

func (p *KeyPool) Next() (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.entries)
	if n == 0 {
		return "", -1
	}
	now := time.Now()
	for tries := 0; tries < n; tries++ {
		idx := int((atomic.AddUint64(&p.counter, 1) - 1) % uint64(n))
		e := p.entries[idx]
		if !isBroken(e, now) {
			return e.Key, idx
		}
	}
	bestIdx := 0
	for i, e := range p.entries {
		if e.BrokenUntil.Before(p.entries[bestIdx].BrokenUntil) {
			bestIdx = i
		}
	}
	return p.entries[bestIdx].Key, bestIdx
}

func (p *KeyPool) MarkFailure(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.entries) {
		return
	}
	e := p.entries[idx]
	e.Fails++
	if e.Fails >= p.threshold && !e.Broken {
		e.Broken = true
		e.BrokenUntil = time.Now().Add(p.cooldown)
	}
}

func (p *KeyPool) MarkSuccess(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.entries) {
		return
	}
	e := p.entries[idx]
	e.Fails = 0
	e.Broken = false
	e.BrokenUntil = time.Time{}
}

func (p *KeyPool) Reload(keys, labels []string) (added, removed, kept int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev := make(map[string]*KeyEntry, len(p.entries))
	for _, e := range p.entries {
		prev[e.Key] = e
	}

	useLabels := len(labels) == len(keys)
	next := make([]*KeyEntry, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for i, k := range keys {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		label := "reload"
		if useLabels {
			label = labels[i]
		}
		if old, ok := prev[k]; ok {
			old.Label = label
			next = append(next, old)
			kept++
		} else {
			next = append(next, &KeyEntry{Key: k, Label: label})
			added++
		}
	}
	for k := range prev {
		if _, ok := seen[k]; !ok {
			removed++
		}
	}
	p.entries = next
	return
}

func isBroken(e *KeyEntry, now time.Time) bool {
	if !e.Broken {
		return false
	}
	if now.After(e.BrokenUntil) {
		e.Broken = false
		e.Fails = 0
		e.BrokenUntil = time.Time{}
		return false
	}
	return true
}
