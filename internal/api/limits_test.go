package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PrateekGithub23/passable/internal/ratelimit"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLimitReturns429WithRetryAfter(t *testing.T) {
	mr := miniredis.RunT(t)
	s := &Server{limiter: ratelimit.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))}
	rule := ratelimit.Rule{Name: "test", Burst: 2, Per: time.Minute}
	h := s.limit(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }, s.byIP(rule))

	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", "/", nil))
		codes = append(codes, rec.Code)
		if rec.Code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "60" {
			t.Errorf("Retry-After %q, want 60", rec.Header().Get("Retry-After"))
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("status codes %v, want [200 200 429]", codes)
	}
}

func TestClientIPTrustsHeaderOnlyWhenConfigured(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.7:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	// Not configured: a client-supplied header must not choose the rate-limit bucket.
	if ip := (&Server{}).clientIP(req); ip != "10.0.0.7" {
		t.Errorf("untrusted: got %s, want the TCP peer 10.0.0.7", ip)
	}
	// Configured (we're behind a proxy that sets it): first address is the client.
	if ip := (&Server{ipHeader: "X-Forwarded-For"}).clientIP(req); ip != "203.0.113.9" {
		t.Errorf("trusted: got %s, want 203.0.113.9", ip)
	}
}
