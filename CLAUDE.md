# Passable: project brief for Claude Code

Accessibility-aware pedestrian routing for Waterloo (UW campus + uptown) with live, decaying, crowd-sourced hazard reports. Built solo by a UWaterloo Computer Engineering student in a 10-day sprint. The goal is a deployed, tested, benchmarked full-stack system that is explainable in SWE interviews.

## How to work with me
- Work in small steps. After each step, run the code or tests and show the output before moving on.
- Do not invent results. Any number used in the README or on a resume must come from a real run.
- Never add features outside the MVP list below. If something is not listed, ask first.
- Prefer boring, readable code. Add a short entry to `DECISIONS.md` whenever a real design choice is made (what, what was rejected, why).
- Commit at the end of each working session with a clear message.
- If the plan and reality disagree (e.g. data coverage is poor), stop and tell me instead of silently working around it.

## Scope
**Building (MVP):**
- Pedestrian graph of campus + uptown Waterloo built from OpenStreetMap, bbox `43.455,-80.565,43.485,-80.505` (south,west,north,east).
- 3 profiles: `wheelchair` (steps are a hard block, bad surfaces and missing/high kerbs penalized or blocked), `stroller` (steps blocked, mild penalties), `walk_winter` (soft penalties for steps and bad surfaces; actively hazard-sensitive).
- A* routing with per-segment confidence labels (`verified` / `inferred` / `unknown`) and an explanation when no fully accessible route exists.
- Hazard reports (elevator out, blocked sidewalk, construction, ice/snow, missing curb ramp) with TTL, confirm/resolve votes, and a lifecycle: active -> confirmed -> resolved/expired.
- Live updates over WebSocket; users with an active route through an affected edge get a "route affected, reroute?" prompt.
- Anonymous sessions only, rate limiting, public `/status` page with live metrics.

**Not building:** accounts/auth, elevation data, photo uploads, mobile apps, offline mode, anything outside the bbox.

**Cut order if behind (never cut the last two):** 1) place search, 2) simulator, 3) stroller profile, 4) route cache. **Never cut:** the copy-on-write hazard overlay with live updates; tests and benchmarks.

## Stack (and why)
- **Frontend:** Next.js + TypeScript + Tailwind + MapLibre GL (typed UI; free vector-map renderer).
- **Backend:** Go (goroutines, low-latency read-mostly in-memory graph, static binary).
- **DB:** PostgreSQL + PostGIS (spatial snapping via KNN `<->`, GiST indexes), `pg_trgm` for place search.
- **Redis:** pub/sub for cross-instance fan-out, route cache keyed by graph version, token-bucket rate limiting.
- **Infra:** Docker Compose locally, GitHub Actions CI, Prometheus metrics, k6 load tests.
- **Deploy:** Vercel (frontend), Fly.io or Railway (Go API + Redis), Neon or Supabase (Postgres with PostGIS).

## Architecture

```
Browser (Next.js + MapLibre)
   |  REST: route, hazards          WS: hazard events + route.affected
   v                                 v
Go service
  Router (A*) -- reads --> atomic.Pointer[Snapshot]
                            |- base graph (immutable, compact adjacency arrays)
                            '- hazard overlay (copy-on-write, versioned)
  Hazard svc --tx--> Postgres --event log--> overlay rebuild
       '--publish--> Redis pub/sub
  WS hub: viewport subscriptions + edge -> subscribers inverted index
PostgreSQL + PostGIS (edges, reports, votes, events)   Redis (pub/sub, cache, rate limit)
```

## Core engineering challenge
Route queries must see a consistent view of edge costs while hazards change them concurrently, without locks stalling searches, and only affected users should be notified.

Design:
1. Base graph is immutable; static per-profile costs are precomputed at load.
2. Live hazards live in a small copy-on-write overlay behind an `atomic.Pointer`. Each query loads the pointer once and uses that single versioned snapshot for the whole search.
3. A hazard change builds a new overlay from the delta and swaps the pointer; the version increments. The route cache key includes the version, so stale routes are never served.
4. Active-route subscriptions register edge IDs in an inverted index (`edge -> subscribers`); a hazard on edge E notifies only sessions indexed under E.
5. Postgres is the source of truth. Each instance applies overlay deltas from the event log (via Redis pub/sub); a sequence gap triggers a full resync.
6. A* uses a haversine heuristic. All edge costs are >= edge length, so the heuristic is admissible.

Trade-offs to document in `DECISIONS.md`: full in-memory graph vs on-demand queries, A* vs contraction hierarchies, soft penalties vs hard blocks, how unknown tags are treated.

