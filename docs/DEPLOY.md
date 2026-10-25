# Deploying Passable

Production layout:

| Piece | Service | Why |
|---|---|---|
| Go API (routing, hazards, WebSocket) | **Fly.io** (`fly.toml`, `Dockerfile`) | Runs our Docker image; long-lived WebSocket connections; one always-on machine keeps the graph in memory |
| Postgres + PostGIS | **Neon** | Managed Postgres with the PostGIS and pg_trgm extensions |
| Redis (cache, pub/sub, rate limits) | **Docker Redis** in development; in production, any compatible **free-tier hosted Redis** (see [Redis](#redis)) | Optional: the API runs without it, with reduced features |
| Next.js frontend | **Vercel** | Builds and serves `web/`, deploys on every push via its GitHub integration |

Secrets (`DATABASE_URL`, `REDIS_URL`, `FLY_API_TOKEN`) are never committed. Locally they live in `.env` (git-ignored), and in production in Fly secrets and GitHub Actions secrets.

## One-time setup

### 1. Accounts and CLIs
Create accounts on fly.io, neon.tech and vercel.com (signing in with GitHub is simplest). Then:

```sh
brew install flyctl
npm install -g vercel
fly auth login
vercel login
```

### 2. Database (Neon)
1. Create a project in the region **AWS US East (N. Virginia)**, which is close to Fly's `iad` region.
2. Copy the **direct** connection string (connection pooling **off**). The pooled endpoint uses PgBouncer in transaction mode, which breaks the server-side prepared statements that pgx uses.
3. Load the schema and the campus data from your machine. This needs `data/*.json` from `scripts/fetch_osm.sh`:

```sh
set -a; source .env; set +a                   # DATABASE_URL, REDIS_URL
go run ./cmd/migrate -db "$DATABASE_URL"
go run ./cmd/importer -db "$DATABASE_URL"     # ~21k edges, a few seconds
```

> The importer runs `TRUNCATE edges, nodes, places ... CASCADE`, so re-importing also deletes hazard reports (they reference edges). Only re-import on purpose.

### 3. Redis
See [Redis](#redis) below. Development needs nothing beyond `docker compose up`. For production, either add a free-tier hosted Redis URL to `.env` as `REDIS_URL`, or deploy without one (with the trade-offs listed there).

### 4. API (Fly.io)
```sh
fly launch --no-deploy --copy-config      # keeps fly.toml; pick a unique app name if asked
fly secrets set DATABASE_URL="$DATABASE_URL" REDIS_URL="$REDIS_URL"   # omit REDIS_URL to run without Redis
fly deploy                                # runs /app/migrate first (release_command), then starts the API
curl https://<app>.fly.dev/healthz
```

### 5. Frontend (Vercel)
```sh
cd web
vercel link                                         # create/link the project (root directory: web)
vercel env add NEXT_PUBLIC_API_URL production       # value: https://<app>.fly.dev
vercel --prod
```
Then let the API accept requests from the site:
```sh
fly secrets set CORS_ORIGINS="https://<your-site>.vercel.app,http://localhost:3000"
```
(Secrets override the `[env]` value in `fly.toml`. Setting one restarts the app.)

To deploy on every push, connect the GitHub repo in the Vercel dashboard (Project → Settings → Git) with root directory `web`.

### 6. Continuous deployment of the API
```sh
fly tokens create deploy -x 999999h       # prints a token
```
Add it on GitHub under Settings → Secrets and variables → Actions → **New repository secret**, named `FLY_API_TOKEN`. From then on, each push to `main` that passes CI also runs `flyctl deploy` (`.github/workflows/ci.yml`).

## Checks after a deploy
- `https://<app>.fly.dev/healthz` returns `{"status":"ok",...}`.
- `https://<site>.vercel.app` loads. Clicking two points shows a route.
- `https://<site>.vercel.app/status` says "All systems operational".
- Report a hazard in one browser and confirm it appears in another (live updates through Fly's proxy).
- `https://<app>.fly.dev/metrics` serves Prometheus metrics.

## Redis

### What Redis is used for
| Role | Redis commands | Without Redis |
|---|---|---|
| Route cache (`internal/cache`) | `GET`, `SET` with expiry | Every route is computed (still ~3 ms p50) |
| Event bus between API instances (`internal/live/bus.go`) | `PUBLISH`, `SUBSCRIBE` (one long-lived connection per instance) | Only one API instance can run; hazards still reach that instance's WebSocket clients |
| Rate limiting (`internal/ratelimit`) | `EVALSHA`/`EVAL` (Lua) running `HMGET`, `HSET`, `PEXPIRE` | **No rate limits.** Fine for local development, not for a public deployment |

Any provider that supports these commands works with Redis 6.2+ semantics. The URL format decides TLS:
- `redis://localhost:6379/0`: plain TCP, used for local Docker.
- `rediss://default:<password>@<host>:<port>`: TLS (note the extra **s**). Hosted providers usually require it.

Both forms are covered by `TestRedisOptions` in `cmd/api/main_test.go`.

### Development: local Docker Redis (default)
`docker-compose.yml` runs `redis:7-alpine` on `localhost:6379`. When `REDIS_URL` is unset, the API defaults to `redis://localhost:6379/0`, so nothing needs configuring. To check it:

```sh
docker compose up -d --wait
docker compose exec redis redis-cli ping                       # PONG
go run ./cmd/api                                               # log: "route cache + event bus: redis localhost:6379", "rate limits: on"
curl -s -D - -o /dev/null 'localhost:8080/api/v1/route?from=43.4718,-80.5452&to=43.4685,-80.5418&profile=wheelchair' | grep X-Cache   # MISS, then HIT on repeat
docker compose exec redis redis-cli PUBSUB NUMSUB passable:hazard-events                                                                # 1 (the API)
```

### Production: a free-tier hosted Redis, later
Hosted Redis isn't required to deploy. When you want it, choose a provider whose free plan needs **no payment method** and supports **pub/sub and Lua scripts**. Plans change, so check the provider's pricing page for:
1. **No credit card required.** Fly's own `fly redis create` needs a card on trial organisations, so sign up with the provider directly instead.
2. **`PUBLISH`/`SUBSCRIBE` and `EVAL`/`EVALSHA` supported.** Some serverless or "REST-only" offerings restrict these. The API needs a normal TCP/TLS Redis connection, not an HTTP API.
3. **A command quota that fits the traffic.** Each route request uses about 2–3 commands (rate-limit script, cache `GET`, plus a `SET` on a miss). Each hazard write uses about 3. A quota of N commands per day covers roughly N/3 route requests per day.
4. **A region close to the API and database** (US East for this setup), otherwise every cached lookup pays the extra round trip.

Free tiers commonly listed as meeting these at the time of writing include **Upstash** (sign up at upstash.com directly, not through Fly) and **Redis Cloud's free database** (redis.io). Verify current terms before relying on them.

Then:
1. Create the database. Pick a region near the API, and enable TLS if it's optional.
2. Copy its connection URL, in the `rediss://...` form, into `.env` as `REDIS_URL`. Never commit it.
3. Check it from your machine before deploying:
   ```sh
   set -a; source .env; set +a
   go run ./cmd/api -addr :8099 -db postgres://passable:passable@localhost:5433/passable
   # expect: "route cache + event bus: redis <host>:<port>" and "rate limits: on"
   ```
4. `fly secrets set REDIS_URL="$REDIS_URL"`, which restarts the API with Redis enabled.

### Running without Redis
Set `REDIS_URL` to an empty string (`fly secrets set REDIS_URL=""`, or `REDIS_URL= go run ./cmd/api` locally). Unset means "use local Docker Redis"; empty means "Redis off". The API logs `redis disabled (REDIS_URL is empty): ...` and keeps working: routing, hazards and live updates all run on a **single** instance. If a configured Redis is unreachable at startup, the API also runs without it and logs `redis unavailable (...)`. Keep the Fly app at one machine in this mode (`min_machines_running = 1`, and don't scale out), and keep in mind that rate limiting is off.
