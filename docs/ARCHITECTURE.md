# Architecture

## Components

```mermaid
flowchart TB
    subgraph Browser["Browser: Next.js + MapLibre"]
        UI["Map, route panel, report form"]
    end

    subgraph API["Go API (one or more instances)"]
        H["HTTP handlers"]
        R["A* router"]
        S["Snapshot (atomic.Pointer)"]
        G["Base graph: immutable, in memory"]
        O["Hazard overlay: copy-on-write"]
        RS["Reports service + expiry worker"]
        HUB["WebSocket hub: edge → subscribers"]
    end

    subgraph Data["Data stores"]
        PG[("PostgreSQL + PostGIS: source of truth")]
        RD[("Redis: cache, pub/sub, rate limits")]
    end

    UI -->|"REST: route, report, vote"| H
    HUB -.->|"WebSocket: hazard.*, route.affected"| UI
    H --> R
    R -->|"one load per query"| S
    S --> G
    S --> O
    H -->|"report / vote"| RS
    H -->|"KNN snapping"| PG
    H -->|"route cache, rate limits"| RD
    RS -->|"transaction + event log (seq)"| PG
    RS <-->|"pub/sub: hazard changes"| RD
    RS -->|"apply in seq order"| O
    RS -->|"broadcast"| HUB
```

| Component | Code | Role |
|---|---|---|
| Importer | `cmd/importer` | Splits OSM ways into graph edges at junctions and loads Postgres (one transaction) |
| Base graph | `internal/graph` | Immutable nodes and undirected edges with CSR adjacency, loaded once at startup (~9 MiB) |
| Cost model | `internal/cost` | Per-profile edge costs from OSM tags: `length × multiplier + penalty`, or blocked |
| Router | `internal/route` | A* with a haversine heuristic; starts and ends partway along edges |
| Hazard rules | `internal/hazard` | Pure functions: effects per profile, TTLs, lifecycle state machine |
| Overlay | `internal/overlay` | Immutable snapshots of live hazards behind `atomic.Pointer` |
| Reports | `internal/reports` | Postgres writes under a global advisory lock, event log, expiry worker, resync |
| Live | `internal/live` | WebSocket hub (viewport + route subscriptions) and the Redis pub/sub bus |
| API | `internal/api` | HTTP handlers, snapping, segment explanations, CORS, rate limits, stats |

## Flow 1: a route request

```mermaid
sequenceDiagram
    participant B as Browser
    participant A as API
    participant C as Redis
    participant P as Postgres
    B->>A: GET /route?from&to&profile
    A->>A: snap = overlay.Load()  (one atomic read)
    A->>C: GET route:v{snap.Seq}:{profile}:{from}:{to}
    alt cache hit
        C-->>A: cached JSON
    else miss
        A->>P: KNN: nearest edges to from / to (GiST <->)
        A->>A: re-rank in metres, keep edges usable by profile
        A->>A: A* with cost(e) = snap.Cost(base[e])
        A->>A: merge into segments, label confidence, explain
        A->>C: SET (10 min TTL)
    end
    A-->>B: route + X-Graph-Version
```

The whole request uses **one** overlay snapshot. If a hazard lands mid-request, this request keeps its consistent view, and the next one sees the new version under a new cache key.

## Flow 2: a hazard report reaching other users

```mermaid
sequenceDiagram
    participant U as Reporter
    participant A1 as API instance 1
    participant P as Postgres
    participant R as Redis
    participant A2 as API instance 2
    participant V as Other user (route on that edge)
    U->>A1: POST /hazards
    A1->>P: BEGIN, advisory lock, INSERT report + event (seq = n), COMMIT
    A1->>A1: apply event n to own overlay (read-your-writes)
    A1->>R: PUBLISH change n
    R-->>A1: change n (already applied: ignored)
    R-->>A2: change n
    A2->>A2: apply n if seq = current + 1, else resync from Postgres
    A2->>V: route.affected (edge index lookup, profile check)
    V->>A2: GET /route (new version → new cache key → fresh route)
```

## Guarantees and how they're kept

| Guarantee | Mechanism | Tested by |
|---|---|---|
| A query never sees half-applied hazard changes | Immutable snapshots, one `atomic.Pointer` load per query | `TestConcurrentRoutingSeesConsistentSnapshots` (race detector, 27k routes against 141k swaps) |
| Searches never wait for writers | Writers copy small maps and swap the pointer; readers take no locks | same |
| Every instance agrees on what version N means | `pg_advisory_xact_lock` serializes hazard writes, so seq order equals commit order | `TestHazardLifecycleAcrossInstances` |
| Stale routes are never served | The cache key contains the graph version | `TestPlanSeesHazards`, integration test |
| Lost pub/sub messages don't leave an instance wrong | A gap causes a resync from Postgres; a periodic catch-up (`EXPIRY_INTERVAL`: 30 s by default, 10 min on Fly) handles a lost last message | `TestGapTriggersResync`; manual Redis-pause test (DECISIONS #24) |
| Only affected users are prompted | Inverted index edge → clients, plus a per-profile effect check | `TestHubRoutingOfMessages` |
| A* returns optimal routes | Every cost ≥ edge length, and the haversine heuristic is admissible | `TestCostNeverBelowLength`, `TestAStarMatchesDijkstra*` |

The reasoning behind each choice, and what was rejected, is in [`DECISIONS.md`](../DECISIONS.md).
