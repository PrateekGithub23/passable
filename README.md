# Passable

Accessibility-aware pedestrian routing for the University of Waterloo campus and uptown Waterloo. Passable finds routes for three profiles. `wheelchair` treats steps as impassable and penalizes or blocks bad surfaces and missing or high kerbs. `stroller` blocks steps and applies milder penalties. `walk_winter` applies soft penalties and reacts strongly to hazards. Each segment is labelled `verified`, `inferred` or `unknown`, so the route says how much it trusts the map data. People can report hazards (elevator out, blocked sidewalk, ice); reports decay over time and are confirmed or resolved by votes. A copy-on-write overlay applies the reports to routing without blocking route queries, and anyone whose active route crosses an affected edge is told over WebSocket.

> Status: Day 2 of 10. OSM import, in-memory graph, per-profile cost model, A* routing CLI, first benchmark.

## Running locally

Requires Docker, Go 1.22+, `curl` and `jq`.

```sh
docker compose up -d --wait   # Postgres+PostGIS on localhost:5433, Redis on 6379
scripts/gate.sh               # tag-coverage counts from Overpass
scripts/fetch_osm.sh          # downloads data/ways.json and data/nodes.json
go run ./cmd/importer         # loads the graph (flags: -ways, -nodes, -db)
go run ./cmd/route -from 43.4718,-80.5452 -to 43.4685,-80.5418   # SLC -> Hagey Hall, all profiles
go run ./cmd/bench            # latency / nodes-expanded benchmark
go test -race ./...
```

If Overpass rate-limits you, set `OVERPASS=https://overpass.kumi.systems/api/interpreter`.

## Routing performance (Day 2)

From `go run ./cmd/bench` on an Apple M4: 2000 random queries per profile, mean straight-line distance about 1.95 km. Times are for one in-process A* search. Full output: [`docs/benchmarks/day2.md`](docs/benchmarks/day2.md).

| Profile | p50 | p95 | p99 | Nodes expanded (mean / p95) |
|---|---|---|---|---|
| wheelchair | 493 µs | 1.88 ms | 2.66 ms | 2104 / 6199 |
| stroller | 489 µs | 1.91 ms | 2.90 ms | 2025 / 5764 |
| walk_winter | 442 µs | 1.60 ms | 2.43 ms | 1873 / 5244 |

The graph takes 6.5 MiB of heap and loads in about 116 ms. Building each profile's cost table takes about 3 ms.

Routes differ by profile on real campus pairs. For SLC → Hagey Hall, wheelchair and stroller take a 569 m step-free route, while walk_winter takes a 611 m route that includes steps; only 7.6% of the route length is shared.

## Data coverage

Source: OpenStreetMap via Overpass, fetched 2026-10-24, bbox `43.455,-80.565,43.485,-80.505`.

**OSM tag counts** (`scripts/gate.sh`; "ped ways" = `highway` footway/path/pedestrian)

| Metric | Count |
|---|---|
| Ped ways | 5239 |
| … with `surface` | 3475 (66.3%) |
| … with `smoothness` | 73 (1.4%) |
| … with `incline` | 11 (0.2%) |
| … with `wheelchair` | 6 (0.1%) |
| `footway=sidewalk` ways | 1658 |
| `footway=crossing` ways | 1074 |
| `highway=crossing` nodes | 1684 |
| `highway=steps` ways | 246 |
| Nodes with `kerb` | 1277 |
| Elevator nodes | 8 |

**Imported graph** (`cmd/importer` + verification SQL)

| Metric | Value |
|---|---|
| Ways → edges | 9013 → 21,173 (36 degenerate stretches skipped) |
| Graph nodes | 15,468 |
| Total length | 703.1 km (footway 313.1 km, service 153.8, residential 95.4) |
| Ped edges | 11,159 |
| … surface coverage | 70.3% of edges, 68.7% of length |
| … smoothness / incline / wheelchair | 1.3% / 0.1% / 0.1% |
| Steps edges | 237 |
| Crossing edges | 2167 (1177 with kerb info) |
| Edges with kerb info | 2459 (`lowered` 2251, `flush` 179, `raised` 19) |
| Edges with width | 280 |
| Nearest-edge check (Dana Porter Library) | 5 nearest edges at 14.5–19.0 m |

**What this means**
- **Steps, crossings, kerbs and surface are well mapped,** so the profiles rely on them.
- **Slope is not modelled.** Fewer than 1% of edges have an `incline` tag, and elevation data is out of scope, so the wheelchair profile cannot avoid steep paths.
- **About 30% of pedestrian edges have no surface tag.** Their segments are labelled `unknown`, not assumed to be fine.
- **Indoor routes are largely missing.** Only 8 elevators are mapped, and most building interiors aren't in the graph.
- **Crossings are detected from way tags only.** Where sidewalks aren't mapped separately, a crossing along a road edge isn't flagged (see `DECISIONS.md` #6).
