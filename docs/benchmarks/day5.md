# Day 5 benchmark (2026-10-24)

Machine: Apple M4. Same 2000 seeded query pairs per profile as Day 2. Latency is one in-process A* search; since Day 5 every edge cost goes through the hazard overlay snapshot (`snap.Cost`), as in the API.

## 0 live hazards (`go run ./cmd/bench -hazards 0`)

```
go go1.27.3 darwin/arm64, 10 CPUs
graph: 15468 nodes, 21173 edges, load 192ms, heap 8.6 MiB
queries: 2000 per profile, random node pairs in the profile's usable network, seed 42, 0 live hazards

profile           p50      p95      p99      max |  exp mean   exp p95 | crow mean route mean
wheelchair      521µs  2.026ms  2.812ms  5.812ms |      2104      6199 |     1944m     2461m   (cost table 5.204ms, no path 0)
stroller        487µs  1.785ms    2.8ms  3.681ms |      2025      5764 |     1964m     2455m   (cost table 3.348ms, no path 0)
walk_winter     460µs  1.643ms  2.567ms  4.614ms |      1873      5244 |     1942m     2411m   (cost table 3.078ms, no path 0)
```

## 50 live hazards (`go run ./cmd/bench -hazards 50`)

```
go go1.27.3 darwin/arm64, 10 CPUs
graph: 15468 nodes, 21173 edges, load 136ms, heap 8.6 MiB
queries: 2000 per profile, random node pairs in the profile's usable network, seed 42, 50 live hazards

profile           p50      p95      p99      max |  exp mean   exp p95 | crow mean route mean
wheelchair      569µs  2.189ms  3.033ms      5ms |      2107      6156 |     1944m     2462m   (cost table 3.174ms, no path 1)
stroller        545µs  2.047ms  3.138ms  5.044ms |      2034      5795 |     1965m     2458m   (cost table 3.083ms, no path 2)
walk_winter     534µs  1.921ms  3.097ms  5.659ms |      1881      5301 |     1942m     2413m   (cost table 3.188ms, no path 0)
```

