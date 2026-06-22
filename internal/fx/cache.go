package fx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	rateTTL       = 60 * time.Second
	rateKeyPrefix = "fx:rate:"
)

// RateCache is a Redis-backed store for FX rate responses.
type RateCache struct {
	redis *redis.Client
}

// NewRateCache creates a RateCache backed by the given Redis client.
func NewRateCache(r *redis.Client) *RateCache {
	return &RateCache{redis: r}
}

// Get returns a cached RateResponse for the given pair, or false if absent.
func (c *RateCache) Get(ctx context.Context, from, to string) (*RateResponse, bool) {
	data, err := c.redis.Get(ctx, rateKeyPrefix+from+":"+to).Bytes()
	if err != nil {
		return nil, false
	}
	var resp RateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

// Set stores a RateResponse in Redis with a 60-second TTL.
func (c *RateCache) Set(ctx context.Context, from, to string, resp *RateResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = c.redis.Set(ctx, rateKeyPrefix+from+":"+to, data, rateTTL).Err()
}

// CachedAt is the timestamp embedded in a rate response to track freshness.
func (c *RateCache) CachedAt(ctx context.Context, from, to string) (time.Time, bool) {
	resp, ok := c.Get(ctx, from, to)
	if !ok {
		return time.Time{}, false
	}
	return resp.CachedAt, true
}
