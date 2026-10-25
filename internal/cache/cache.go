// Package cache is a small Redis-backed cache for route responses.
//
// Keys include the graph version (hazard event seq), so when a hazard changes, every new
// request uses a new key and stale routes are never served; old keys simply age out via TTL.
// No explicit invalidation is needed.
//
// The cache is best-effort: if Redis is down or slow, Get reports a miss and Set does nothing,
// so routing keeps working without it. A nil *RouteCache is valid and caches nothing.
package cache

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// RouteCache stores serialized route responses.
type RouteCache struct {
	rdb *redis.Client
	ttl time.Duration
}

// New returns a cache using rdb. Entries live for ttl.
func New(rdb *redis.Client, ttl time.Duration) *RouteCache {
	return &RouteCache{rdb: rdb, ttl: ttl}
}

// RouteKey builds the cache key. Coordinates must already be rounded the same way the route
// was computed, so a key always describes exactly one response.
func RouteKey(version int64, profile string, fromLat, fromLon, toLat, toLon float64) string {
	return fmt.Sprintf("route:v%d:%s:%.5f,%.5f:%.5f,%.5f", version, profile, fromLat, fromLon, toLat, toLon)
}

// Get returns the cached bytes for key, or ok=false on a miss or any error.
func (c *RouteCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	// Short timeout: a slow cache must not make routing slower than no cache.
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) { // redis.Nil = plain miss, not worth logging
			log.Printf("cache get: %v", err)
		}
		return nil, false
	}
	return b, true
}

// Set stores val under key with the cache TTL. Errors are logged, not returned.
func (c *RouteCache) Set(ctx context.Context, key string, val []byte) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := c.rdb.Set(ctx, key, val, c.ttl).Err(); err != nil {
		log.Printf("cache set: %v", err)
	}
}
