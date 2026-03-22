package provider

import "sync"

type Registry struct {
    mu        sync.RWMutex
    providers map[string]Provider
    fallback  Provider
}

func NewRegistry(fallback Provider) *Registry {
    return &Registry{providers: make(map[string]Provider), fallback: fallback}
}

func (r *Registry) Register(host string, p Provider) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.providers[host] = p
}

func (r *Registry) Lookup(host string) Provider {
    r.mu.RLock()
    defer r.mu.RUnlock()
    if p, ok := r.providers[host]; ok {
        return p
    }
    return r.fallback
}
