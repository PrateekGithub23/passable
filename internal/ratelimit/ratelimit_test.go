package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestLimiter runs the real Lua script against miniredis (an in-process Redis) with a
// controllable clock.
func newTestLimiter(t *testing.T) (*Limiter, *time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	l := New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	l.now = func() time.Time { return now }
	return l, &now
}

func TestBurstThenRefill(t *testing.T) {
	l, now := newTestLimiter(t)
	rule := Rule{Name: "t", Burst: 3, Per: 10 * time.Second}
	ctx := context.Background()

	// A full bucket allows Burst requests back to back...
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow(ctx, rule, "s1"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	// ...then refuses, saying when the next token arrives.
	ok, retry := l.Allow(ctx, rule, "s1")
	if ok || retry != 10*time.Second {
		t.Fatalf("4th request: ok=%v retry=%v, want refused with 10s", ok, retry)
	}
	// Other keys have their own bucket.
	if ok, _ := l.Allow(ctx, rule, "s2"); !ok {
		t.Fatal("a different key was limited")
	}
	// After one refill period, exactly one more request fits.
	*now = now.Add(10 * time.Second)
	if ok, _ := l.Allow(ctx, rule, "s1"); !ok {
		t.Fatal("no token after one refill period")
	}
	if ok, _ := l.Allow(ctx, rule, "s1"); ok {
		t.Fatal("two tokens after one refill period")
	}
	// A long wait refills only up to Burst, never more.
	*now = now.Add(time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow(ctx, rule, "s1"); ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("after a long idle period %d requests passed, want burst 3", allowed)
	}
}

// TestConcurrentRequestsNeverOverspend: many goroutines (think: several API instances) hit
// one key at once; the atomic script must let exactly Burst through.
func TestConcurrentRequestsNeverOverspend(t *testing.T) {
	l, _ := newTestLimiter(t)
	rule := Rule{Name: "t", Burst: 5, Per: time.Hour}
	var passed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow(context.Background(), rule, "same-key"); ok {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 5 {
		t.Fatalf("%d of 50 concurrent requests passed, want exactly 5", passed.Load())
	}
}

func TestFailOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	l := New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	mr.Close() // Redis goes away
	if ok, _ := l.Allow(context.Background(), Rule{Name: "t", Burst: 1, Per: time.Hour}, "k"); !ok {
		t.Fatal("limiter should allow requests when Redis is down")
	}
	var nilLimiter *Limiter
	if ok, _ := nilLimiter.Allow(context.Background(), Rule{Name: "t", Burst: 1, Per: time.Hour}, "k"); !ok {
		t.Fatal("nil limiter should allow everything")
	}
}
