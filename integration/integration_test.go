//go:build integration

// Package integration runs the API against real Postgres+PostGIS and Redis started in Docker by
// testcontainers. Run with:
//
//	go test -tags integration -v ./integration
//
// Setup (TestMain): start both containers, apply the goose migrations, and load the real campus
// graph from testdata/campus-graph.json.gz into the database (edge geometry is the straight line
// between endpoints; lengths and tags are the real ones). Each test then builds TWO complete API
// instances sharing that Postgres and Redis, the way two production replicas would.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PrateekGithub23/passable/db/migrations"
	"github.com/PrateekGithub23/passable/internal/api"
	"github.com/PrateekGithub23/passable/internal/cache"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/live"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/PrateekGithub23/passable/internal/ratelimit"
	"github.com/PrateekGithub23/passable/internal/reports"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Shared by all tests (set up once in TestMain).
var (
	pool *pgxpool.Pool
	rdb  *redis.Client
	g    *graph.Graph
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:         "postgis/postgis:16-3.4",
			ImagePlatform: "linux/amd64", // no arm64 build of this image
			Env:           map[string]string{"POSTGRES_USER": "t", "POSTGRES_PASSWORD": "t", "POSTGRES_DB": "t"},
			ExposedPorts:  []string{"5432/tcp"},
			// The image starts Postgres twice (init, then for real); wait for the second.
			WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	must(err)
	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "redis:7-alpine", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForListeningPort("6379/tcp"),
		},
		Started: true,
	})
	must(err)

	pgURL := "postgres://t:t@" + endpoint(ctx, pgC, "5432/tcp") + "/t"
	migrate(pgURL)
	pool, err = pgxpool.New(ctx, pgURL)
	must(err)
	seedCampus(ctx)
	g, err = graph.Load(ctx, pool)
	must(err)
	opts := &redis.Options{Addr: endpoint(ctx, redisC, "6379/tcp"), ContextTimeoutEnabled: true}
	rdb = redis.NewClient(opts)

	code := m.Run()
	pool.Close()
	pgC.Terminate(ctx)
	redisC.Terminate(ctx)
	os.Exit(code)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func endpoint(ctx context.Context, c testcontainers.Container, port string) string {
	host, err := c.Host(ctx)
	must(err)
	p, err := c.MappedPort(ctx, port)
	must(err)
	return fmt.Sprintf("%s:%s", host, p.Port())
}

// migrate applies every migration, exactly as cmd/migrate does.
func migrate(url string) {
	db, err := sql.Open("pgx", url)
	must(err)
	defer db.Close()
	goose.SetBaseFS(migrations.FS)
	must(goose.SetDialect("postgres"))
	must(goose.Up(db, "."))
}

