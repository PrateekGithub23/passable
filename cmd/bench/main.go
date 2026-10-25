// Command bench measures A* latency and search effort on random queries over the real graph.
//
//	go run ./cmd/bench [-n 2000] [-seed 42] [-hazards 50]
//
// -hazards N places N random hazards (half blocked sidewalks, half ice) on the overlay, so the
// numbers include the cost of the copy-on-write overlay lookup on the routing hot path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"runtime"
	"slices"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/hazard"
	"github.com/PrateekGithub23/passable/internal/overlay"
	"github.com/PrateekGithub23/passable/internal/route"
	"github.com/jackc/pgx/v5"
)

func main() {
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	n := flag.Int("n", 2000, "queries per profile")
	seed := flag.Int64("seed", 42, "random seed for query pairs")
	nHazards := flag.Int("hazards", 0, "random live hazards to place on the overlay")
	flag.Parse()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	// Graph memory = heap growth across the load, each side measured after a GC so garbage
	// from parsing doesn't count.
	before := heapAfterGC()
	loadStart := time.Now()
	g, err := graph.Load(ctx, conn)
	loadTime := time.Since(loadStart)
	conn.Close(ctx)
	if err != nil {
		log.Fatalf("load graph: %v", err)
	}
	graphBytes := heapAfterGC() - before

	fmt.Printf("go %s %s/%s, %d CPUs\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Printf("graph: %d nodes, %d edges, load %s, heap %.1f MiB\n",
		len(g.Nodes), len(g.Edges), loadTime.Round(time.Millisecond), float64(graphBytes)/(1<<20))
	// Random hazards, from their own seeded generator so query pairs stay identical across
	// runs with different -hazards values.
	var live []overlay.Hazard
	hr := rand.New(rand.NewSource(*seed + 1))
	for i := 0; i < *nHazards; i++ {
		h := overlay.Hazard{ID: int64(i + 1), Edge: int32(hr.Intn(len(g.Edges))), Type: hazard.BlockedSidewalk, Severity: hazard.Major, Status: hazard.Active}
		if i%2 == 1 {
			h.Type, h.Severity = hazard.IceSnow, hazard.Minor
		}
		live = append(live, h)
	}
	snap := overlay.Build(1, live)
	fmt.Printf("queries: %d per profile, random node pairs in the profile's usable network, seed %d, %d live hazards\n\n", *n, *seed, snap.Len())
	fmt.Printf("%-12s %8s %8s %8s %8s | %9s %9s | %9s %9s\n",
		"profile", "p50", "p95", "p99", "max", "exp mean", "exp p95", "crow mean", "route mean")

	for _, p := range cost.Profiles {
		tableStart := time.Now()
		table := cost.Table(g, p)
		tableTime := time.Since(tableStart)
		// Same cost function the API uses: base table, then the overlay snapshot on top.
		costFn := func(e int32) float64 { return snap.Cost(p, e, table[e]) }

		// Pick query endpoints only from the profile's main network, so every query has an
		// answer and we measure search time, not "no route" failures.
		comp, sizes := g.Components(func(e int32) bool { return !math.IsInf(table[e], 1) })
		main := int32(slices.Index(sizes, slices.Max(sizes)))
		var pool []int32
		for i, c := range comp {
			if c == main {
				pool = append(pool, int32(i))
			}
		}

		// Fixed seed: every run (and every benchmark day) uses the same query pairs, so
		// numbers are comparable over time.
		r := rand.New(rand.NewSource(*seed))
		lat := make([]time.Duration, 0, *n)
		exp := make([]int, 0, *n)
		var crow, length float64
		noPath := 0
		for q := 0; q < *n; q++ {
			src, dst := pool[r.Intn(len(pool))], pool[r.Intn(len(pool))]
			// Time only the search itself.
			start := time.Now()
			path, err := route.AStar(g, costFn, src, dst)
			lat = append(lat, time.Since(start))
			if err != nil {
				// Only possible when hazards cut the network; count it but keep the timing
				// (a failed search is real work the server would do too).
				noPath++
				continue
			}
			exp = append(exp, path.Expanded)
			a, b := g.Nodes[src], g.Nodes[dst]
			crow += geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
			length += path.LengthM
		}
		// Sort so percentiles are just indexes into the slice.
		slices.Sort(lat)
		slices.Sort(exp)
		mean := 0
		for _, x := range exp {
			mean += x
		}
		ok := len(exp)
		fmt.Printf("%-12s %8s %8s %8s %8s | %9d %9d | %8.0fm %8.0fm   (cost table %s, no path %d)\n", p,
			pct(lat, 50), pct(lat, 95), pct(lat, 99), lat[len(lat)-1].Round(time.Microsecond),
			mean/ok, exp[ok*95/100], crow/float64(ok), length/float64(ok), tableTime.Round(time.Microsecond), noPath)
	}
}

// pct returns the p-th percentile of an already sorted slice (nearest-rank method).
func pct(sorted []time.Duration, p int) time.Duration {
	return sorted[(len(sorted)-1)*p/100].Round(time.Microsecond)
}

// heapAfterGC forces a garbage collection and returns the live heap size in bytes.
func heapAfterGC() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}
