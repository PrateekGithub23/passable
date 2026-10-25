package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PrateekGithub23/passable/internal/metrics"
	"github.com/PrateekGithub23/passable/internal/ratelimit"
)

// Rate limits. Writes are limited per session AND per IP: per session so one person can't
// flood reports, per IP so one machine can't dodge that by minting many sessions. Reads are
// per IP and generous: they exist to stop scripts, not people clicking around.
var (
	limitNewSession   = ratelimit.Rule{Name: "session-ip", Burst: 10, Per: time.Minute}
	limitReportSess   = ratelimit.Rule{Name: "report-session", Burst: 5, Per: 3 * time.Minute} // 5 at once, then 20/hour
	limitReportIP     = ratelimit.Rule{Name: "report-ip", Burst: 20, Per: time.Minute}
	limitVoteSess     = ratelimit.Rule{Name: "vote-session", Burst: 30, Per: 10 * time.Second}
	limitRouteIP      = ratelimit.Rule{Name: "route-ip", Burst: 60, Per: 200 * time.Millisecond} // 5/s sustained
	limitPlacesIP     = ratelimit.Rule{Name: "places-ip", Burst: 30, Per: 300 * time.Millisecond}
	limitWebSocketIP  = ratelimit.Rule{Name: "ws-ip", Burst: 10, Per: 10 * time.Second}
	limitListHazardIP = ratelimit.Rule{Name: "hazards-ip", Burst: 60, Per: 500 * time.Millisecond}
	limitStatusIP     = ratelimit.Rule{Name: "status-ip", Burst: 20, Per: time.Second}
)

// keyed pairs a rule with how to derive its bucket key from a request.
type keyed struct {
	rule ratelimit.Rule
	key  func(*http.Request) string
}

func (s *Server) byIP(rule ratelimit.Rule) keyed { return keyed{rule, s.clientIP} }

func bySession(rule ratelimit.Rule) keyed {
	return keyed{rule, func(r *http.Request) string { return r.Header.Get(sessionHeader) }}
}

// limit wraps h so a request runs only if every rule allows it. Otherwise it answers 429 with
// Retry-After (seconds), which well-behaved clients use to back off.
func (s *Server) limit(h http.HandlerFunc, rules ...keyed) http.HandlerFunc {
	if s.limiter == nil {
		return h // no Redis (or limits disabled): nothing to enforce
	}
	return func(w http.ResponseWriter, r *http.Request) {
		for _, k := range rules {
			if ok, wait := s.limiter.Allow(r.Context(), k.rule, k.key(r)); !ok {
				metrics.RateLimited.WithLabelValues(k.rule.Name).Inc()
				secs := ratelimit.RetryAfterSeconds(wait)
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeError(w, http.StatusTooManyRequests, fmt.Sprintf("too many requests; try again in %d s", secs))
				return
			}
		}
		h(w, r)
	}
}

// clientIP is the address rate limits are keyed on. Behind a reverse proxy (e.g. Fly.io) every
// request comes from the proxy's address, so the real client IP must be read from the header
// the proxy sets. That header is only trusted when configured: otherwise anyone could send a
// fake X-Forwarded-For and get a fresh rate-limit bucket with every request.
func (s *Server) clientIP(r *http.Request) string {
	if s.ipHeader != "" {
		if v := r.Header.Get(s.ipHeader); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0]) // X-Forwarded-For: client, proxy1, ...
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