// seedCampus inserts the fixture's nodes and edges (keeping edge ids, so they match the fixture).
func seedCampus(ctx context.Context) {
	f, err := os.Open("../testdata/campus-graph.json.gz")
	must(err)
	defer f.Close()
	fg, err := graph.ReadFixture(f)
	must(err)
	tx, err := pool.Begin(ctx)
	must(err)
	defer tx.Rollback(ctx)
	ids, lats, lons := make([]int64, len(fg.Nodes)), make([]float64, len(fg.Nodes)), make([]float64, len(fg.Nodes))
	for i, n := range fg.Nodes {
		ids[i], lats[i], lons[i] = n.OSMID, n.Lat, n.Lon
	}
	_, err = tx.Exec(ctx, `INSERT INTO nodes (id, geom) SELECT id, ST_SetSRID(ST_MakePoint(lon, lat), 4326)
		FROM unnest($1::bigint[], $2::float8[], $3::float8[]) AS t(id, lat, lon)`, ids, lats, lons)
	must(err)
	n := len(fg.Edges)
	eids, from, to, lens := make([]int64, n), make([]int64, n), make([]int64, n), make([]float64, n)
	hw, sf, fw, ks, tags := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	steps, cross := make([]bool, n), make([]bool, n)
	for i, e := range fg.Edges {
		eids[i], from[i], to[i], lens[i] = e.ID, fg.Nodes[e.From].OSMID, fg.Nodes[e.To].OSMID, e.LengthM
		hw[i], sf[i], fw[i], ks[i] = e.Highway, e.Surface, e.Footway, e.KerbStart
		steps[i], cross[i] = e.HasSteps, e.IsCrossing
		t, _ := json.Marshal(map[string]string{"sidewalk": e.Sidewalk})
		tags[i] = string(t)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO edges (id, osm_way_id, from_node, to_node, length_m, geom, highway, surface, footway, kerb_start, has_steps, is_crossing, tags)
		SELECT e.id, 0, e.f, e.t, e.l, ST_MakeLine(a.geom, b.geom), e.hw, nullif(e.sf, ''), nullif(e.fw, ''), nullif(e.ks, ''), e.st, e.cr, e.tags::jsonb
		FROM unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::float8[], $5::text[], $6::text[], $7::text[], $8::text[], $9::bool[], $10::bool[], $11::text[])
		  AS e(id, f, t, l, hw, sf, fw, ks, st, cr, tags)
		JOIN nodes a ON a.id = e.f JOIN nodes b ON b.id = e.t`,
		eids, from, to, lens, hw, sf, fw, ks, steps, cross, tags)
	must(err)
	_, err = tx.Exec(ctx, `SELECT setval('edges_id_seq', (SELECT max(id) FROM edges))`)
	must(err)
	must(tx.Commit(ctx))
}

// reset gives each test a clean slate: no hazards, votes or events (so the graph version starts
// at 0 again), and an empty Redis (no cached routes or rate-limit buckets from earlier tests).
// Without it, tests depend on each other's leftovers and fail when run twice (-count=2).
func reset(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE hazard_events, report_votes, hazard_reports, route_requests RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
}

// instance is one complete API server, wired like cmd/api/main.go.
type instance struct {
	url     string
	svc     *reports.Service
	overlay *overlay.Holder
}

func newInstance(t *testing.T, limits bool) *instance {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	holder := overlay.NewHolder(overlay.Build(0, nil))
	svc := reports.New(pool, g, holder)
	if err := svc.Resync(ctx); err != nil {
		t.Fatal(err)
	}
	hub := live.NewHub(nil, func() int64 { return holder.Load().Seq })
	bus, err := live.NewBus(ctx, rdb)
	if err != nil {
		t.Fatal(err)
	}
	svc.OnCommit = func(ch reports.Change) { bus.Publish(ctx, ch) }
	go bus.Run(ctx, func(ch reports.Change) { svc.ApplyChange(ctx, ch); hub.Broadcast(ch) })
	rec := api.NewRecorder(pool)
	go rec.Run(ctx)
	cfg := api.Config{DB: pool, Graph: g, Overlay: holder, Reports: svc, Cache: cache.New(rdb, time.Minute), Live: hub, Recorder: rec}
	if limits {
		cfg.Limiter = ratelimit.New(rdb)
	}
	srv := httptest.NewServer(api.New(cfg))
	t.Cleanup(func() { srv.Close(); cancel() })
	return &instance{url: srv.URL, svc: svc, overlay: holder}
}

// ---- small HTTP helpers ----

type routeResp struct {
	GraphVersion int64   `json:"graph_version"`
	DistanceM    float64 `json:"distance_m"`
	Accessible   bool    `json:"accessible"`
	Segments     []struct {
		EdgeIDs     []int64      `json:"edge_ids"`
		Coordinates [][2]float64 `json:"coordinates"`
	} `json:"segments"`
}

func (r routeResp) edges() []int64 {
	var out []int64
	for _, s := range r.Segments {
		out = append(out, s.EdgeIDs...)
	}
	return out
}

const slcToHagey = "from=43.4718,-80.5452&to=43.4685,-80.5418&profile=wheelchair"

func getRoute(t *testing.T, base string) (routeResp, string) {
	t.Helper()
	res, err := http.Get(base + "/api/v1/route?" + slcToHagey)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var r routeResp
	if res.StatusCode != 200 {
		t.Fatalf("route: HTTP %d", res.StatusCode)
	}
	json.NewDecoder(res.Body).Decode(&r)
	return r, res.Header.Get("X-Cache")
}

func post(t *testing.T, url, sid string, body any) (int, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest("POST", url, rd)
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set("X-Session-ID", sid)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func session(t *testing.T, base string) string {
	t.Helper()
	code, out := post(t, base+"/api/v1/session", "", nil)
	if code != 201 {
		t.Fatalf("session: HTTP %d", code)
	}
	return out["session_id"].(string)
}

// eventually polls cond for up to 3 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---- tests ----

// TestHazardLifecycleAcrossInstances is the end-to-end story on real infrastructure:
// a report filed on instance A changes routes on instance B, B's WebSocket clients hear about
// it, the cache key changes, votes move it through its lifecycle, and resolving restores routes.
func TestHazardLifecycleAcrossInstances(t *testing.T) {
	reset(t)
	A, B := newInstance(t, false), newInstance(t, false)

	// Baseline route on B, and the cache: second identical request is a hit.
	before, cacheState := getRoute(t, B.url)
	if cacheState != "MISS" {
		t.Fatalf("first request: X-Cache %s, want MISS", cacheState)
	}
	if _, again := getRoute(t, B.url); again != "HIT" {
		t.Fatalf("second request: X-Cache %s, want HIT", again)
	}

	// A WebSocket client on B watches this route and the area.
	ctx := context.Background()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(B.url, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	msgs := make(chan map[string]any, 32)
	go func() {
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			msgs <- m
		}
	}()
	sub := func(v any) { b, _ := json.Marshal(v); ws.Write(ctx, websocket.MessageText, b) }
	sub(map[string]any{"type": "subscribe.viewport", "bbox": []float64{-80.565, 43.455, -80.505, 43.485}})
	sub(map[string]any{"type": "subscribe.route", "profile": "wheelchair", "edge_ids": before.edges()})
	time.Sleep(200 * time.Millisecond)

	// File a blocked sidewalk on A, on the middle of B's route.
	mid := before.Segments[len(before.Segments)/2].Coordinates
	pt := mid[len(mid)/2]
	reporter := session(t, A.url)
	code, rep := post(t, A.url+"/api/v1/hazards", reporter, map[string]any{"lat": pt[1], "lng": pt[0], "type": "blocked_sidewalk"})
	if code != 201 {
		t.Fatalf("create hazard: HTTP %d %v", code, rep)
	}
	id, edge := int64(rep["id"].(float64)), int64(rep["edge_id"].(float64))
	if !slices.Contains(before.edges(), edge) {
		t.Fatalf("hazard snapped to edge %d, which is not on the route", edge)
	}

	// B's WebSocket client gets both the viewport event and the route prompt.
	got := map[string]bool{}
	timeout := time.After(3 * time.Second)
	for !got["hazard.created"] || !got["route.affected"] {
		select {
		case m := <-msgs:
			got[m["type"].(string)] = true
		case <-timeout:
			t.Fatalf("WebSocket on B received %v; want hazard.created and route.affected", got)
		}
	}

	// B's overlay converges to A's version via Redis.
	eventually(t, "B overlay catches up", func() bool { return B.overlay.Load().Seq == A.overlay.Load().Seq })

	// B now routes around it: new version => new cache key => MISS, and the edge is avoided.
	after, cacheState := getRoute(t, B.url)
	if cacheState != "MISS" || after.GraphVersion != before.GraphVersion+1 {
		t.Fatalf("after hazard: X-Cache %s version %d, want MISS at %d", cacheState, after.GraphVersion, before.GraphVersion+1)
	}
	if slices.Contains(after.edges(), edge) {
		t.Fatalf("new route still uses blocked edge %d", edge)
	}

	// Votes: two other people confirm -> confirmed. Repeating a vote doesn't bump the version.
	s2, s3 := session(t, A.url), session(t, B.url)
	post(t, fmt.Sprintf("%s/api/v1/hazards/%d/confirm", A.url, id), s2, nil)
	v := A.overlay.Load().Seq
	post(t, fmt.Sprintf("%s/api/v1/hazards/%d/confirm", A.url, id), s2, nil)
	if A.overlay.Load().Seq != v {
		t.Error("a repeated identical vote changed the graph version")
	}
	_, out := post(t, fmt.Sprintf("%s/api/v1/hazards/%d/confirm", B.url, id), s3, nil)
	if out["status"] != "confirmed" {
		t.Fatalf("after 2 confirmations: status %v", out["status"])
	}

	// The reporter resolves it from B; A's routes return to the original.
	_, out = post(t, fmt.Sprintf("%s/api/v1/hazards/%d/resolve", B.url, id), reporter, nil)
	if out["status"] != "resolved" {
		t.Fatalf("reporter resolve: status %v", out["status"])
	}
	eventually(t, "A sees the resolve", func() bool { return A.overlay.Load().Len() == 0 })
	final, _ := getRoute(t, A.url)
	if final.DistanceM != before.DistanceM {
		t.Fatalf("after resolve: %.1f m, want the original %.1f m", final.DistanceM, before.DistanceM)
	}
	code, _ = post(t, fmt.Sprintf("%s/api/v1/hazards/%d/confirm", A.url, id), s3, nil)
	if code != http.StatusConflict {
		t.Fatalf("vote on resolved report: HTTP %d, want 409", code)
	}
}

// TestSnapFromInsideBuilding is a regression test for a load-test finding: this point is inside
// a building whose indoor corridors form a dense network disconnected from the outdoor paths.
// The 20 nearest edges were all indoor, so the API used to answer "no mapped path within 150 m"
// although an outdoor path is ~40 m away.
func TestSnapFromInsideBuilding(t *testing.T) {
	reset(t)
	A := newInstance(t, false)
	for _, p := range []string{"wheelchair", "stroller", "walk_winter"} {
		res, err := http.Get(A.url + "/api/v1/route?from=43.472777,-80.543046&to=43.470068,-80.539312&profile=" + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Errorf("%s: HTTP %d, want 200", p, res.StatusCode)
		}
	}
}

// TestExpiryWorker: a report past its deadline is expired by the worker, logged as an event,
// and removed from every instance's overlay.
func TestExpiryWorker(t *testing.T) {
	reset(t)
	A, B := newInstance(t, false), newInstance(t, false)
	sid := session(t, A.url)
	_, rep := post(t, A.url+"/api/v1/hazards", sid, map[string]any{"lat": 43.4708681, "lng": -80.5427402, "type": "ice_snow"})
	id := int64(rep["id"].(float64))
	eventually(t, "B sees the new hazard", func() bool { return B.overlay.Load().Len() == 1 })

	if _, err := pool.Exec(context.Background(), `UPDATE hazard_reports SET expires_at = now() - interval '1 s' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	n, err := A.svc.ExpireDue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ExpireDue: %d, %v; want 1", n, err)
	}
	eventually(t, "B drops the expired hazard", func() bool { return B.overlay.Load().Len() == 0 })
	var kind, status string
	pool.QueryRow(context.Background(), `SELECT kind FROM hazard_events WHERE report_id = $1 ORDER BY seq DESC LIMIT 1`, id).Scan(&kind)
	pool.QueryRow(context.Background(), `SELECT status FROM hazard_reports WHERE id = $1`, id).Scan(&status)
	if kind != "expired" || status != "expired" {
		t.Fatalf("event %q, status %q; want expired/expired", kind, status)
	}
}

