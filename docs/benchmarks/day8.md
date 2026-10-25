# Day 8 benchmarks (2026-10-25)

Machine: Apple M4, 16 GiB. One API process (`-rate-limit=false`), Postgres+PostGIS (amd64 emulation) and Redis in Docker, k6 on the same machine (so it competes with the API for CPU; real numbers would be no worse).

## Routing core, three benchmark days compared

`go run ./cmd/bench`: the same 2000 seeded query pairs per profile, one in-process A* search each (wheelchair shown).

| Day | What changed | p50 | p95 | p99 | nodes expanded (mean) |
|---|---|---|---|---|---|
| 2 | plain A* on a cost table | 493 µs | 1.88 ms | 2.66 ms | 2104 |
| 5 | + hazard overlay lookup on every edge (0 hazards) | 521 µs | 2.03 ms | 2.81 ms | 2104 |
| 8 | (no routing changes since Day 5) | 500 µs | 1.91 ms | 2.68 ms | 2104 |

Full Day 8 output:

```
go go1.27.3 darwin/arm64, 10 CPUs
graph: 15468 nodes, 21173 edges, load 144ms, heap 8.6 MiB
queries: 2000 per profile, random node pairs in the profile's usable network, seed 42, 0 live hazards

profile           p50      p95      p99      max |  exp mean   exp p95 | crow mean route mean
wheelchair      500µs  1.914ms  2.683ms  4.214ms |      2104      6199 |     1944m     2461m   (cost table 3.188ms, no path 0)
stroller        477µs  1.781ms  2.718ms  4.001ms |      2025      5764 |     1964m     2455m   (cost table 3.133ms, no path 0)
walk_winter     449µs  1.609ms  2.488ms  4.343ms |      1873      5244 |     1942m     2411m   (cost table 3.345ms, no path 0)
```

Go benchmark on the real graph from the committed fixture (`go test -bench AStarCampus -benchmem ./internal/route`):

```
BenchmarkAStarCampus/wheelchair-10         	    1646	    656775 ns/op	      2116 nodes/op	  224715 B/op	      25 allocs/op
BenchmarkAStarCampus/stroller-10           	    1911	    631793 ns/op	      2017 nodes/op	  224830 B/op	      25 allocs/op
BenchmarkAStarCampus/walk_winter-10        	    2133	    596310 ns/op	      1863 nodes/op	  225013 B/op	      25 allocs/op
```

## HTTP load (k6, `scripts/k6/load.js`)

Route requests between random pairs of 60 on-path campus points, random profile. Latency is measured by k6, client side.

### 200 req/s for 60 s, plus 1 hazard report per second (each resolved 3 s later)

The graph version changed ~240 times during the run, so most requests missed the cache by design (each change means new cache keys).

```
route_cache_hit................: 0.90%  109 out of 12000
route_hit_duration.............: avg=575.59µs med=521µs  p(90)=753.4µs p(95)=864.79µs p(99)=1.73ms max=2.25ms  
route_miss_duration............: avg=2.93ms   med=2.78ms p(90)=3.88ms  p(95)=4.24ms   p(99)=5.32ms max=108.75ms
http_req_duration..............: avg=2.96ms   med=2.78ms p(90)=3.92ms  p(95)=4.32ms   p(99)=5.73ms max=108.75ms
http_req_failed................: 0.00%  0 out of 12132
```

Report-to-client propagation measured during this run (`cmd/wsprobe -n 30`, same instance):

```
  POST sent -> hazard.created received: p50 4.46ms  p95 5.52ms  p99 7.14ms  max 9.31ms
  POST sent -> route.affected received: p50 4.47ms  p95 5.54ms  p99 7.17ms  max 9.32ms
```

### 200 req/s for 60 s, no hazard writes (stable graph version: the cache can work)

```
route_cache_hit................: 39.64% 4758 out of 12001
route_hit_duration.............: avg=620.15µs med=550.5µs p(90)=881.3µs p(95)=1ms    p(99)=1.65ms max=6.83ms 
route_miss_duration............: avg=3.27ms   med=3.11ms  p(90)=4.49ms  p(95)=5ms    p(99)=6.43ms max=13.4ms 
http_req_duration..............: avg=2.22ms   med=2.38ms  p(90)=4.17ms  p(95)=4.63ms p(99)=5.98ms max=13.4ms 
http_req_failed................: 0.00%  0 out of 12011
```

### 1000 req/s for 30 s, plus hazard writes

```
route_cache_hit................: 4.55%  1365 out of 29996
route_hit_duration.............: avg=807.34µs med=709µs  p(90)=1.11ms p(95)=1.33ms p(99)=2.13ms  max=13.68ms 
route_miss_duration............: avg=3.89ms   med=3.49ms p(90)=4.92ms p(95)=5.64ms p(99)=12.46ms max=266.05ms
http_req_duration..............: avg=3.77ms   med=3.44ms p(90)=4.89ms p(95)=5.62ms p(99)=12.3ms  max=266.05ms
http_req_failed................: 0.00%  0 out of 30066
dropped_iterations.............: 5      0.156146/s
```

## Server-side view (`route_requests`, all three k6 runs)

Duration here is the whole route handler (cache lookup, snapping queries, A*, response building), recorded by the API itself.

```
 requests | cache_hit_pct | p50_ms | p95_ms | p99_ms | avg_nodes_expanded | graph_versions_seen 
----------+---------------+--------+--------+--------+--------------------+---------------------
    53997 |          11.5 |   2.93 |   4.87 |   7.56 |                375 |                 241
(1 row)

```

## Found by the load test

The first k6 smoke run had 4% failed route requests. They were all requests touching one point inside a building whose indoor corridors (`indoor=yes`) form a dense network not connected to the outdoor paths: the 20 nearest edges were all indoor, so snapping answered "no mapped path within 150 m" although an outdoor path is ~40 m away. Fixed (64 candidates, plus a 400-candidate fallback when none is on the main network), with an integration regression test that fails without the fix. After the fix: 0 failures in 54k requests.
