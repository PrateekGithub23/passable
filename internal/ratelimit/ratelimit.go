// Package ratelimit is a token-bucket rate limiter stored in Redis, so every API instance
// shares the same budget for a given key (a session or an IP address).
//
// Token bucket in one sentence: each key has a bucket holding up to Burst tokens; it refills
// continuously at one token per Per; every request takes one token; no token -> 429.
// That allows short bursts (a user confirming several hazards in a row) while capping the
// sustained rate (a script spamming reports).
//
// The read-refill-take-write sequence runs as one Lua script, which Redis executes
// atomically: two instances handling requests for the same key at the same moment can't both
// read "1 token left" and both let their request through.
package ratelimit

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rule is a limit: up to Burst requests at once, refilling one every Per.
type Rule struct {
	Name  string // part of the Redis key, e.g. "hazard-create"
	Burst float64
	Per   time.Duration
}

// tokenBucket is the atomic refill-and-take step.
//
//	KEYS[1] = bucket key
//	ARGV    = refill rate (tokens per ms), burst, now (ms)
//	returns   {allowed (1/0), retry-after (ms)}
//
// The bucket is a hash {t = tokens, ts = last update}. A missing key means a full bucket.
// PEXPIRE removes buckets once they would be full again anyway, so idle keys don't pile up.
var tokenBucket = redis.NewScript(`
local rate  = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now   = tonumber(ARGV[3])
local d = redis.call('HMGET', KEYS[1], 't', 'ts')
local tokens = tonumber(d[1]) or burst
local ts     = tonumber(d[2]) or now
if now > ts then tokens = math.min(burst, tokens + (now - ts) * rate) end
local allowed, retry = 0, 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = math.ceil((1 - tokens) / rate)
end
redis.call('HSET', KEYS[1], 't', tostring(tokens), 'ts', tostring(math.max(now, ts)))
redis.call('PEXPIRE', KEYS[1], math.ceil(burst / rate) + 1000)
return {allowed, retry}
`)

// Limiter checks rules against Redis. A nil *Limiter allows everything.
type Limiter struct {
	rdb     *redis.Client
	now     func() time.Time // injectable clock for tests
	timeout time.Duration    // Redis budget per check; past it, the request is allowed
}

// New returns a limiter backed by rdb.
func New(rdb *redis.Client) *Limiter {
	return &Limiter{rdb: rdb, now: time.Now, timeout: 50 * time.Millisecond}
}

// Allow takes one token for key under rule. It returns whether the request may proceed and,
// if not, how long until a token is available.
//
// Fail-open: if Redis is unreachable the request is allowed. Losing rate limiting during a
// Redis outage is better than taking the whole API down with it.
func (l *Limiter) Allow(ctx context.Context, rule Rule, key string) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	rate := 1 / float64(rule.Per.Milliseconds()) // tokens per millisecond
	res, err := tokenBucket.Run(ctx, l.rdb, []string{"rl:" + rule.Name + ":" + key},
		rate, rule.Burst, l.now().UnixMilli()).Int64Slice()
	if err != nil || len(res) != 2 {
		log.Printf("ratelimit %s: %v (allowing)", rule.Name, err)
		return true, 0
	}
	if res[0] == 1 {
		return true, 0
	}
	return false, time.Duration(res[1]) * time.Millisecond
}

// RetryAfterSeconds rounds a wait up to whole seconds for the Retry-After header (min 1).
func RetryAfterSeconds(d time.Duration) int {
	return int(math.Max(1, math.Ceil(d.Seconds())))
}
