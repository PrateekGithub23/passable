# Design decisions

Each entry: what was chosen, what was rejected, and why.

## 1. Region: UW campus + uptown Waterloo
**Chose:** bbox `43.455,-80.565,43.485,-80.505` (south,west,north,east).
**Rejected:** the whole city of Waterloo or the Region of Waterloo.
**Why:** a 10-day solo build needs an area I can check by walking it. The campus has the features the profiles care about (steps, crossings, kerbs, a few elevators). It also keeps the graph small: 21k edges fit in memory with room to spare. The coverage gate confirmed the data is usable before any code depended on it (surface on 66% of pedestrian ways, 1277 kerb nodes, 246 steps ways).

## 2. Overpass API + custom Go importer instead of osm2pgsql
**Chose:** `scripts/fetch_osm.sh` downloads Overpass JSON (`out geom`), and `cmd/importer` splits the ways into graph edges and loads them with pgx.
**Rejected:** osm2pgsql (and osm2pgrouting) on a `.pbf` extract.
**Why:** osm2pgsql produces rendering tables, not a routable graph, so a splitting step would still be needed. osm2pgrouting's schema is built around cars, and its tag handling is configured through XML. A ~250-line importer gives me exactly the schema in `001_init.sql` and control over how tags reach the cost model, and I can explain every line of it. The cost is that Overpass rate-limits requests, which the scripts handle by retrying and supporting an `OVERPASS` mirror. Downloading a regional extract isn't worth it for a bbox this small.

## 3. `geometry(…, 4326)` instead of `geography`
**Chose:** store `geometry` with SRID 4326 and cast to `geography` only when meters are needed (`ST_Distance(geom::geography, …)`).
**Rejected:** `geography` columns.
**Why:** the KNN operator `<->` works directly on GiST-indexed `geometry`, which is what nearest-edge snapping needs. Edge lengths are computed once in the importer (haversine), so storing the geometry in meters gains nothing. **Consequence:** `<->` ranks by distance in degrees, so KNN order differs slightly from order in meters (observed: a 19.0 m edge ranked ahead of two 17.2 m edges). Snapping must take about 10 KNN candidates and re-rank them with `geography` distance.

## 4. Undirected edges
**Chose:** one row per edge. The router will traverse each edge in both directions.
**Rejected:** directed edges (two rows per segment, or a `oneway` flag the router respects).
**Why:** pedestrians aren't bound by `oneway`, which applies to vehicles. The direction-dependent effects that matter (incline, steps up versus down) are essentially untagged here (incline on 0.1% of pedestrian edges), and elevation data is out of scope. Undirected edges halve the table and keep the graph simple. Revisit if direction-dependent costs are added.

## 5. Kerb values come from any node in the stretch, not only the edge's endpoints
**Chose:** `kerb_start` is the first kerb-tagged node anywhere in the edge's stretch, and `kerb_end` is the last (when it's a different node).
**Rejected:** reading kerb tags only from the edge's two endpoints.
**Why:** OSM mappers usually put the kerb node partway along the crossing way, a few metres before the road, not at the junction where the importer splits edges. Reading only the endpoints would drop most kerb data. With this rule, 2459 edges carry kerb info, 1177 of them crossings.

## 6. `is_crossing` comes from way tags only
**Chose:** `is_crossing = footway=crossing OR highway=crossing` on the way itself.
**Rejected:** marking every edge that touches a `highway=crossing` node.
**Why:** crossing nodes sit on the road way as well as the footway, so the node rule would mark the adjoining road edges as crossings too. **Limitation:** where sidewalks aren't mapped separately, a crossing along a road edge is not flagged.

## 7. Local environment: Postgres on host port 5433, PostGIS image under amd64
**Chose:** map the container to `localhost:5433`, and set `platform: linux/amd64` on the db service.
**Why:** a local Postgres already uses 5432. `postgis/postgis:16-3.4` has no arm64 build, so on Apple Silicon it runs under emulation. That is slower but fine for development. The importer's default `-db` points at 5433.

