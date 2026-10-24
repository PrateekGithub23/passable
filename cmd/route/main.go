// Command route runs A* between two points for one or all profiles and prints a summary.
//
//	go run ./cmd/route -from 43.4697,-80.5421 -to 43.4730,-80.5395 [-profile wheelchair]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PrateekGithub23/passable/internal/cost"
	"github.com/PrateekGithub23/passable/internal/geo"
	"github.com/PrateekGithub23/passable/internal/graph"
	"github.com/PrateekGithub23/passable/internal/route"
	"github.com/jackc/pgx/v5"
)

func main() {
	dbURL := flag.String("db", "postgres://passable:passable@localhost:5433/passable", "Postgres connection URL")
	fromS := flag.String("from", "", "start as lat,lng")
	toS := flag.String("to", "", "end as lat,lng")
	profileS := flag.String("profile", "", "profile (default: all)")
	flag.Parse()

	from, err := parseLatLng(*fromS)
	if err != nil {
		log.Fatalf("-from: %v", err)
	}
	to, err := parseLatLng(*toS)
	if err != nil {
		log.Fatalf("-to: %v", err)
	}
	profiles := cost.Profiles
	if *profileS != "" {
		p, err := cost.ParseProfile(*profileS)
		if err != nil {
			log.Fatal(err)
		}
		profiles = []cost.Profile{p}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	g, err := graph.Load(ctx, conn)
	conn.Close(ctx)
	if err != nil {
		log.Fatalf("load graph: %v", err)
	}

	_, sizes := g.Components(nil)
	fmt.Printf("graph: %d nodes, %d edges, %d components (largest %d nodes)\n\n",
		len(g.Nodes), len(g.Edges), len(sizes), slices.Max(sizes))

	paths := map[cost.Profile]route.Path{}
	for _, p := range profiles {
		table := cost.Table(g, p)
		// Snap onto the largest component of edges this profile can use, so a start inside a
		// steps-only pocket does not doom the search.
		comp, sizes := g.Components(func(e int32) bool { return !math.IsInf(table[e], 1) })
		main := int32(slices.Index(sizes, slices.Max(sizes)))
		src, srcD := nearestNode(g, comp, main, from)
		dst, dstD := nearestNode(g, comp, main, to)
		fmt.Printf("%-12s snap: from %.1f m, to %.1f m (usable network %d nodes)\n", p, srcD, dstD, sizes[main])
		start := time.Now()
		path, err := route.AStar(g, func(e int32) float64 { return table[e] }, src, dst)
		elapsed := time.Since(start)
		if errors.Is(err, route.ErrNoPath) {
			fmt.Printf("%-12s no route (expanded %d nodes in %s)\n", p, path.Expanded, elapsed)
			continue
		}
		paths[p] = path
		printSummary(g, p, path, elapsed)
	}

	if len(paths) > 1 {
		fmt.Println("\nshared length between profiles:")
		for i, a := range profiles {
			for _, b := range profiles[i+1:] {
				pa, okA := paths[a]
				pb, okB := paths[b]
				if okA && okB {
					fmt.Printf("  %-11s vs %-11s %5.1f%%\n", a, b, 100*sharedLength(g, pa, pb)/math.Max(pa.LengthM, pb.LengthM))
				}
			}
		}
	}
}

func printSummary(g *graph.Graph, p cost.Profile, path route.Path, elapsed time.Duration) {
	var steps, crossings int
	conf := map[cost.Confidence]float64{}
	reasons := map[string]int{}
	for _, ei := range path.Edges {
		e := &g.Edges[ei]
		if e.HasSteps {
			steps++
		}
		if e.IsCrossing {
			crossings++
		}
		r := cost.Evaluate(p, e)
		conf[r.Confidence] += e.LengthM
		for _, why := range r.Reasons {
			reasons[why]++
		}
	}
	fmt.Printf("%-12s %6.0f m  cost %6.0f  edges %3d  steps %d  crossings %d  expanded %5d  %s\n",
		p, path.LengthM, path.Cost, len(path.Edges), steps, crossings, path.Expanded, elapsed.Round(time.Microsecond))
	fmt.Printf("%-12s confidence: verified %.0f m, inferred %.0f m, unknown %.0f m\n", "",
		conf[cost.Verified], conf[cost.Inferred], conf[cost.Unknown])
	if len(reasons) > 0 {
		var parts []string
		for why, n := range reasons {
			parts = append(parts, fmt.Sprintf("%s x%d", why, n))
		}
		fmt.Printf("%-12s penalties: %s\n", "", strings.Join(parts, ", "))
	}
}

// sharedLength sums the length of edges that appear in both paths.
func sharedLength(g *graph.Graph, a, b route.Path) float64 {
	in := map[int32]bool{}
	for _, e := range a.Edges {
		in[e] = true
	}
	total := 0.0
	for _, e := range b.Edges {
		if in[e] {
			total += g.Edges[e].LengthM
		}
	}
	return total
}

type latLng struct{ lat, lng float64 }

func parseLatLng(s string) (latLng, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return latLng{}, fmt.Errorf("want lat,lng, got %q", s)
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return latLng{}, fmt.Errorf("want lat,lng, got %q", s)
	}
	return latLng{lat, lng}, nil
}

// nearestNode is a linear scan restricted to one component; edge snapping via PostGIS
// replaces it in the API.
func nearestNode(g *graph.Graph, comp []int32, want int32, p latLng) (int32, float64) {
	best, bestD := int32(-1), math.Inf(1)
	for i, n := range g.Nodes {
		if comp[i] != want {
			continue
		}
		if d := geo.Haversine(p.lat, p.lng, n.Lat, n.Lon); d < bestD {
			best, bestD = int32(i), d
		}
	}
	return best, bestD
}
