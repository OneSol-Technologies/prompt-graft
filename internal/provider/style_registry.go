package provider

import "sync"

type StyleRegistry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	fallback  Provider
}

func NewStyleRegistry(fallback Provider) *StyleRegistry {
	return &StyleRegistry{providers: make(map[string]Provider), fallback: fallback}
}

func (r *StyleRegistry) Register(style string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[style] = p
}

func (r *StyleRegistry) Lookup(style string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.providers[style]; ok {
		return p
	}
	return r.fallback
}
