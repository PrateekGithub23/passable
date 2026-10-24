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
