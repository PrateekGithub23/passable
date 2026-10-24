# Day 2 benchmark (2026-10-24)

Machine: Apple M4, 16 GiB. Postgres in Docker (amd64 emulation); routing is in-process, so the DB does not affect query latency.
Commands: `go run ./cmd/bench` and `go run ./cmd/route -from A -to B`. Latency is one A* search on a precomputed cost table; it excludes snapping and I/O.

## Random queries

```
go go1.27.3 darwin/arm64, 10 CPUs
graph: 15468 nodes, 21173 edges, load 116ms, heap 6.5 MiB
queries: 2000 per profile, random node pairs in the profile's usable network, seed 42

profile           p50      p95      p99      max |  exp mean   exp p95 | crow mean route mean
wheelchair      493µs  1.882ms  2.658ms  4.125ms |      2104      6199 |     1944m     2461m   (cost table 3.268ms)
stroller        489µs  1.909ms  2.899ms  7.971ms |      2025      5764 |     1964m     2455m   (cost table 3.103ms)
walk_winter     442µs  1.601ms  2.426ms   4.64ms |      1873      5244 |     1942m     2411m   (cost table 3.101ms)
```

## Golden campus pairs

### Dana Porter->E7 (`43.4697,-80.5421` -> `43.4730,-80.5395`)

```
wheelchair   snap: from 17.2 m, to 19.5 m (usable network 14987 nodes)
wheelchair      794 m  cost    915  edges  40  steps 0  crossings 2  expanded   507  154µs
             confidence: verified 614 m, inferred 96 m, unknown 84 m
             penalties: surface unknown x2, road without sidewalk data x7, smoothness intermediate x1, crossing without kerb data x1
stroller     snap: from 17.2 m, to 19.5 m (usable network 15094 nodes)
stroller        777 m  cost    862  edges  37  steps 0  crossings 2  expanded   474  137µs
             confidence: verified 600 m, inferred 89 m, unknown 88 m
             penalties: surface unknown x2, road without sidewalk data x4, smoothness intermediate x1, crossing without kerb data x2
walk_winter  snap: from 17.2 m, to 19.5 m (usable network 15246 nodes)
walk_winter     664 m  cost    734  edges  32  steps 1  crossings 2  expanded   477  156µs
             confidence: verified 560 m, inferred 89 m, unknown 15 m
             penalties: steps x2, road without sidewalk data x4

shared length between profiles:
  wheelchair  vs stroller     91.0%
  wheelchair  vs walk_winter  74.2%
  stroller    vs walk_winter  82.9%
```

### SLC->Hagey Hall (`43.4718,-80.5452` -> `43.4685,-80.5418`)

```
wheelchair   snap: from 28.5 m, to 13.4 m (usable network 14987 nodes)
wheelchair      569 m  cost    580  edges  41  steps 0  crossings 0  expanded   211  79µs
             confidence: verified 529 m, inferred 0 m, unknown 39 m
             penalties: surface unknown x2
stroller     snap: from 28.5 m, to 13.4 m (usable network 15094 nodes)
stroller        569 m  cost    574  edges  41  steps 0  crossings 0  expanded   232  78µs
             confidence: verified 529 m, inferred 0 m, unknown 39 m
             penalties: surface unknown x2
walk_winter  snap: from 25.2 m, to 13.4 m (usable network 15246 nodes)
walk_winter     611 m  cost    644  edges  41  steps 1  crossings 3  expanded   138  54µs
             confidence: verified 587 m, inferred 0 m, unknown 24 m
             penalties: steps x2, road without sidewalk data x2

shared length between profiles:
  wheelchair  vs stroller    100.0%
  wheelchair  vs walk_winter   7.6%
  stroller    vs walk_winter   7.6%
```

### V1->MC (`43.4710,-80.5503` -> `43.4721,-80.5440`)

```
wheelchair   snap: from 4.3 m, to 40.3 m (usable network 14987 nodes)
wheelchair      646 m  cost    709  edges  33  steps 0  crossings 1  expanded   202  74µs
             confidence: verified 524 m, inferred 116 m, unknown 6 m
             penalties: crossing without kerb data x1, road without sidewalk data x2, surface compacted x2
stroller     snap: from 4.3 m, to 40.3 m (usable network 15094 nodes)
stroller        646 m  cost    670  edges  33  steps 0  crossings 2  expanded   182  74µs
             confidence: verified 524 m, inferred 110 m, unknown 11 m
             penalties: crossing without kerb data x2, surface compacted x2
walk_winter  snap: from 4.3 m, to 38.7 m (usable network 15246 nodes)
walk_winter     562 m  cost    591  edges  32  steps 1  crossings 3  expanded   113  45µs
             confidence: verified 544 m, inferred 0 m, unknown 18 m
             penalties: surface dirt x1, steps x2, surface unknown x1

shared length between profiles:
  wheelchair  vs stroller     87.8%
  wheelchair  vs walk_winter  45.0%
  stroller    vs walk_winter  57.3%
```