## Database schema
```sql
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE nodes (
  id   BIGINT PRIMARY KEY,                       -- OSM node id
  geom geometry(Point, 4326) NOT NULL
);
CREATE INDEX nodes_geom_idx ON nodes USING GIST (geom);

CREATE TABLE edges (                             -- undirected for MVP
  id          BIGSERIAL PRIMARY KEY,
  osm_way_id  BIGINT NOT NULL,
  from_node   BIGINT NOT NULL REFERENCES nodes(id),
  to_node     BIGINT NOT NULL REFERENCES nodes(id),
  length_m    DOUBLE PRECISION NOT NULL,
  geom        geometry(LineString, 4326) NOT NULL,
  highway     TEXT NOT NULL,
  footway     TEXT, surface TEXT, smoothness TEXT, incline TEXT,
  width_m     DOUBLE PRECISION,
  wheelchair  TEXT, kerb_start TEXT, kerb_end TEXT,
  has_steps   BOOLEAN NOT NULL DEFAULT false,
  is_crossing BOOLEAN NOT NULL DEFAULT false,
  tags        JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX edges_geom_idx ON edges USING GIST (geom);
CREATE INDEX edges_from_idx ON edges (from_node);
CREATE INDEX edges_to_idx   ON edges (to_node);
```
Added from day 5 (via a real migration tool, not init scripts): `sessions`, `hazard_reports` (edge_id, type, severity, status, expires_at, confirm_count, resolve_count, note), `report_votes` (PK `(report_id, session_id)` for idempotent votes), `hazard_events` (seq bigserial, report_id, kind, edge_ids, created_at; append-only), `route_requests` (profile, distance_m, duration_ms, nodes_expanded, graph_version; for benchmarks).

Use `geometry` (not `geography`) so GiST + KNN `<->` work directly; cast to `geography` only when meters are needed. PostGIS wants `(lon, lat)`.

## API (v1)
- `GET /api/v1/route?from=lat,lng&to=lat,lng&profile=wheelchair`
- `GET /api/v1/places?q=`
- `GET /api/v1/hazards?bbox=`
- `POST /api/v1/hazards`
- `POST /api/v1/hazards/{id}/confirm` and `/resolve`
- `GET /api/v1/graph/version`
- `WS /ws`: client messages `subscribe.viewport`, `subscribe.route`; server events `hazard.created|updated|expired`, `route.affected`
- `GET /healthz`, `GET /metrics`

## Frontend
Pages: `/` (map + route panel), `/about` (data sources, coverage, limitations), `/status` (live metrics).
Components: `MapView`, `RoutePanel`, `ProfileSelector`, `RouteSegmentList`, `HazardLayer`, `ReportSheet`, `LiveToast`, `CoverageOverlay`.

## Testing and benchmarking
- Correctness: property test that A* equals Dijkstra on random queries; golden routes on known campus pairs; table tests for each profile's cost model.
- Concurrency: `go test -race`; stress test hammering hazard writes during routing.
- Integration: Postgres and Redis via testcontainers; WebSocket delivery tests.
- Performance: Go benchmarks + k6. Record p50/p95/p99 route latency, nodes expanded, cache hit rate, graph memory, and report-to-client propagation latency. Run benchmarks on days 2, 5 and 8 and keep all three.

## 10-day roadmap
| Day | Deliverable | Done when |
|---|---|---|
| 1 | Coverage gate, repo, Docker Compose, schema, OSM importer | Importer loads the bbox into Postgres; coverage report printed |
| 2 | Go graph loader, per-profile cost model, A*, first benchmark | CLI returns different routes per profile on 3 known campus pairs |
| 3 | Route API, snap-to-nearest-edge, place search, segment explanations | `curl` returns annotated segments with confidence labels |
| 4 | Next.js app, MapLibre, route rendering, profile selector | Click two points in browser, see a route |
| 5 | Hazard API, state machine, expiry worker, copy-on-write overlay | Posting a hazard changes the route and cache key; race detector clean |
| 6 | WebSocket hub, subscriptions, Redis pub/sub, live UI | Hazard in browser A appears in browser B and triggers reroute prompt |
| 7 | Report-flow UI, rate limiting, labelled demo simulator. **Feature freeze** | A stranger can file a report without instructions |
| 8 | Property tests, integration tests, k6 load tests | Tests pass; benchmark numbers recorded |
| 9 | Production deploy, Prometheus, status page, CI/CD | Public URL works; CI deploys on merge |
| 10 | README, architecture diagram, design notes, demo video | Someone can clone, run and understand it in 10 minutes |

Days 8-10 are integration, testing, deployment and docs only. No new features.

## MVP completion criteria
- Public URL works end to end and routes differ by profile on real campus paths.
- A hazard posted in one browser appears in another within a measured, reported latency; an affected active route triggers a reroute prompt.
- Tests and CI are green, including the race detector.
- Benchmarks are published, README and architecture diagram exist, and data-coverage limitations are stated plainly.

---

