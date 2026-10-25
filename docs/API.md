# API reference

Base URL: `http://localhost:8080` locally, `https://passable-api.fly.dev` in production. Every response is JSON. Errors look like `{"error": "message"}`, using these status codes:

| Code | Meaning |
|---|---|
| 400 | invalid input |
| 401 | missing or unknown session |
| 404 | not found |
| 409 | report already resolved or expired |
| 422 | point not near any mapped path |
| 429 | rate limited; see `Retry-After` |

## Routing

### `GET /api/v1/route?from=lat,lng&to=lat,lng&profile=wheelchair`
`profile` is `wheelchair`, `stroller` or `walk_winter`. Both points must be inside the service area (`43.455,-80.565` to `43.485,-80.505`).

```sh
curl 'localhost:8080/api/v1/route?from=43.4718,-80.5452&to=43.4685,-80.5418&profile=wheelchair'
```

Response fields:
- `accessible`: `false` means no fully accessible route exists. The route shown crosses the fewest barriers, and `barriers` names them.
- `distance_m`, `cost`, `nodes_expanded`, `search_ms`, and `graph_version` (the hazard state the route was computed against).
- `snap.from` / `snap.to`: the edge each point was attached to, and how far away it was.
- `confidence_m`: metres of the route labelled `verified`, `inferred` and `unknown`.
- `notes`: plain-language warnings.
- `segments`: consecutive edges with the same description, name, surface, kerb, confidence and penalties, merged into one entry. Each has `[lon, lat]` coordinates and any live `hazards` on it.

Response headers: `X-Cache: HIT|MISS` and `X-Graph-Version`.

### `GET /api/v1/places?q=dc`
Place search. It matches UW building codes exactly (`dc`, `slc`, `e7`), matches word prefixes (`dav`), and tolerates typos (`dana portr`). Returns up to 10 `results` with `name`, `ref`, `kind` and `point`.

## Hazard reports

Writes need an anonymous session id in the `X-Session-ID` header:

```sh
SID=$(curl -s -X POST localhost:8080/api/v1/session | jq -r .session_id)
curl -X POST localhost:8080/api/v1/hazards -H "X-Session-ID: $SID" -H 'Content-Type: application/json' \
     -d '{"lat":43.47087,"lng":-80.54274,"type":"blocked_sidewalk","note":"fenced off"}'
curl -X POST localhost:8080/api/v1/hazards/1/confirm -H "X-Session-ID: $SID"     # "still there"
curl -X POST localhost:8080/api/v1/hazards/1/resolve -H "X-Session-ID: $SID"     # "it's gone"
curl 'localhost:8080/api/v1/hazards?bbox=-80.565,43.455,-80.505,43.485'          # live reports in a box
curl localhost:8080/api/v1/graph/version                                          # {"version": n, "live_hazards": n}
```

| Field | Values |
|---|---|
| `type` | `elevator_out`, `blocked_sidewalk`, `construction`, `ice_snow`, `missing_curb_ramp` |
| `severity` | `major` (blocks wheelchair and stroller) or `minor` (slows them down). Optional: defaults per type |
| `note` | optional, up to 280 characters |

The point is attached to the nearest path within 30 m. Otherwise the API returns 422.

**Lifecycle:** a report starts `active`. Two confirmations from other sessions make it `confirmed`. Two resolve votes, or a resolve from the reporter, make it `resolved`. A report that passes its TTL becomes `expired`. TTLs: ice 12 h, blocked sidewalk 24 h, elevator 48 h, construction 7 days, missing curb ramp 30 days. Each confirmation resets the TTL. One vote per session per report; voting again changes your vote.

**Rate limits:** writes are limited per session and per IP. For example, a session can file 5 reports at once, then 20 per hour. See `DECISIONS.md` #25 for every limit.

## Live updates: `GET /ws` (WebSocket)

Client → server:
```json
{"type": "subscribe.viewport", "bbox": [west, south, east, north]}
{"type": "subscribe.route", "profile": "wheelchair", "edge_ids": [4035, 4034]}
{"type": "unsubscribe.route"}
```

Server → client:
```json
{"type": "hello", "graph_version": 12}
{"type": "hazard.created", "hazard": {...}, "graph_version": 13}
{"type": "hazard.updated", "hazard": {...}, "graph_version": 14}
{"type": "hazard.expired", "hazard": {...}, "graph_version": 15}
{"type": "route.affected", "hazard": {...}, "edge_id": 4035, "graph_version": 13}
{"type": "error", "message": "..."}
```
- `hazard.*` events go to clients whose viewport contains the report.
- `route.affected` goes only to clients whose subscribed route uses the reported edge, and only when the hazard changes that client's profile costs. For example, an elevator outage doesn't prompt a winter walker.
- A resolved report arrives as `hazard.updated` with `status: "resolved"`.

## Operations
| Endpoint | Purpose |
|---|---|
| `GET /healthz` | 200 when the database answers (used by Fly's health check) |
| `GET /metrics` | Prometheus metrics: route latency by profile and cache result, nodes expanded, hazard events, propagation latency, rate-limit rejections, live gauges |
| `GET /api/v1/status` | JSON for the public `/status` page: 15-minute route percentiles from real traffic, cache hit rate, propagation, live counts |
