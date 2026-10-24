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