// TestGapTriggersResync: an instance that receives an event from the future (it missed some)
// reloads from Postgres instead of applying it, and ends up exactly at the database's state.
func TestGapTriggersResync(t *testing.T) {
	reset(t)
	A := newInstance(t, false)
	sid := session(t, A.url)
	post(t, A.url+"/api/v1/hazards", sid, map[string]any{"lat": 43.4708681, "lng": -80.5427402, "type": "construction"})

	var dbSeq int64
	var live int
	pool.QueryRow(context.Background(), `SELECT max(seq) FROM hazard_events`).Scan(&dbSeq)
	pool.QueryRow(context.Background(), `SELECT count(*) FROM hazard_reports WHERE status IN ('active','confirmed')`).Scan(&live)

	// A fresh service with an empty overlay (version 0) stands in for an instance that missed
	// events. Delivering an event from far in the future must not be applied as-is: it's a gap,
	// so the service reloads everything from Postgres.
	stale := overlay.NewHolder(overlay.Build(0, nil))
	svc := reports.New(pool, g, stale)
	svc.ApplyChange(context.Background(), reports.Change{Seq: dbSeq + 50, Report: reports.Report{ID: 999999, EdgeID: g.Edges[0].ID, Status: "active"}})
	if stale.Load().Seq != dbSeq || stale.Load().Len() != live {
		t.Fatalf("after gap: overlay at %d with %d hazards, want DB state %d with %d", stale.Load().Seq, stale.Load().Len(), dbSeq, live)
	}
}

