// Command bench measures A* latency and search effort on random queries over the real graph.
//
//	go run ./cmd/bench [-n 2000] [-seed 42]
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
	"github.com/PrateekGithub23/passable/internal/route"
	"github.com/jackc/pgx/v5"
)

func main() {
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	n := flag.Int("n", 2000, "queries per profile")
	seed := flag.Int64("seed", 42, "random seed for query pairs")
	flag.Parse()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
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
	fmt.Printf("queries: %d per profile, random node pairs in the profile's usable network, seed %d\n\n", *n, *seed)
	fmt.Printf("%-12s %8s %8s %8s %8s | %9s %9s | %9s %9s\n",
		"profile", "p50", "p95", "p99", "max", "exp mean", "exp p95", "crow mean", "route mean")

	for _, p := range cost.Profiles {
		tableStart := time.Now()
		table := cost.Table(g, p)
		tableTime := time.Since(tableStart)
		costFn := func(e int32) float64 { return table[e] }

		comp, sizes := g.Components(func(e int32) bool { return !math.IsInf(table[e], 1) })
		main := int32(slices.Index(sizes, slices.Max(sizes)))
		var pool []int32
		for i, c := range comp {
			if c == main {
				pool = append(pool, int32(i))
			}
		}

		r := rand.New(rand.NewSource(*seed))
		lat := make([]time.Duration, 0, *n)
		exp := make([]int, 0, *n)
		var crow, length float64
		for q := 0; q < *n; q++ {
			src, dst := pool[r.Intn(len(pool))], pool[r.Intn(len(pool))]
			start := time.Now()
			path, err := route.AStar(g, costFn, src, dst)
			lat = append(lat, time.Since(start))
			if err != nil {
				log.Fatalf("%s: %d->%d in the same component returned %v", p, src, dst, err)
			}
			exp = append(exp, path.Expanded)
			a, b := g.Nodes[src], g.Nodes[dst]
			crow += geo.Haversine(a.Lat, a.Lon, b.Lat, b.Lon)
			length += path.LengthM
		}
		slices.Sort(lat)
		slices.Sort(exp)
		mean := 0
		for _, x := range exp {
			mean += x
		}
		fmt.Printf("%-12s %8s %8s %8s %8s | %9d %9d | %8.0fm %8.0fm   (cost table %s)\n", p,
			pct(lat, 50), pct(lat, 95), pct(lat, 99), lat[len(lat)-1].Round(time.Microsecond),
			mean/len(exp), exp[len(exp)*95/100], crow/float64(*n), length/float64(*n), tableTime.Round(time.Microsecond))
	}
}

func pct(sorted []time.Duration, p int) time.Duration {
	return sorted[(len(sorted)-1)*p/100].Round(time.Microsecond)
}

func heapAfterGC() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}