## 8. Cost model: length multipliers plus fixed penalties, with hard blocks only where a profile truly cannot pass
**Chose:** `cost = length_m * multiplier + penalty_m`. The multiplier (≥ 1) is the product of the factors that apply, and the penalties (≥ 0) are added. A blocked edge costs +Inf. Implemented in `internal/cost`, with table tests per profile.

| Factor | wheelchair | stroller | walk_winter |
|---|---|---|---|
| Steps | block | block | ×2.0 + 15 m |
| Fair surface (compacted, fine_gravel, sett, grass_paver, cobblestone) | ×1.5 | ×1.2 | ×1.2 |
| Bad surface (gravel, dirt, ground, grass, sand, rock, unpaved, mud, …) | block | ×2.0 | ×1.5 |
| Surface unknown, not inferable | ×1.3 | ×1.15 | ×1.1 |
| `smoothness=intermediate` | ×1.2 | ×1.1 | – |
| `smoothness` bad or worse | block | ×1.5 | – |
| `wheelchair=no` | block | – | – |
| Kerb `raised` | block | +30 m | – |
| Kerb of unknown height (`yes`, `rolled`, other) | +50 m | +10 m | – |
| Crossing with no kerb data | +30 m | +10 m | – |
| Road: `sidewalk=separate` / major road without sidewalk / minor road without sidewalk data | ×1.5 / ×3.0 / ×1.3 (all profiles) | | |

**Rejected:** a separate cost formula per profile (harder to compare and test), and blocking every edge with poor or missing data.
**Why:** a hard block means "this profile physically can't use it" (steps or a raised kerb for a wheelchair). Everything else is a preference, so it gets a soft penalty, and a slightly worse route always beats no route. Multipliers ≥ 1 and penalties ≥ 0 keep cost ≥ length, which keeps the haversine heuristic admissible. `TestCostNeverBelowLength` checks this across every combination of tag values, and `TestAStarMatchesDijkstra` checks the search itself. walk_winter has no hard blocks; it reacts to hazards through the overlay (Day 5).
**Ignored on purpose:** `incline` is almost always just `up`/`down`, which says nothing on undirected edges. `width` appears on only 280 edges.

## 9. Missing tags are penalized and labelled, not blocked or assumed fine
**Chose:** a sidewalk, crossing, plaza or road with no `surface` tag is assumed paved (no penalty, label `inferred`). Any other edge with no surface tag gets the profile's "surface unknown" multiplier and the label `unknown`. A crossing with no kerb data gets a penalty and is labelled `unknown`. Surface values the model doesn't recognize count as unknown.
**Rejected:** blocking unknown edges for wheelchair, and treating unknown as good.
**Why:** about 30% of pedestrian edges have no surface tag. Blocking them would disconnect large parts of campus. Treating them as good would put false confidence in the route. Inference applies only where local practice makes it safe: Waterloo sidewalks and roads are effectively always paved. The label shows the user which kind of segment they're on.

## 10. Full in-memory graph instead of querying Postgres per route
**Chose:** load every node and edge at startup into a CSR adjacency index (`internal/graph`), plus one precomputed cost slice per profile.
**Rejected:** on-demand queries (pgRouting, or recursive SQL per route).
**Why:** the whole graph is 6.5 MiB of heap and loads in about 110 ms. Searches read only memory: p50 is about 0.49 ms and p99 about 2.7 ms on random cross-bbox queries (see `docs/benchmarks/day2.md`). An immutable in-memory graph is also what makes the copy-on-write hazard overlay possible. The cost is that memory and startup time grow with the region, which is irrelevant at this bbox size.

## 11. A* with a haversine heuristic instead of contraction hierarchies
**Chose:** plain A* over the cost table.
**Rejected:** contraction hierarchies (CH) and other preprocessing-based speedups.
**Why:** CH precomputes shortcuts for fixed edge weights. Live hazards change weights constantly, and each change would trigger expensive re-contraction (or require customizable CH, which is far more complex). A* already runs in about 2–3 ms at p99 on this graph. It expands roughly 1,900–2,100 nodes per query on average. That is well within budget, and per-query cost changes are trivial to apply.

