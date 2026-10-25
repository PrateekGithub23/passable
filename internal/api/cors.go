package api

import (
	"net/http"
	"slices"
)

// WithCORS lets browsers on the listed origins (e.g. the Next.js dev server on
// http://localhost:3000) call the API.
//
// Why it is needed: the frontend and API run on different origins (port 3000 vs 8080, and
// Vercel vs Fly.io in production). Browsers block cross-origin responses unless the server
// opts in with Access-Control-Allow-Origin. We echo the request's Origin only when it is on
// the allowlist, instead of "*", so random sites cannot use the API from a visitor's browser.
func WithCORS(h http.Handler, origins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(origins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			// Responses differ by Origin, so caches must not reuse one origin's response for another.
			w.Header().Add("Vary", "Origin")
		}
		// Preflight: before a non-simple request (e.g. POST with a JSON body, used for hazard
		// reports later), the browser sends OPTIONS to ask which methods and headers are allowed.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Session-ID")
			w.Header().Set("Access-Control-Max-Age", "600") // let the browser cache the preflight for 10 min
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Let browser JS read our custom response headers (cache status, graph version).
		w.Header().Set("Access-Control-Expose-Headers", "X-Cache, X-Graph-Version")
		h.ServeHTTP(w, r)
	})
}
