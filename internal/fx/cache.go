package fx

import (
    "context"
    "sync"
    "time"
)

// Cache stores FX rate responses with TTL handling.
type Cache struct {
    mu    sync.RWMutex
    items map[string]*RateResponse
}

// NewCache creates a new Cache instance.
func NewCache() *Cache {
    return &Cache{items: make(map[string]*RateResponse)}
}

// Get retrieves a cached response if present.
func (c *Cache) Get(ctx context.Context, key string) (*RateResponse, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    resp, ok := c.items[key]
    if !ok {
        return nil, false
    }
    // TTL is handled by caller; we just return the cached value.
    return resp, true
}

// Set stores a RateResponse in the cache.
func (c *Cache) Set(ctx context.Context, key string, resp *RateResponse) {
    c.mu.Lock()
    defer c.mu.Unlock()
    // Store a copy to avoid external mutation.
    copyResp := *resp
    copyResp.CachedAt = time.Now().UTC()
    c.items[key] = &copyResp
}