## 12. Snap to the profile's usable network
**Chose:** before searching, snap each endpoint to the nearest node in the largest connected component of the edges the profile can use.
**Rejected:** snapping to the nearest node in the full graph.
**Why:** on a real campus pair (SLC → Hagey Hall), the nearest node sat in a 9-node pocket that only steps lead out of, so wheelchair and stroller searches failed with "no route" even though a step-free route exists. Profile-aware snapping moved the start 3.3 m (from 25.2 m to 28.5 m away) and found a 569 m step-free route. Day 3's edge snapping applies the same rule and reports the snap distance, so the user can see when the nearest usable path is far away.

## 13. Snapping: PostGIS KNN for candidates, exact projection in Go, routes start mid-edge
**Chose:** fetch the 20 nearest edges with `ORDER BY geom <-> point` (GiST index). Then project the point onto each edge's geometry in Go, re-rank by distance in meters, and keep the nearest edge the profile can use within 150 m. The search seeds both endpoints of the start edge with the matching fraction of that edge's cost, and finishes through a virtual target joined to both endpoints of the end edge. When start and end share an edge, the direct piece is also a candidate.
**Rejected:** snapping to the nearest node, which can add up to half an edge of detour and put the start on the wrong side of a barrier. Also rejected: `ST_ClosestPoint`/`ST_LineLocatePoint` in SQL, whose fraction is planar (by degrees) and wouldn't match the haversine edge lengths.
**Why:** routes start where the user actually is. The heuristic stays admissible because a partial edge's cost is at least the straight-line distance to the snapped point. Covered by `TestAStarPointsMatchesDijkstra`, which includes the shared-edge case.

## 14. "No accessible route" returns the route with the fewest barriers, and names them
**Chose:** when either point has no usable edge within 150 m, or the search finds no path, route again with each blocked edge costed at `10,000 + 10 × length` instead of +Inf. The response comes back with `accessible: false`, a `barriers` list (description, reasons, location) and a note.
**Rejected:** returning an error or an empty route.
**Why:** "no route" is useless to someone in a wheelchair. "The only way through crosses these steps at this spot" is actionable, and it's what the planned elevator-out hazards will need. The large constant makes barrier count dominate distance. On today's static data this never triggers: all 237 steps-edge midpoints have a usable path within 150 m (40 of them got a "nearest path not usable" snap note instead). It is tested on a synthetic graph (`TestPlanExplainsBarrier`).

## 15. Place search: OSM named features + pg_trgm, with UW building codes
**Chose:** a `places` table of named buildings, amenities, shops, leisure, tourism and office features (1,458 rows), using way and relation centers. Search runs over one generated lowercased column (`name ref aliases`). An exact `ref` match scores 1.0, a word-prefix regex match 0.9 (GIN trigram index), and otherwise `word_similarity` handles typos. Aliases include `old_name`/`old_ref`, because UW renamed Engineering 7 to the Pearl Sullivan Engineering Building and people still search for "E7".
**Rejected:** plain substring `LIKE` (in a test, "dc" matched "Chil**dc**are"), and deduping by name alone (it merged 13 Tim Hortons locations into one).
**Note:** `002_places.sql` is an init script like `001`. On the existing dev volume it was applied by hand with `psql`. Day 5 brings in a real migration tool, as the plan says.

## 16. Frontend: MapLibre loaded only in the browser, OpenFreeMap basemap, state owned by one component
**Chose:**
- **One owner of state.** `Planner` (client component) holds start, end, profile and the last result. `MapView`, `RoutePanel`, `ProfileSelector` and `RouteSegmentList` only render props.
- **MapLibre loads only in the browser.** It's created inside `useEffect` with a dynamic `import("maplibre-gl")`, so it never runs during Next's server prerender. Its Web Worker script is copied into `public/` by `predev`/`prebuild` and set with `setWorkerUrl`, because the bundler can't trace maplibre-gl 6's runtime-built worker URL.
- **Free basemap:** OpenFreeMap vector tiles. No API key, no account, and the style is MapLibre-native.
- **Loading is derived, not stored.** Each result is saved with the request key (points + profile) it answers, and a result whose key doesn't match the current inputs is ignored. Stale responses can't appear, and the React lint rule against `setState` inside effects is satisfied.