## DAY 1 TASKS (current)

Goal: repo + Postgres/PostGIS + Redis in Docker + schema + importer that loads real OSM data for the bbox into the DB, plus a go/no-go decision on data coverage.

Layout: `cmd/importer/`, `db/migrations/`, `scripts/`, `data/` (git-ignored), `DECISIONS.md`, `README.md`.

1. **Repo setup.** `.gitignore` containing `data/`, `bin/`, `node_modules/`, `.next/`, `.env`. Go module `github.com/<USER>/passable` (ask me for my GitHub username), Go 1.22+.
2. **docker-compose.yml.** Services: `db` (image `postgis/postgis:16-3.4`, user/password/db all `passable`, port 5432, healthcheck with `pg_isready`, mount `./db/migrations` at `/docker-entrypoint-initdb.d`), `redis` (`redis:7-alpine`, port 6379), named volume `pgdata`. Init scripts only run on first volume creation; schema changes today need `docker compose down -v`.
3. **Schema.** Put the SQL above in `db/migrations/001_init.sql`. Start the stack and verify with `SELECT postgis_version();` and `\dt`.
4. **Coverage gate: do this BEFORE writing the importer.** Write `scripts/gate.sh` (bash + curl + jq) that POSTs Overpass queries for the bbox using `out count;` and prints counts for: footway/path/pedestrian ways; those with `surface`; `footway=sidewalk`; `footway=crossing`; `highway=crossing` nodes; `highway=steps`; ways with `wheelchair`, `smoothness`, `incline`; nodes with `kerb`; elevator nodes; and surface coverage as a percentage. Add a 2s sleep between queries and support an `OVERPASS` env var for mirror endpoints. Run it and show me the output.
   - Go: surface coverage around 50% or more, steps and crossings present, some kerb/wheelchair tags.
   - Marginal: lower coverage -> proceed, but plan to hand-tag or seed about 20 campus segments later and document it.
   - No-go: almost no tags -> stop and tell me. The fallback project is TransferGuard (transit transfer-reliability planner).
   Stop here and show me the numbers before continuing.
5. **Fetch data.** `scripts/fetch_osm.sh` downloads two files into `data/`:
   - `ways.json`: Overpass `out geom;` for ways with `highway` in (footway, path, pedestrian, steps, living_street, residential, service, unclassified, tertiary, secondary, primary, cycleway, track), excluding `foot=no` and `access` private/no.
   - `nodes.json`: nodes tagged `kerb` or `highway=crossing`.
   Print element counts afterward. A count of 0 means the query or bbox is wrong.
6. **Importer** (`cmd/importer/main.go`, Go + `github.com/jackc/pgx/v5`). Flags: `-ways`, `-nodes`, `-db`. Logic:
   - Count how many ways use each OSM node.
   - Split each way into edges at every node used by more than one way and at the way's endpoints. Skip stretches where from == to or length is 0.
   - Edge length = sum of haversine distances along the stretch's geometry.
   - Fill typed columns from tags; `has_steps` = `highway=steps`; `is_crossing` = `footway=crossing` or `highway=crossing`; `kerb_start`/`kerb_end` from the tagged-node lookup; parse `width` to a float; store all raw tags in `tags` JSONB.
   - In a single transaction: `TRUNCATE edges, nodes RESTART IDENTITY CASCADE`, batch-insert nodes then edges (batches of about 5000), commit.
   - Geometry written as WKT `LINESTRING(lon lat, ...)` with SRID 4326.
   - Log ways -> edges and graph node counts.
7. **Verify** with SQL and show me the results: edge count and total km; counts by `highway`; tag coverage percentages on footway/path/pedestrian edges (surface, smoothness, incline, wheelchair); counts of steps, crossings and kerb info; and a nearest-edge KNN query (`ORDER BY geom <-> ST_SetSRID(ST_MakePoint(lon, lat), 4326) LIMIT 5`) for a known campus point, which should return distances of roughly 5-30 m.
8. **Docs.** Create `DECISIONS.md` with entries for: region choice, Overpass + custom importer instead of osm2pgsql, `geometry` vs `geography`, undirected edges. Create `README.md` with a title, one-paragraph pitch, and a "Data coverage" section containing the real numbers from steps 4 and 7.
9. **Commit** ("Day 1: schema, OSM importer, Docker Compose") and push if a remote is configured.

Day 1 is done when: the stack starts healthy, the gate ran and the numbers are recorded, the importer ran with no errors, the verification queries give sensible results, and the docs are committed.

Known pitfalls: Overpass may rate-limit (retry after a minute or use a mirror such as `https://overpass.kumi.systems/api/interpreter`); on Apple Silicon add `platform: linux/amd64` to the db service if the image misbehaves; a port 5432 conflict means remap to 5433 and pass `-db` accordingly.
