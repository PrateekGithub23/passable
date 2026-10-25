# Passable

Accessibility-aware pedestrian routing for the University of Waterloo campus and uptown Waterloo. Passable finds routes for three profiles. `wheelchair` treats steps as impassable and penalizes or blocks bad surfaces and missing or high kerbs. `stroller` blocks steps and applies milder penalties. `walk_winter` applies soft penalties and reacts strongly to hazards. Each segment is labelled `verified`, `inferred` or `unknown`, so the route says how much it trusts the map data. People can report hazards (elevator out, blocked sidewalk, ice); reports decay over time and are confirmed or resolved by votes. A copy-on-write overlay applies the reports to routing without blocking route queries, and anyone whose active route crosses an affected edge is told over WebSocket.

> Status: Day 9 of 10, deployed. **Live: https://passable-alpha.vercel.app** (API: https://passable-api.fly.dev, [status page](https://passable-alpha.vercel.app/status)).

## Production

| Piece | Where |
|---|---|
| Frontend | Vercel: https://passable-alpha.vercel.app |
| API (Go) | Fly.io, one machine in `iad`: https://passable-api.fly.dev (`/healthz`, `/metrics`) |
| Postgres + PostGIS | Neon, us-east-1 |
| Redis | Upstash free tier, us-east-1 (TLS) |

All of it runs on free tiers with no payment method. Setup steps are in [`docs/DEPLOY.md`](docs/DEPLOY.md).

Measured on the deployed system (2026-10-25):
- **Routing:** route requests not in the cache average 11.6 ms on the server over 11 distinct routes. Most of that is the two snapping queries between Fly and Neon; the A* search itself takes ~0.2 ms. Repeat requests are served from the Redis cache.
- **Live updates, server side:** a hazard reaches WebSocket clients 2.2 ms (p50) after it is committed, through Upstash pub/sub.
- **Live updates, in the browser:** in a two-browser test on the live site, a report filed through the UI in one browser showed the "reroute?" prompt in the other 470 ms after Send was pressed. That includes both browsers' internet round trips to Virginia.

## Running locally

Requires Docker, Go 1.22+, `curl` and `jq`.

```sh
docker compose up -d --wait   # Postgres+PostGIS on localhost:5433, Redis on 6379
go run ./cmd/migrate          # apply database migrations (goose)
scripts/gate.sh               # tag-coverage counts from Overpass
scripts/fetch_osm.sh          # downloads data/ways.json and data/nodes.json
go run ./cmd/importer         # loads the graph (flags: -ways, -nodes, -db)
go run ./cmd/route -from 43.4718,-80.5452 -to 43.4685,-80.5418   # SLC -> Hagey Hall, all profiles
go run ./cmd/bench            # latency / nodes-expanded benchmark
go run ./cmd/api              # HTTP API on :8080 (flags -addr, -db, -cors; or PORT / DATABASE_URL / CORS_ORIGINS)
go test -race ./...                                   # unit, property and golden tests (no Docker needed)
go test -tags integration -race ./integration          # real Postgres+Redis via testcontainers
k6 run -e RATE=200 scripts/k6/load.js                 # load test (start the API with -rate-limit=false)

cd web && npm install && npm run dev   # map UI on http://localhost:3000
```

In the UI, click once for the start and again for the destination, then switch profiles to compare routes. "Report a problem" files a hazard: tap the spot, choose what it is, send. Tap any hazard marker to say "Still there" or "It's gone".

To see live features without real reports, run the demo simulator. Its hazards are labelled "Demo", and it cleans up after itself when you press Ctrl-C:

```sh
go run ./cmd/simulator -every 45s -max 6
``` Line colours show confidence: dark blue is verified, light blue is inferred, and dashed amber is unknown. Red marks barriers. A dotted grey line joins each clicked point to where the route actually starts on the network.

If Overpass rate-limits you, set `OVERPASS=https://overpass.kumi.systems/api/interpreter`.

## API

```sh
curl 'localhost:8080/api/v1/route?from=43.4718,-80.5452&to=43.4685,-80.5418&profile=wheelchair'
curl 'localhost:8080/api/v1/places?q=dc'
curl localhost:8080/healthz
```