// TestRateLimitWithRealRedis: the 6th report from one session within the burst is refused.
func TestRateLimitWithRealRedis(t *testing.T) {
	reset(t)
	A := newInstance(t, true)
	sid := session(t, A.url)
	var codes []int
	for i := 0; i < 6; i++ {
		code, _ := post(t, A.url+"/api/v1/hazards", sid, map[string]any{"lat": 43.4708681, "lng": -80.5427402, "type": "ice_snow"})
		codes = append(codes, code)
	}
	if !slices.Equal(codes, []int{201, 201, 201, 201, 201, 429}) {
		t.Fatalf("status codes %v, want five 201s then 429", codes)
	}
}

// TestRecorderWritesRouteRequests: route requests (hits and misses) land in route_requests.
func TestRecorderWritesRouteRequests(t *testing.T) {
	reset(t)
	A := newInstance(t, false)
	var before int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM route_requests`).Scan(&before)
	getRoute(t, A.url)
	getRoute(t, A.url)
	eventually(t, "two rows written", func() bool {
		var n int
		pool.QueryRow(context.Background(), `SELECT count(*) FROM route_requests`).Scan(&n)
		return n >= before+2
	})
	var hits int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM route_requests WHERE cache_hit`).Scan(&hits)
	if hits == 0 {
		t.Fatal("no cache_hit row recorded")
	}
}
