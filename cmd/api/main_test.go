package main

import "testing"

// TestRedisOptions: the same REDIS_URL handling must work for local Docker Redis and for
// hosted free-tier providers, which require TLS (rediss://) and a password.
func TestRedisOptions(t *testing.T) {
	local, err := redisOptions("redis://localhost:6379/0")
	if err != nil {
		t.Fatal(err)
	}
	if local.Addr != "localhost:6379" || local.TLSConfig != nil || !local.ContextTimeoutEnabled {
		t.Errorf("local: addr %q tls %v ctxTimeouts %v", local.Addr, local.TLSConfig != nil, local.ContextTimeoutEnabled)
	}

	hosted, err := redisOptions("rediss://default:s3cret@example-12345.upstash.io:6379")
	if err != nil {
		t.Fatal(err)
	}
	if hosted.TLSConfig == nil || hosted.TLSConfig.ServerName != "example-12345.upstash.io" {
		t.Errorf("hosted: TLS not enabled for the right host: %+v", hosted.TLSConfig)
	}
	if hosted.Username != "default" || hosted.Password != "s3cret" || !hosted.ContextTimeoutEnabled {
		t.Errorf("hosted: user %q password set %v ctxTimeouts %v", hosted.Username, hosted.Password != "", hosted.ContextTimeoutEnabled)
	}

	if _, err := redisOptions("not a url"); err == nil {
		t.Error("an invalid URL should be rejected at startup, not ignored")
	}
}