`/route` takes `from` and `to` as `lat,lng` inside the bbox, plus `profile` (`wheelchair`, `stroller` or `walk_winter`). The response includes:
- `accessible`, `distance_m`, `cost`, `nodes_expanded` and `search_ms`;
- `snap`: the edge each point snapped to, and how far away it was;
- `confidence_m`: meters labelled verified, inferred and unknown;
- `notes`: plain-language warnings;
- `segments`: consecutive edges with the same description, name, surface, kerb, confidence and penalties, merged, each with `[lon, lat]` coordinates.

When no fully accessible route exists, the response has `accessible: false` and a `barriers` list naming what blocks the profile.

### Hazard reports

```sh
SID=$(curl -s -X POST localhost:8080/api/v1/session | jq -r .session_id)
curl -X POST localhost:8080/api/v1/hazards -H "X-Session-ID: $SID" -H 'Content-Type: application/json' \
     -d '{"lat":43.47087,"lng":-80.54274,"type":"blocked_sidewalk","note":"fenced off"}'
curl -X POST localhost:8080/api/v1/hazards/1/confirm -H "X-Session-ID: $SID"    # or /resolve
curl 'localhost:8080/api/v1/hazards?bbox=-80.565,43.455,-80.505,43.485'
curl localhost:8080/api/v1/graph/version
```

Writes are rate-limited per session and per IP (Redis token buckets; a session can file 5 reports at once, then 20/hour). Over the limit, the API returns `429` with `Retry-After`. See `DECISIONS.md` #25 for every limit.

Hazard types are `elevator_out`, `blocked_sidewalk`, `construction`, `ice_snow` and `missing_curb_ramp`. A new report changes routes from the very next request:
- the response's `graph_version` increases;
- the route cache key changes;
- affected segments list the report under `hazards`;
- if a hazard leaves no way through, it appears as a named barrier.

Reports move from `active` to `confirmed` (2 confirmations), then to `resolved` (2 resolve votes, or the reporter) or `expired` (per-type TTL, extended by each confirmation).

### Live updates (`GET /ws`)

The map subscribes to its visible area and to the edges of the route on screen. Hazard changes arrive as `hazard.created|updated|expired`. A new hazard on the route's edges that matters for the chosen profile sends `route.affected`, and the UI asks "reroute?". With several API instances, events travel between them over Redis pub/sub, and Postgres stays the source of truth if a message is lost.

Measured from a hazard POST to the WebSocket event: p50 6.3 ms and p95 8.4 ms, including the cross-instance trip. In the browser, the toast appeared 40 ms after the report was sent ([`docs/benchmarks/day6-propagation.md`](docs/benchmarks/day6-propagation.md); `go run ./cmd/wsprobe` reproduces it).

`/places?q=` matches UW building codes exactly (`dc`, `slc`, `e7`), matches word prefixes (`dav`), and tolerates typos (`dana portr`).

## Load test (Day 8)

From k6 on an Apple M4, with one API process. Full results are in [`docs/benchmarks/day8.md`](docs/benchmarks/day8.md).

| Scenario | Requests | Failed | p50 | p95 | p99 | Cache hits |
|---|---|---|---|---|---|---|
| 200 req/s + a hazard change every second | 12,000 | 0 | 2.78 ms | 4.24 ms | 5.32 ms | 0.9% |
| 200 req/s, no hazard writes | 12,001 | 0 | | | | 39.6% (hit p95 1.0 ms vs miss 5.0 ms) |
| 1,000 req/s + hazard changes | 29,996 | 0 | 3.44 ms | 5.62 ms | 12.3 ms | 4.6% |

During the 200 req/s run, a hazard reached a WebSocket client p95 5.5 ms after its POST was sent. Hazard changes invalidate the cache by design: each change moves requests to new keys, so stale routes are never served.

## Routing performance

Day 5 numbers, with the hazard overlay on the hot path: wheelchair p50 521 µs and p99 2.81 ms with 0 live hazards; 569 µs and 3.03 ms with 50 ([`docs/benchmarks/day5.md`](docs/benchmarks/day5.md)). Day 2 baseline before the overlay:

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