**Rejected:** a global state library (three components don't need one); Mapbox GL or Google Maps (both need keys and paid tiers); `next/dynamic` with `ssr: false` for the whole map (it works too, but an effect-scoped import keeps the browser-only boundary inside one file).
**Why:** the setup is simple, free, and easy to explain. Verified end to end in headless Chrome: two clicks route SLC → Hagey Hall (wheelchair 560 m; switching to winter walk gives 622 m via steps), with no console or network errors.

## 17. CORS: echo allowlisted origins instead of `*`
**Chose:** `api.WithCORS` sets `Access-Control-Allow-Origin` only for origins listed in `CORS_ORIGINS` (default `http://localhost:3000`), adds `Vary: Origin`, and answers preflight requests for the upcoming POST endpoints.
**Why:** the frontend and API are on different origins in development (ports 3000 and 8080) and in production (Vercel and Fly.io). An allowlist stops arbitrary sites from calling the API from a visitor's browser, which matters once hazard reports are accepted.

## 18. Migrations with goose, embedded in the binary
**Chose:** `pressly/goose` with SQL files in `db/migrations`, embedded via `go:embed`, and run by `go run ./cmd/migrate [up|down|status]`. 001 and 002 were rewritten as idempotent (`IF NOT EXISTS`), so goose adopted the existing dev database without dropping it. The Docker init-script mount was removed, because an init script would also execute the `-- +goose Down` `DROP` statements.
**Rejected:** golang-migrate (separate up/down files and a heavier CLI), keeping Docker init scripts (they only run on an empty volume, and nothing records which schema version a database is at), and auto-migrating on API startup (with several instances, schema changes should be one explicit step in the deploy).
**Verified:** `up` on the existing database kept all 21,173 edges, and a `down`/`up` round trip of 003 drops and recreates its five tables.

## 19. Hazard overlay: copy-on-write snapshots, versioned by a global event sequence
**Chose:**
- **Immutable snapshots.** `overlay.Snapshot` holds the live hazards plus, for each affected edge, the combined effect per profile. It lives behind `atomic.Pointer` (`overlay.Holder`).
- **One load per request.** Each route request loads the snapshot once (`api.view`) and uses it for the cache key, snapping, the search and the explanation.
- **Writers copy, then swap.** A writer clones the two small maps (size proportional to the number of live hazards, not the graph), applies one event, and swaps the pointer. Writers are serialized by a mutex; readers never lock.
- **Version = event seq.** The version is `hazard_events.seq`. Every hazard write transaction first takes `pg_advisory_xact_lock`, so writes are serialized across all instances and seq order equals commit order.
- **Events in strict order.** An instance applies events only when `seq == current + 1`, ignores duplicates, and on a gap rebuilds from Postgres in one `REPEATABLE READ` read (all live reports plus `max(seq)`). Events carry the report's full new state, so applying one is an upsert or a delete.

**Rejected:** an `RWMutex` around mutable costs (a long search holding the read lock blocks writers, and queued writers then block new searches); and a per-instance counter as the version (two instances would give different versions for the same hazard state, which breaks a shared Redis cache).
**Why:** route queries never wait on hazard writes and always see one consistent set of costs. `TestConcurrentRoutingSeesConsistentSnapshots` checks this under `-race`: 27,055 routes ran while the writer swapped the snapshot 141,586 times, and every route's cost equalled the sum of its edges in the snapshot it loaded, with no route using an edge that snapshot blocks.
**Cost:** Day 5 benchmark, wheelchair p50 521 µs with 0 hazards and 569 µs with 50, against 493 µs on Day 2 before the overlay existed.

## 20. Hazard effects and lifecycle
**Chose:** effects per profile, as a pure table in `internal/hazard`:

| Hazard | wheelchair | stroller | walk_winter |
|---|---|---|---|
| elevator_out | block | block | – |
| blocked_sidewalk | block | block | block |
| construction | block | ×2 | ×1.5 |
| ice_snow | ×3 | ×2 | ×4 |
| missing_curb_ramp | block | ×1.5 | – |

`major` severity turns soft effects into blocks for wheelchair and stroller, and doubles walk_winter's multiplier. Several hazards on one edge combine as "any block, otherwise the largest multiplier".

**Lifecycle:** a report starts `active`. 2 confirmations from other sessions make it `confirmed`. 2 resolve votes, or the reporter voting resolve, make it `resolved`. Past its TTL it becomes `expired`. TTLs: ice 12 h, blocked sidewalk 24 h, elevator 48 h, construction 7 d, missing curb ramp 30 d. A confirmation resets the deadline to a full TTL from now, so hazards people keep seeing stay alive and forgotten ones fade out. Votes are one per session per report and can be changed; repeating the same vote writes nothing, so it doesn't bump the graph version. A background worker (`RunExpiry`, every 30 s) expires overdue reports. Several instances can run it safely, because the advisory lock plus `WHERE status IN (live)` expires each report once.
**Why:** walk_winter has no static blocks, so it is the profile that reacts most to hazards, as the plan specifies. The thresholds are low because the campus is small and fast reaction matters more than certainty. Reports decay without anyone having to clean them up.

## 21. Route cache in Redis keyed by graph version; fails open
**Chose:** cache the full route response under `route:v{seq}:{profile}:{from}:{to}`, with coordinates rounded to 5 decimals (~1 m), for 10 minutes. Requests are rounded the same way before routing, so a key always describes exactly one response. Redis calls have a 50 ms timeout, and any Redis error counts as a miss. `X-Cache` and `X-Graph-Version` headers show what happened.
**Why:** a hazard change bumps seq, so new requests use new keys and stale routes are never served, with no invalidation logic. If Redis goes down, routing still works.

## 22. Anonymous sessions via a server-issued id in a header
**Chose:** `POST /api/v1/session` returns a random 128-bit id, which the client sends as `X-Session-ID` on writes. Unknown or missing ids get 401.
**Rejected:** cookies, which would need `SameSite=None; Secure` across the Vercel and Fly domains; and client-generated ids (the server couldn't tell a real session from a made-up string).
**Why:** no accounts (out of scope), but one vote per person per report still needs an identity, and rate limiting (Day 7) will key on it.

## 23. Live updates: WebSocket hub with an edge -> subscribers inverted index
**Chose:** one WebSocket per tab (`GET /ws`, `coder/websocket`). A client subscribes to its viewport and to its active route as `(profile, edge_ids)`. The hub keeps `byEdge: edge id -> clients`:
- `hazard.*` events go to clients whose viewport contains the report.
- `route.affected` goes only to clients indexed under the report's edge, and only if `hazard.EffectOn` blocks or penalizes their profile. An elevator outage doesn't prompt a winter walker.
- It fires only when a report is created; votes on an existing report don't re-prompt.

Each client has a 32-message send buffer, and one goroutine per client does all its writes (WebSocket writes can't run concurrently). A client that falls behind is disconnected rather than slowing the broadcast. `Accept` checks the Origin header against the same allowlist as CORS. The HTTP server's deadlines are cleared before the upgrade, so long-lived sockets aren't cut off.
**Rejected:** broadcasting every event to everyone and letting clients filter (wasteful, and it leaks every user's prompts to everyone), Server-Sent Events (one-way; subscriptions would need a separate channel), and a scan over all clients per hazard (the index makes the route check a single map lookup).
**Why:** the plan's requirement is that only affected users are notified. `TestHubRoutingOfMessages` covers it: a wheelchair route on the edge is prompted, a walk_winter route isn't, an unrelated hazard reaches nobody, a vote doesn't re-prompt, and unsubscribing removes the edge from the index.

## 24. Cross-instance fan-out over Redis pub/sub, with Postgres as the safety net
**Chose:**
- **Publish after commit.** After a hazard write commits, the instance applies it locally (read-your-writes) and publishes the `Change`, which carries the full report state, on `passable:hazard-events`.
- **Every instance handles every event.** Each instance, the writer included, applies the event to its overlay (a duplicate is ignored by seq) and pushes it to its own clients.
- **Two ways to recover lost messages.** Pub/sub is fire-and-forget, so lost messages are recovered two ways: a seq gap triggers a resync, and a catch-up every 30 s compares `max(seq)` in Postgres with the overlay and resyncs if behind. The catch-up exists because a lost *last* message would never cause a gap.
- **Bounded publish.** Publishing has a 500 ms budget and falls back to local delivery. `ContextTimeoutEnabled` is set on the Redis client because go-redis v9 ignores context deadlines without it. Before that fix, a paused Redis made a hazard POST take 5–9 s. After it, 0.52 s, and route requests still answered (0.1 s).

**Rejected:** Redis Streams or a message broker (durable delivery, but Postgres is already the durable log, so a second one would be redundant), and Postgres `LISTEN/NOTIFY` (would work, but the plan puts fan-out on Redis, and it would tie every WebSocket instance to a dedicated DB connection).
**Measured** (`docs/benchmarks/day6-propagation.md`): from POST start to the WebSocket event, p50 6.3 ms and p95 8.4 ms across instances, against 6.2 / 8.3 ms on the same instance. Almost all of it is the POST's own DB transaction. In the browser test, B's toast appeared 40 ms after A's POST started. With Redis paused, B fell one version behind and the catch-up brought it back within 3 s.

## 25. Rate limiting: Redis token buckets, per session and per IP, fail-open
**Chose:** a token bucket per `(rule, key)` in Redis, updated by one Lua script, which Redis runs atomically. Concurrent requests on several instances can't both spend the last token: `TestConcurrentRequestsNeverOverspend` lets exactly 5 of 50 simultaneous requests through.

| Endpoint | Key | Burst | Refill |
|---|---|---|---|
| `POST /hazards` | session | 5 | 1 per 3 min (20/h) |
| `POST /hazards` | IP | 20 | 1/min |
| `POST /hazards/{id}/confirm`, `/resolve` | session | 30 | 1 per 10 s |
| `POST /session` | IP | 10 | 1/min |
| `GET /route` | IP | 60 | 5/s |
| `GET /places` | IP | 30 | ~3/s |
| `GET /hazards` | IP | 60 | 2/s |
| `GET /ws` (connect) | IP | 10 | 1 per 10 s |

Refusals are `429` with `Retry-After`. Writes are limited per session *and* per IP: per session so one person can't flood reports, per IP so minting new sessions doesn't escape the limit. The client IP is read from a proxy header only when one is configured (`TRUSTED_IP_HEADER`, e.g. `Fly-Client-IP`); otherwise anyone could send a fake `X-Forwarded-For` and get a fresh bucket on every request. If Redis is unreachable the limiter allows the request (fail-open, 50 ms budget), and `-rate-limit=false` turns limits off for load tests.
**Rejected:** fixed windows (they allow twice the limit around a window boundary), in-memory limits (each instance would have its own budget), and failing closed (a Redis blip would take reporting down).
**Verified:** a session's 6th report within the burst gets `429 Retry-After: 180`.

## 26. Demo simulator, clearly labelled
**Chose:** `cmd/simulator` acts like a few people. Using only the public API, it reports realistic hazards around the campus core, confirms them, and resolves them, keeping at most N live, and resolves everything it created when stopped with Ctrl-C. Its reports carry `demo: true` (migration 004, `is_demo`), which the UI shows as a "Demo" badge in popups and the reroute toast. It retries points the API rejects as too far from a path, and waits out `429`s.
**Why:** the live features need hazards to show. Simulated data must never be mistaken for real reports, and the simulator never touches non-demo reports.

## 27. Report flow designed for first-time users
**Chose:**
- **One entry point:** a single "Report a problem" button on the map.
- **Three steps:** tap the spot, then pick from 5 plainly named types, each with a one-line hint. One optional yes/no question ("Can someone in a wheelchair get past it?", which maps to severity, defaulted per type), and an optional note.
- **Errors say what to do next.** For example: "That spot isn't on a path we know. Tap right on the sidewalk or path."
- **Votes in the popup.** Every hazard popup has "Still there" / "It's gone".
- **Sessions are invisible.** The session is created on first write and kept in localStorage; on a 401 the client gets a new one and retries.

**Verified** with a headless "stranger" test that finds every control only by its visible text. It covered an off-path tap and its guidance, a successful report, a second person confirming it, and the reporter resolving it. The test caught two layout bugs that are now fixed: the Send button was below the fold at 800 px, and on phones MapLibre's expanded attribution covered the report button (fixed with compact attribution, plus a full-width bottom sheet with pinned buttons).

**Feature freeze** from here on, as planned: Days 8–10 are tests, load tests, deployment and docs only.

## 28. Snapping looks wider when the nearest edges are a disconnected cluster
**Chose:** fetch 64 KNN candidates (was 20). If none of them is on the main network within 150 m, query again with 400 before answering "no mapped path".
**Why:** found by the Day 8 load test. Inside buildings, OSM maps indoor corridors and stairs (`indoor=yes`) as dense networks that aren't connected to the outdoor paths. At one campus point all 20 nearest edges were indoor, so 4% of load-test requests got a wrong 422 even though an outdoor path was ~40 m away. The wider second query only runs in that rare case, so typical requests pay nothing extra. `TestSnapFromInsideBuilding` fails with the old behaviour and passes with the fix.
**Data limitation noted:** indoor networks are in the graph but mostly unreachable from outside, which is one more reason indoor routing is out of scope (README, "Data coverage").

## 29. Test strategy: fast tests by default, real infrastructure behind a build tag
**Chose:**
- **Unit and property tests**, run by `go test ./...` with no Docker needed. They cover the cost model, hazard rules, the overlay including the race stress test, the hub, the rate limiter (on miniredis), and plan() on synthetic graphs.
- **Real-graph tests from a committed fixture** (`testdata/campus-graph.json.gz`, 593 KB, written by `cmd/exportgraph`): golden routes for 3 campus pairs × 3 profiles (lengths and steps/no-steps pinned to the Day 2 results), A* = Dijkstra on 450 random real queries, and `BenchmarkAStarCampus`. They run anywhere, CI included.
- **Integration tests** (`go test -tags integration ./integration`): testcontainers starts PostGIS and Redis, applies the goose migrations, and seeds the campus graph. Each test builds two complete API instances sharing them, so the multi-instance paths are exercised for real: the hazard lifecycle across instances (WebSocket delivery, overlay convergence, cache key change, votes, resolve), the expiry worker, gap → resync, rate limits on real Redis, and the stats recorder. Each test starts from a clean slate, and the suite passes `-count=3`.
- **Load tests:** k6 (`scripts/k6/load.js`).

**Rejected:** using the dev database in tests (state leaks between runs, and it isn't available in CI), and mocking Postgres (the queries that matter, KNN snapping, advisory locks and `RETURNING` CTEs, are exactly what a mock would hide).

## 30. Deployment on free tiers, with no payment method
**Chose:** Vercel (frontend), Fly.io trial (API, one always-on machine in `iad`), Neon (Postgres 18 + PostGIS 3.6, us-east-1) and Upstash Redis signed up for directly (us-east-1, TLS). The API's migrations run as Fly's `release_command`, so schema changes land before the new code starts.
**Rejected:** `fly redis create` (it needs a card on trial organisations), a Fly-hosted Postgres (paid), and Neon's pooled endpoint (PgBouncer in transaction mode breaks pgx's prepared statements, so the API uses the direct endpoint).
**Why:** the only hard constraint was zero cost with no card on file. Everything sits in or next to US East, so the API's tight Redis budgets (50 ms for cache and rate limits, 500 ms for publish) hold. They did not hold from a laptop in Ontario (Upstash round trip ~42 ms), where the cache and rate limiter correctly failed open; from Fly the cache hits as intended.
**Verified on production:**
- health checks pass and the cache hits;
- a route not in the cache averages 11.6 ms on the server;
- server-side propagation is 2.2 ms p50;
- a two-browser test on the live site went from report to "reroute?" prompt in 470 ms, and the reroute avoided the reported path.

**Watch:** the Fly organisation is on a trial plan, so check its limits in the Fly dashboard before relying on the demo staying up.
