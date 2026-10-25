# Day 6: report-to-client propagation latency (2026-10-24)

Machine: Apple M4; Postgres (amd64 emulation) and Redis in Docker; two API instances (:8080, :8081) sharing them.
Measured with `go run ./cmd/wsprobe`: time from sending the hazard POST until a WebSocket client receives the event. Includes the POST's own Postgres transaction.

## Cross-instance (POST to :8080, client on :8081, via Redis pub/sub)

```
POST http://localhost:8080 -> WS ws://localhost:8081/ws, 100 hazards
  POST sent -> hazard.created received: p50 6.34ms  p95 8.43ms  p99 9.49ms  max 10.78ms
  POST sent -> route.affected received: p50 6.35ms  p95 8.46ms  p99 9.51ms  max 10.79ms
  (includes the POST's own DB transaction; the client sees the event at about the same time the POST returns)
```

## Same instance

```
POST http://localhost:8080 -> WS ws://localhost:8080/ws, 100 hazards
  POST sent -> hazard.created received: p50 6.21ms  p95 8.25ms  p99 9.56ms  max 15.41ms
  POST sent -> route.affected received: p50 6.22ms  p95 8.26ms  p99 9.57ms  max 15.42ms
  (includes the POST's own DB transaction; the client sees the event at about the same time the POST returns)
```

## In the browser

Headless Chrome, two isolated browser contexts: A POSTs to :8081, B (talking to :8080, wheelchair route over the hazard's edge) shows the "reported on your route" toast **40 ms** after A's POST starts (one run, includes React render).
